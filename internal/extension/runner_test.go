package extension

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The tests below pin the decision protocol: what a hook's stdout means, and
// which failures are allowed to stop a call. The rule throughout is that only
// an explicit, unambiguous verdict blocks; everything else defers.

// run is RunHook against a single tool call, which is the only context the
// blocking path ever supplies.
func run(t *testing.T, hooks ...HookConfig) Outcome {
	t.Helper()
	return RunHook(context.Background(), hooks, EventPreToolUse,
		map[string]any{"tool": "bash"}, "bash")
}

func TestRunHookDecisions(t *testing.T) {
	cases := []struct {
		name string
		body string
		want Decision
	}{
		{"allow", `{"decision":"allow"}`, DecisionAllow},
		{"deny", `{"decision":"deny","reason":"not on main"}`, DecisionDeny},
		{"ask", `{"decision":"ask","reason":"confirm"}`, DecisionAsk},
		{"explicit defer", `{"decision":""}`, DecisionDefer},
		{"empty decision", `{"reason":"just noting"}`, DecisionDefer},
		{"continue true", `{"continue":true}`, DecisionAllow},
		{"continue false", `{"continue":false,"reason":"must confirm"}`, DecisionDeny},
		{"silence", ``, DecisionDefer},
		{"bare text", `thinking about it`, DecisionDefer},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := run(t, HookConfig{
				Event:   "PreToolUse",
				Command: `printf '%s'` + "'" + tc.body + "'",
				Timeout: 5,
			})
			if got.Decision != tc.want {
				t.Errorf("Decision = %q, want %q (reason %q)", got.Decision, tc.want, got.Reason)
			}
		})
	}
}

// A reason is how a hook tells the agent what to do instead, so it has to
// survive parsing on every path that produces a decision.
func TestRunHookReasonsSurvive(t *testing.T) {
	got := run(t, HookConfig{
		Event:   "PreToolUse",
		Command: `printf '{"decision":"deny","reason":"main is protected"}'`,
		Timeout: 5,
	})
	if got.Reason != "main is protected" {
		t.Errorf("Reason = %q, want the hook's own words", got.Reason)
	}
}

// A hook printing a debug line must not be read as a denial. Reading a log line
// as a veto would stop the agent for no reason, and the author would have no way
// to tell why.
func TestRunHookBareTextIsNotAVeto(t *testing.T) {
	got := run(t, HookConfig{Event: "PreToolUse", Command: "echo checking policy", Timeout: 5})
	if got.Decision != DecisionDefer {
		t.Errorf("Decision = %q, want defer for bare text", got.Decision)
	}
	if got.Reason != "checking policy" {
		t.Errorf("Reason = %q, want the text kept as a reason", got.Reason)
	}
}

// Hooks conventionally `exit 1` to deny, so a non-zero exit carrying a verdict
// is an answer rather than a crash.
func TestRunHookNonZeroExitWithVerdictIsAnAnswer(t *testing.T) {
	got := run(t, HookConfig{
		Event:   "PreToolUse",
		Command: `printf '{"decision":"deny","reason":"blocked"}'; exit 1`,
		Timeout: 5,
	})
	if got.Decision != DecisionDeny {
		t.Errorf("Decision = %q, want deny", got.Decision)
	}
	if got.Reason != "blocked" {
		t.Errorf("Reason = %q, want blocked", got.Reason)
	}
}

// A non-zero exit with no verdict is a failure — a missing command, a syntax
// error. Reading it as a silent defer would let a typo disable a hook without a
// word, which is the one outcome worse than the hook being visibly broken.
func TestRunHookNonZeroExitWithoutVerdictIsAFailure(t *testing.T) {
	var logged []string
	SetHookLogger(func(msg string, _ ...any) { logged = append(logged, msg) })
	t.Cleanup(func() { SetHookLogger(nil) })

	got := run(t, HookConfig{
		Event:   "PreToolUse",
		Command: "this-command-does-not-exist-pi-go",
		Timeout: 5,
	})
	if got.Decision != DecisionDefer {
		t.Errorf("Decision = %q, want defer for a failed hook", got.Decision)
	}
	if len(logged) != 1 {
		t.Fatalf("logged %d failures, want 1: %v", len(logged), logged)
	}
	if !strings.Contains(logged[0], "failed") {
		t.Errorf("log = %q, want it to say the hook failed", logged[0])
	}
}

// A hook that cannot run must not be the reason the agent stops working. The
// failure is visible in the log; the call carries on to the rules.
func TestRunHookFailureDoesNotVeto(t *testing.T) {
	var logged []string
	SetHookLogger(func(msg string, _ ...any) { logged = append(logged, msg) })
	t.Cleanup(func() { SetHookLogger(nil) })

	if _, err := runHook(context.Background(),
		HookConfig{Event: "PreToolUse", Command: "this-command-does-not-exist-pi-go", Timeout: 5},
		map[string]any{}); err == nil {
		t.Error("a hook that could not run reported success")
	}
}

func TestRunHookTimeout(t *testing.T) {
	start := time.Now()
	_, err := runHook(context.Background(),
		HookConfig{Event: "PreToolUse", Command: "sleep 10", Timeout: 1},
		map[string]any{})
	if err == nil {
		t.Fatal("a slow hook did not time out")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want it to name the timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Errorf("hook ran for %s, want it cut off near its 1s timeout", elapsed)
	}
}

// The chain folds left to right and stops at the first terminal decision: hooks
// are listed most-specific-first, so the first hook to answer is the author's
// chosen authority.
func TestRunHookFoldsLeftToRight(t *testing.T) {
	got := run(t,
		HookConfig{Event: "PreToolUse", Command: `printf '{"decision":"allow"}'`, Timeout: 5},
		HookConfig{Event: "PreToolUse", Command: `printf '{"decision":"deny","reason":"second"}'`, Timeout: 5},
	)
	if got.Decision != DecisionAllow {
		t.Errorf("Decision = %q, want the first hook's allow to win", got.Decision)
	}
}

func TestRunHookDenyWins(t *testing.T) {
	got := run(t,
		HookConfig{Event: "PreToolUse", Command: `printf '{"decision":"deny","reason":"first"}'`, Timeout: 5},
		HookConfig{Event: "PreToolUse", Command: `printf '{"decision":"allow"}'`, Timeout: 5},
	)
	if got.Decision != DecisionDeny {
		t.Errorf("Decision = %q, want the first hook's deny to be terminal", got.Decision)
	}
	if got.Reason != "first" {
		t.Errorf("Reason = %q, want first — the later hook should not be consulted", got.Reason)
	}
}

// A deny anywhere in the chain outranks an ask earlier on, because an ask is a
// request to consult the rules and that happens after every hook has had a say.
func TestRunHookDenyBeatsEarlierAsk(t *testing.T) {
	got := run(t,
		HookConfig{Event: "PreToolUse", Command: `printf '{"decision":"ask"}'`, Timeout: 5},
		HookConfig{Event: "PreToolUse", Command: `printf '{"decision":"deny","reason":"no"}'`, Timeout: 5},
	)
	if got.Decision != DecisionDeny {
		t.Errorf("Decision = %q, want deny", got.Decision)
	}
}

func TestRunHookAskReachesTheEnd(t *testing.T) {
	got := run(t,
		HookConfig{Event: "PreToolUse", Command: `printf '{"decision":"ask","reason":"touches main"}'`, Timeout: 5},
		HookConfig{Event: "PreToolUse", Command: `printf '{"decision":"ask","reason":"writes config"}'`, Timeout: 5},
	)
	if got.Decision != DecisionAsk {
		t.Fatalf("Decision = %q, want ask", got.Decision)
	}
	if !strings.Contains(got.Reason, "touches main") || !strings.Contains(got.Reason, "writes config") {
		t.Errorf("Reason = %q, want both hooks' reasons", got.Reason)
	}
}

// Every defer in the chain is still an ask-free defer: a hook that says nothing
// must not be reported as a request for confirmation.
func TestRunHookAllDeferIsDefer(t *testing.T) {
	got := run(t,
		HookConfig{Event: "PreToolUse", Command: "echo one", Timeout: 5},
		HookConfig{Event: "PreToolUse", Command: "echo two", Timeout: 5},
	)
	if got.Decision != DecisionDefer {
		t.Errorf("Decision = %q, want defer", got.Decision)
	}
}

func TestRunHookSkipsOtherEvents(t *testing.T) {
	got := run(t,
		HookConfig{Event: "PostToolUse", Command: `printf '{"decision":"deny"}'`, Timeout: 5},
		HookConfig{Event: "Nonsense", Command: `printf '{"decision":"deny"}'`, Timeout: 5},
	)
	if got.Decision != DecisionDefer {
		t.Errorf("Decision = %q, want defer — a hook for another event must not fire", got.Decision)
	}
}

func TestRunHookRespectsToolFilter(t *testing.T) {
	deny := HookConfig{
		Event:   "PreToolUse",
		Tools:   []string{"write"},
		Command: `printf '{"decision":"deny","reason":"no"}'`,
		Timeout: 5,
	}
	if got := RunHook(context.Background(), []HookConfig{deny}, EventPreToolUse, nil, "bash"); got.Decision != DecisionDefer {
		t.Errorf("Decision = %q, want defer for a tool the hook does not match", got.Decision)
	}
	if got := RunHook(context.Background(), []HookConfig{deny}, EventPreToolUse, nil, "write"); got.Decision != DecisionDeny {
		t.Errorf("Decision = %q, want deny for the tool the hook matches", got.Decision)
	}
}

// A hook with no tool filter fires for every tool, which is the documented
// meaning of an unqualified hook.
func TestRunHookUnqualifiedMatchesEveryTool(t *testing.T) {
	deny := HookConfig{
		Event:   "PreToolUse",
		Command: `printf '{"decision":"deny","reason":"no"}'`,
		Timeout: 5,
	}
	for _, tool := range []string{"bash", "write", "read"} {
		if got := RunHook(context.Background(), []HookConfig{deny}, EventPreToolUse, nil, tool); got.Decision != DecisionDeny {
			t.Errorf("%q: Decision = %q, want deny", tool, got.Decision)
		}
	}
}

// The hook sees what it is being asked about. A hook registered for several
// events branches on `event`, and one that has to guess which event it is being
// called for gets the timestamp wrong.
func TestRunHookPayloadCarriesEventAndTool(t *testing.T) {
	dir := filepath.ToSlash(t.TempDir())
	out := dir + "/payload.json"
	// Through the fold, not runHook directly: stamping the event is the fold's
	// job, because a tool hook's tool is already in the caller's payload.
	RunHook(context.Background(), []HookConfig{{
		Event:   "PreToolUse",
		Command: `cat > "` + out + `"`,
		Timeout: 5,
	}}, EventPreToolUse, map[string]any{"tool": "bash", "arg": "ls -la"}, "bash")

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading payload: %v", err)
	}
	body := string(data)
	for _, want := range []string{`"event":"PreToolUse"`, `"tool":"bash"`, `"arg":"ls -la"`} {
		if !strings.Contains(body, want) {
			t.Errorf("payload %s missing %s", body, want)
		}
	}
	// The tool appears once, not twice. Writing it again from a default would
	// produce two keys in one JSON object with the last silently winning.
	if strings.Count(body, `"tool"`) != 1 {
		t.Errorf("payload %s repeats the tool key", body)
	}
}

// Hook stdout carries the verdict, so it must be captured and never printed.
// Inheriting it would paint the verdict onto the TUI's alternate screen.
func TestRunHookDoesNotWriteToStdout(t *testing.T) {
	got := run(t, HookConfig{
		Event:   "PreToolUse",
		Command: `echo '{"decision":"deny","reason":"secret policy detail"}'`,
		Timeout: 5,
	})
	if got.Reason != "secret policy detail" {
		t.Errorf("Reason = %q, want the verdict captured rather than printed", got.Reason)
	}
}

// HookRunner is the seam's view: one call, one decision.
func TestHookRunnerRunDecision(t *testing.T) {
	r := NewHookRunner([]HookConfig{
		{Event: "PreToolUse", Command: `printf '{"decision":"ask","reason":"confirm"}'`, Timeout: 5},
	})
	got := r.RunDecision(context.Background(), "PreToolUse", map[string]any{"tool": "bash"}, "bash")
	if got.Decision != "ask" || got.Reason != "confirm" {
		t.Errorf("RunDecision = %+v, want ask/confirm", got)
	}
}

// An unrecognised event name is a no-match, not a "fires for everything": the
// seam names events as strings, and a typo that silently widened a hook would be
// the worst possible failure mode for a guard.
func TestHookRunnerUnknownEventIsDefer(t *testing.T) {
	r := NewHookRunner([]HookConfig{
		{Event: "PreToolUse", Command: `printf '{"decision":"deny"}'`, Timeout: 5},
	})
	got := r.RunDecision(context.Background(), "pretooluse", nil, "bash")
	if got.Decision != "" {
		t.Errorf("Decision = %q, want defer for an unrecognised event name", got.Decision)
	}
}

// The legacy spelling reaches the runner too, so a config written against the
// old names blocks rather than quietly becoming inert.
func TestHookRunnerAcceptsLegacyEventName(t *testing.T) {
	r := NewHookRunner([]HookConfig{
		{Event: "before_tool", Command: `printf '{"decision":"deny","reason":"legacy"}'`, Timeout: 5},
	})
	got := r.RunDecision(context.Background(), "before_tool", nil, "bash")
	if got.Decision != "deny" {
		t.Errorf("Decision = %q, want deny", got.Decision)
	}
}

// A failure goes to the runner's own sink, not the package one: ACP serves
// several sessions from one process, and the last writer must not decide where
// every session's failures go.
func TestHookRunnerLoggerIsPerRunner(t *testing.T) {
	var global []string
	SetHookLogger(func(msg string, _ ...any) { global = append(global, "global: "+msg) })
	t.Cleanup(func() { SetHookLogger(nil) })

	var mine []string
	r := NewHookRunner([]HookConfig{
		{Event: "PreToolUse", Command: "this-command-does-not-exist-pi-go", Timeout: 5},
	})
	r.SetLogger(func(msg string, _ ...any) { mine = append(mine, msg) })
	r.RunDecision(context.Background(), "PreToolUse", nil, "bash")

	if len(mine) != 1 {
		t.Errorf("runner sink got %d messages, want 1", len(mine))
	}
	if len(global) != 0 {
		t.Errorf("package sink got %v, want nothing — the runner overrides it", global)
	}
}

// Fire is the observational path: it runs the hooks for an event that has no
// tool, and its answer is not enforced.
func TestHookRunnerFireLifecycleEvent(t *testing.T) {
	dir := filepath.ToSlash(t.TempDir())
	marker := dir + "/started"
	r := NewHookRunner([]HookConfig{
		{Event: "SessionStart", Command: `echo hi > "` + marker + `"`, Timeout: 5},
		{Event: "PreToolUse", Command: `printf '{"decision":"deny"}'`, Timeout: 5},
	})
	out := r.Fire(context.Background(), EventSessionStart, map[string]any{"session": "s1"})
	if out.Decision != DecisionDefer {
		t.Errorf("Decision = %q, want defer", out.Decision)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("SessionStart hook did not run: %v", err)
	}
}
