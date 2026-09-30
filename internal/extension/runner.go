package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/tools"
)

// Running a hook, and believing what it says.
//
// A hook is an arbitrary shell command, so two things have to be true before
// pi-go does what it says: it must have finished, and its answer must be
// unambiguous. Both are enforced here rather than at each call site, because
// the call sites are exactly where this gets skipped.

// Decision is what a blocking hook tells the seam to do.
//
// The four values are not four ways of saying "no". They differ in *who*
// decides next:
//
//   - Allow and Deny are terminal. The hook has made the call.
//   - Ask hands the decision to the next authority in the chain — the user's
//     permission rules — rather than making it itself.
//   - Defer is the absence of an opinion.
//
// Defer is the one that earns its place. A hook that does not care about a call
// should not have to guess at an answer, and a hook whose command simply failed
// must not be read as a veto — that would let a typo in one hook silently stop
// the agent working. So "no opinion" is a first-class value, distinct from
// "no".
type Decision string

const (
	// DecisionDefer means the hook has no opinion; evaluation continues.
	DecisionDefer Decision = ""
	// DecisionAllow means the hook approves the call outright.
	DecisionAllow Decision = "allow"
	// DecisionDeny means the hook refuses the call. Reason is shown to the model.
	DecisionDeny Decision = "deny"
	// DecisionAsk means the hook wants the user's permission rules to decide.
	DecisionAsk Decision = "ask"
)

// Outcome is one hook's answer.
type Outcome struct {
	// Decision is what the hook decided.
	Decision Decision
	// Reason is the human-readable explanation, passed to the model on a deny
	// and shown in the prompt on an ask. It is what a hook author uses to tell
	// the agent what to do instead.
	Reason string
	// Hook is the command that produced this outcome, for diagnostics.
	Hook string
}

// blocking reports whether this outcome stops evaluation of the chain.
func (o Outcome) blocking() bool { return o.Decision == DecisionAllow || o.Decision == DecisionDeny }

// HookResult is what a hook wrote on stdout.
//
// Both fields are read together rather than one at a time because a hook that
// says `{"decision":"deny"}` and a hook that says `{"reason":"…"}` are both
// valid, and a parser that required both would reject the first.
type HookResult struct {
	// Decision is the hook's verdict. Empty means defer.
	Decision Decision `json:"decision"`
	// Reason is the explanation, if any.
	Reason string `json:"reason,omitempty"`
	// Continue is the Claude Code spelling of "carry on". It is accepted so a
	// config ported from Claude Code behaves as its author expected: false blocks
	// the call, with the reason doing the explaining. It is deliberately not
	// merged into Decision — an author who writes both `continue: false` and
	// `decision: "ask"` has written something ambiguous, and picking silently
	// would hide the conflict.
	Continue *bool `json:"continue,omitempty"`
}

// runHook executes one hook command and parses its verdict.
//
// The shell is chosen rather than hardcoded to `sh -c`: the rest of pi-go
// already resolves a machine with no bash to PowerShell (internal/tools/shell.go),
// and a hook that could not run at all on Windows would be a hook system that
// silently does nothing on the platform it is most likely to be debugged on.
func runHook(ctx context.Context, hook HookConfig, payload map[string]any) (Outcome, error) {
	input, err := json.Marshal(payload)
	if err != nil {
		return Outcome{Decision: DecisionDefer, Hook: hook.Command}, fmt.Errorf("marshaling hook input: %w", err)
	}

	hookCtx, cancel := context.WithTimeout(ctx, hook.timeout())
	defer cancel()

	cmd := hookShellCommand(hookCtx, hook.Command)
	// exec.CommandContext kills the *shell*, not what the shell started, so
	// without this a `sleep 10` outlives its deadline and the timeout is not
	// enforced at all — the blocking path would freeze the agent for the whole
	// command, which is the thing the timeout exists to prevent.
	//
	// It is Windows-only because that is where the problem is: on Unix the
	// whole process group is already signalled. Windows has no such notion for
	// us to reach, so the process is killed directly.
	//
	// WaitDelay, not an explicit Wait: Cmd owns the wait itself and calling it
	// from here deadlocks. WaitDelay releases the handles and bounds the wait
	// on its own, while leaving Cmd.Run able to report the exit status — which
	// is what tells a timeout apart from a hook that failed by itself.
	if runtime.GOOS == "windows" {
		cmd.Cancel = func() error { return cmd.Process.Kill() }
		cmd.WaitDelay = 2 * time.Second
	}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// Stdout carries the decision, so it is captured rather than inherited.
	// Inheriting it would print the verdict onto the terminal, which corrupts
	// the TUI's alternate screen.
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	runErr := cmd.Run()
	if runErr != nil {
		// hookCtx, not ctx: the timeout is enforced on the derived context, so
		// ctx is still live here and checking it would report every failure as
		// a success.
		if hookCtx.Err() != nil {
			return Outcome{Decision: DecisionDefer, Hook: hook.Command},
				fmt.Errorf("hook timed out after %s", hook.timeout())
		}
		// A non-zero exit that still carries a verdict is an answer, not a
		// crash: hooks conventionally `exit 1` to deny, and treating that as a
		// failure would make the common case unexpressible. A non-zero exit
		// with no verdict *is* a failure — that is what a missing command or a
		// syntax error looks like, and reading it as a silent defer would let a
		// typo disable a hook without a word.
		if out, ok := parseHookResult(stdout.String(), hook); ok && out.Decision != DecisionDefer {
			return out, nil
		}
		return Outcome{Decision: DecisionDefer, Hook: hook.Command},
			fmt.Errorf("command %q: %w (stderr: %s)", hook.Command, runErr, strings.TrimSpace(stderr.String()))
	}

	if out, ok := parseHookResult(stdout.String(), hook); ok {
		return out, nil
	}
	// Exited cleanly and said nothing. That is the definition of defer.
	return Outcome{Decision: DecisionDefer, Hook: hook.Command}, nil
}

// parseHookResult reads a hook's stdout.
//
// Three shapes are accepted, in order: a JSON object with a `decision`, a JSON
// object with only `continue`/`reason`, and bare text. Bare text is treated as
// a reason for defer rather than as a decision, because a hook printing a debug
// line is far more likely than one printing an unmarked veto — and reading a
// log line as a denial would stop the agent for no reason.
func parseHookResult(stdout string, hook HookConfig) (Outcome, bool) {
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return Outcome{Decision: DecisionDefer, Hook: hook.Command}, true
	}

	if strings.HasPrefix(trimmed, "{") {
		var res HookResult
		if err := json.Unmarshal([]byte(trimmed), &res); err == nil {
			if res.Decision != DecisionDefer {
				return Outcome{Decision: res.Decision, Reason: res.Reason, Hook: hook.Command}, true
			}
			// No decision of its own. `continue` is the Claude Code spelling and
			// still decides on its own; otherwise this is a defer carrying at
			// most a reason.
			if res.Continue != nil {
				decision := DecisionAllow
				reason := ""
				if !*res.Continue {
					decision = DecisionDeny
					reason = res.Reason
					if reason == "" {
						reason = "a hook required confirmation and provided no reason"
					}
				}
				return Outcome{Decision: decision, Reason: reason, Hook: hook.Command}, true
			}
			return Outcome{Decision: DecisionDefer, Reason: res.Reason, Hook: hook.Command}, true
		}
		// Not JSON after all — a brace-leading log line. Treated as text.
	}

	// Bare text. Echoed on stderr by the hook author, not a verdict.
	return Outcome{Decision: DecisionDefer, Reason: trimmed, Hook: hook.Command}, true
}

// hookShellCommand builds the command that runs a hook's script.
//
// It resolves the shell the same way the bash tool does, so a hook and a tool
// call in the same session agree about what language a command is written in.
func hookShellCommand(ctx context.Context, script string) *exec.Cmd {
	if hookShellIsPowerShell() {
		return exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-Command", script)
	}
	return exec.CommandContext(ctx, "bash", "-c", script)
}

// hookShellIsPowerShell mirrors internal/tools.resolveShellKind: Windows with
// no bash on PATH gets PowerShell.
//
// It is duplicated rather than imported because internal/tools depends on this
// package (read_image.go), so importing it back would be a cycle. The two must
// stay in step: a hook that ran under bash on a machine whose tools run under
// PowerShell would fail in a way that looks like a broken hook rather than a
// broken shell choice.
var hookShellIsPowerShell = syncOnceValue(resolveHookShell)

func resolveHookShell() bool {
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("bash"); err != nil {
			return true
		}
	}
	return false
}

// syncOnceValue is a tiny stand-in for sync.OnceValue, kept local so this file
// does not depend on the Go version's stdlib surface for one call.
func syncOnceValue(f func() bool) func() bool {
	var (
		done bool
		val  bool
	)
	return func() bool {
		if !done {
			val = f()
			done = true
		}
		return val
	}
}

// RunHook runs every hook registered for an event, in configuration order, and
// folds the answers into one outcome.
//
// Folding is left to right and stops at the first terminal decision. That order
// is not arbitrary: hooks are listed most-specific-first in every config format
// that has one, so the first hook to say allow or deny is the author's chosen
// authority, and letting a later general hook overturn it would make specificity
// meaningless.
//
// `ask` and `defer` do not stop the chain. An ask is a request to consult the
// user's rules, which happens after every hook has had its say, so a hook that
// denies later still wins over one that asked earlier.
func RunHook(ctx context.Context, hooks []HookConfig, event Event, payload map[string]any, toolName string) Outcome {
	return runHooks(ctx, hooks, event, payload, toolName, nil)
}

// runHooks is RunHook with an explicit failure sink. Passing nil falls back to
// the package-level sink, which is what every pre-existing caller wants.
func runHooks(ctx context.Context, hooks []HookConfig, event Event, payload map[string]any, toolName string, logf func(string, ...any)) Outcome {
	if logf == nil {
		logf = hookLogf
	}
	var merged Outcome
	var asked []string
	for _, h := range hooks {
		parsed, err := ParseEvent(h.Event)
		if err != nil || parsed != event {
			continue
		}
		if toolName != "" && !matcherFrom(h.Tools, h.Matcher).matches(toolName) {
			continue
		}
		// Every hook sees the event name, whether or not it was selected by one.
		// A lifecycle event has no tool, and the direct runHook path does not
		// stamp one, so without this a hook that branches on `event` would see
		// it missing on exactly the events it is written for.
		out, err := runHook(ctx, h, withEvent(payload, event, ""))
		if err != nil {
			// A hook that failed has no opinion. It is logged rather than
			// allowed to veto, because a hook that cannot run — a missing tool,
			// a syntax error, a timeout — must not be the reason the agent stops
			// working. The failure is visible in the log either way.
			logf("hook %q failed for event %q: %v", h.Command, event, err)
			continue
		}
		switch out.Decision {
		case DecisionAllow, DecisionDeny:
			// Terminal: this hook has made the call. Defer and ask below are
			// deliberately not terminal — an ask is a request to consult the
			// rules, which happens after every hook has had its say, so a hook
			// that denies later still outranks one that asked earlier.
			return out
		case DecisionAsk:
			asked = append(asked, out.Reason)
		case DecisionDefer:
			if merged.Reason == "" {
				merged.Reason = out.Reason
			}
		}
	}
	if len(asked) > 0 {
		parts := make([]string, 0, len(asked))
		for _, r := range asked {
			if r != "" {
				parts = append(parts, r)
			}
		}
		if len(parts) == 0 {
			parts = append(parts, "a hook asked for confirmation")
		}
		return Outcome{Decision: DecisionAsk, Reason: strings.Join(parts, "; ")}
	}
	return merged
}

// HookRunner is the hook set as the permission seam sees it: one call that
// returns one decision.
//
// It is the whole public surface of the hook system on the blocking path, and
// it is deliberately one method. A seam that could select hooks, run them and
// fold the results itself would have to be reimplemented correctly at every
// call site; there is exactly one seam, so the folding lives here.
type HookRunner struct {
	hooks []HookConfig
	// logf receives per-hook failures for this runner alone. It is nil for the
	// CLI and ACP paths, where the package sink (the standard logger) is right,
	// and set by the TUI, whose stderr belongs to the alternate screen.
	logf func(string, ...any)
}

// NewHookRunner returns a runner over a hook set.
func NewHookRunner(hooks []HookConfig) *HookRunner {
	return &HookRunner{hooks: append([]HookConfig(nil), hooks...)}
}

// SetLogger installs a per-runner sink for hook failures.
//
// It is a method rather than an argument to RunHook because hook failures are
// reported from inside the fold loop, which has no caller to pass anything to.
// Setting the package sink instead would be wrong in a process that builds more
// than one runner — ACP serves several sessions from one process — where the
// last writer would decide where every session's failures went.
func (r *HookRunner) SetLogger(fn func(string, ...any)) { r.logf = fn }

// RunDecision implements the seam's hook interface.
//
// The event arrives as a string because the seam names events as strings; it is
// parsed here rather than at the call site so an unrecognised name is a
// no-match rather than a silent "fires for everything".
func (r *HookRunner) RunDecision(ctx context.Context, event string, payload map[string]any, toolName string) tools.HookDecision {
	parsed, err := ParseEvent(event)
	if err != nil {
		return tools.HookDecision{}
	}
	out := runHooks(ctx, r.hooks, parsed, payload, toolName, r.logf)
	return tools.HookDecision{Decision: string(out.Decision), Reason: out.Reason}
}

// Fire runs every hook for a non-blocking event.
//
// This is the entry point for the observational events — SessionStart, Stop,
// PreCompact, the worktree pair. Their answers are logged rather than enforced,
// so the return value exists to let a caller assert in a test that a hook ran;
// nothing branches on it. It blocks for the hook's timeout, so a caller on the
// TUI's render path must queue it rather than call it directly.
func (r *HookRunner) Fire(ctx context.Context, event Event, payload map[string]any) Outcome {
	return runHooks(ctx, r.hooks, event, payload, "", r.logf)
}

// withEvent stamps the event name and tool into the payload the hook sees.
//
// The event is sent even though the hook was selected by it, because a hook
// registered for several events branches on it, and a hook that has to guess
// which one it is being called for is a hook that gets the timestamp wrong.
//
// toolName is deliberately not defaulted to the tool. A caller that is asking
// about a tool already put the tool in its payload, and writing it twice would
// be two keys in a JSON object — the last one winning, silently.
func withEvent(payload map[string]any, event Event, toolName string) map[string]any {
	out := make(map[string]any, len(payload)+2)
	for k, v := range payload {
		out[k] = v
	}
	out["event"] = string(event)
	if toolName != "" {
		out["tool"] = toolName
	}
	return out
}

// DefaultHookTimeout is the time a hook gets when it does not ask for more.
//
// Ten seconds is chosen to sit under the shortest sensible user patience for a
// UI that has already stopped moving. A blocking PreToolUse hook holds the tool
// call, so a slow hook is a visibly frozen agent — which is why the timeout is
// enforced per hook rather than once for the whole chain.
const DefaultHookTimeout = 10 * time.Second
