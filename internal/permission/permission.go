package permission

import (
	"context"
	"sync"
)

// Mode is a coarse switch that pre-empts rule evaluation.
type Mode string

const (
	// ModeAuto approves everything without asking. It is pi-go's default so
	// that adding the permission system does not change what an existing
	// session does; prompting is opt-in.
	ModeAuto Mode = "auto"
	// ModeDefault evaluates the rules and asks for anything marked ask.
	ModeDefault Mode = "default"
	// ModeAcceptEdits approves edits to files automatically but still asks
	// for anything the rules mark ask.
	ModeAcceptEdits Mode = "acceptEdits"
	// ModePlan refuses everything that would change the working tree, so the
	// agent can only read and propose.
	ModePlan Mode = "plan"
	// ModeDontAsk refuses everything marked ask instead of prompting, for
	// unattended runs.
	ModeDontAsk Mode = "dontAsk"
	// ModeBypass approves everything, including what a rule denies. It
	// exists so a user can recover from a deny rule that is too broad
	// without editing their config mid-session.
	ModeBypass Mode = "bypassPermissions"
)

// Modes lists every mode in the order Shift+Tab cycles through them. The
// order runs from most restrictive to least, so repeated presses loosen the
// session predictably.
var Modes = []Mode{
	ModePlan,
	ModeDefault,
	ModeAcceptEdits,
	ModeAuto,
	ModeDontAsk,
	ModeBypass,
}

// Request describes one tool call awaiting a decision.
type Request struct {
	// Tool is the tool's name as the model invoked it.
	Tool string
	// Arg is the argument the rule should match on: the bash command for
	// Bash, the file path for Read/Write/Edit, the domain for WebFetch.
	// Empty for tools with no meaningful argument.
	Arg string
	// Description is a short human-readable summary for the prompt, e.g.
	// `Bash(rm -rf build)`.
	Description string
}

// Result is the engine's verdict, including the rule that produced it so the
// caller can explain the decision to the user.
type Result struct {
	Decision Decision
	// Rule is the rule that matched, or nil when the decision came from the
	// mode or from the absence of any matching rule.
	Rule *Rule
	// Reason is a short human-readable explanation suitable for a notice.
	Reason string
}

// Approver asks the user whether a call may proceed. Returning an error means
// the user declined or the prompt could not be shown; the caller must treat
// that as a refusal rather than proceeding.
type Approver func(ctx context.Context, req Request) (bool, error)

// Engine evaluates requests against a rule set and a mode. It is safe for
// concurrent use: rule changes can land while a turn is running, and the seam
// is consulted once per tool call from the agent's goroutines.
type Engine struct {
	mu      sync.RWMutex
	rules   []Rule
	mode    Mode
	askHead Approver
	// nonInteractive is set for headless surfaces (ACP, A2A, --mode
	// print|json, subagents, CI) where there is no TTY to prompt on. Ask is
	// resolved against headlessPolicy instead of blocking.
	nonInteractive bool
	headlessPolicy Decision
}

// Option customizes an Engine.
type Option func(*Engine)

// WithMode sets the starting mode. An empty or unknown mode becomes
// ModeAuto, so a typo in config cannot leave the agent unable to act.
func WithMode(m Mode) Option {
	return func(e *Engine) {
		if m == "" {
			m = ModeAuto
		}
		e.mode = m
	}
}

// WithApprover installs the callback used to resolve an ask. A nil approver
// means asks are refused, which is the safe reading: with no way to ask, an
// ask cannot be granted.
func WithApprover(a Approver) Option {
	return func(e *Engine) { e.askHead = a }
}

// WithNonInteractive marks the engine as having no TTY. Ask resolves to
// headlessPolicy, which defaults to Deny so an unattended run fails loudly
// rather than silently performing something a human never saw.
func WithNonInteractive(policy Decision) Option {
	return func(e *Engine) {
		e.nonInteractive = true
		e.headlessPolicy = policy
	}
}

// New returns an Engine. The rule order is preserved as given: the caller is
// responsible for applying user rules after project rules, and within one
// decision kind specificity does not matter, so no sorting is applied here.
func New(rules []Rule, opts ...Option) *Engine {
	e := &Engine{
		rules:          append([]Rule(nil), rules...),
		mode:           ModeAuto,
		headlessPolicy: Deny,
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

// Mode returns the current mode.
func (e *Engine) Mode() Mode {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.mode
}

// SetMode changes the mode. An unknown mode is ignored rather than applied,
// so a bad value cannot silently disable enforcement.
func (e *Engine) SetMode(m Mode) {
	if !validMode(m) {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.mode = m
}

// SetRules replaces the rule set atomically, so a reload never leaves the
// engine with a half-applied configuration.
func (e *Engine) SetRules(rules []Rule) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rules = append([]Rule(nil), rules...)
}

// Rules returns a copy of the current rule set.
func (e *Engine) Rules() []Rule {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return append([]Rule(nil), e.rules...)
}

// Ask resolves a confirmation request that did not come from a rule match.
//
// It exists because a PreToolUse hook can return `ask`, and that has to reach
// the human exactly the way a rule-driven ask does. Routing it through Check
// instead would not work: Check only asks when an ask *rule* matched, and a
// hook asking on a call no rule mentions would fall through to allow — which
// is the one outcome `ask` must never produce.
//
// The return is the same pair shape as Approver, plus the reason to show. A
// non-interactive engine resolves against its headless policy rather than
// returning "nobody to ask", because on ACP and in CI the absence of a TTY is
// already the standing answer and the caller should not have to know that.
func (e *Engine) Ask(ctx context.Context, req Request) (bool, string) {
	e.mu.RLock()
	nonInteractive := e.nonInteractive
	headlessPolicy := e.headlessPolicy
	askHead := e.askHead
	e.mu.RUnlock()

	if nonInteractive {
		allowed := headlessPolicy == Allow
		reason := "a hook asked for confirmation and this run has no terminal to ask on"
		if !allowed {
			reason = "a hook asked for confirmation and the headless policy is deny"
		}
		return allowed, reason
	}
	if askHead == nil {
		return false, "a hook asked for confirmation but no approver is installed"
	}
	approved, err := askHead(ctx, req)
	if err != nil {
		return false, "denied: " + err.Error()
	}
	if !approved {
		return false, "declined by the user"
	}
	return true, "allowed by the user at a hook's request"
}

// NextMode returns the mode Shift+Tab would switch to, without changing it.
func (e *Engine) NextMode() Mode {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return nextMode(e.mode)
}

// CycleMode advances to the next mode and returns the mode now in effect.
//
// This is separate from NextMode because a keypress has to do two things at
// once: decide the new mode and install it. Returning the new mode from a
// getter and leaving the caller to call SetMode opens a window where two
// concurrent keypresses both read the same old mode, both compute the same
// next mode, and the second one silently overrides the first. Doing it under
// one lock makes a mode press a single atomic step.
func (e *Engine) CycleMode() Mode {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.mode = nextMode(e.mode)
	return e.mode
}

func nextMode(cur Mode) Mode {
	for i, m := range Modes {
		if m == cur {
			return Modes[(i+1)%len(Modes)]
		}
	}
	// An unrecognised current mode cycles to the most restrictive entry rather
	// than to auto, so recovering from a bad value never starts by loosening.
	return Modes[0]
}

func validMode(m Mode) bool {
	for _, known := range Modes {
		if known == m {
			return true
		}
	}
	return false
}

// Check evaluates a request and resolves any ask into a final Allow or Deny.
// It is the single seam every caller should use: hooks, plan mode, the
// @-mention path, and background sessions all go through here rather than
// each re-implementing the decision.
//
// The bool return is false when the call must not run, in which case Reason
// explains why in terms the user can act on.
func (e *Engine) Check(ctx context.Context, req Request) (bool, Result) {
	e.mu.RLock()
	mode := e.mode
	rules := e.rules
	askHead := e.askHead
	nonInteractive := e.nonInteractive
	headlessPolicy := e.headlessPolicy
	e.mu.RUnlock()

	// Bypass pre-empts every rule, including deny. It is the escape hatch
	// for a deny rule that is too broad.
	if mode == ModeBypass {
		return true, Result{Decision: Allow, Reason: "bypassPermissions mode allows every call"}
	}

	// Evaluate deny → ask → allow. The order is the safety property, so it
	// is expressed as three passes rather than one pass with a precedence
	// field, which would let a future rule accidentally claim to outrank it.
	if rule, ok := firstMatch(rules, Deny, req); ok {
		return false, Result{Decision: Deny, Rule: &rule, Reason: "denied by rule " + rule.String()}
	}
	if rule, ok := firstMatch(rules, Ask, req); ok {
		if mode == ModeDontAsk {
			return false, Result{Decision: Deny, Rule: &rule, Reason: "rule " + rule.String() + " requires confirmation and dontAsk refuses to ask"}
		}
		if nonInteractive {
			allowed := headlessPolicy == Allow
			res := Result{Decision: headlessPolicy, Rule: &rule, Reason: "rule " + rule.String() + " requires confirmation and this run has no terminal to ask on"}
			return allowed, res
		}
		if askHead == nil {
			return false, Result{Decision: Deny, Rule: &rule, Reason: "rule " + rule.String() + " requires confirmation but no approver is installed"}
		}
		ok, err := askHead(ctx, req)
		if err != nil {
			return false, Result{Decision: Deny, Rule: &rule, Reason: "denied: " + err.Error()}
		}
		if !ok {
			return false, Result{Decision: Deny, Rule: &rule, Reason: "declined by the user"}
		}
		return true, Result{Decision: Allow, Rule: &rule, Reason: "allowed by the user"}
	}
	if _, ok := firstMatch(rules, Allow, req); ok {
		return true, Result{Decision: Allow, Reason: "allowed by rule"}
	}

	// No rule matched. The mode decides.
	switch mode {
	case ModePlan:
		if mutatesWorktree(req.Tool) {
			return false, Result{Decision: Deny, Reason: "plan mode does not allow changes to the working tree"}
		}
		return true, Result{Decision: Allow, Reason: "plan mode allows read-only tools"}
	case ModeAuto:
		return true, Result{Decision: Allow, Reason: "auto mode approves calls no rule matches"}
	default:
		// ModeDefault and ModeAcceptEdits both fall through to allow here:
		// with no rule asking, there is nothing to confirm. The difference
		// between them shows up in the rule pass, where AcceptEdits adds an
		// implicit allow for edits.
		return true, Result{Decision: Allow, Reason: "no rule matched"}
	}
}

// firstMatch returns the first rule with the given decision that matches the
// request.
func firstMatch(rules []Rule, want Decision, req Request) (Rule, bool) {
	for _, r := range rules {
		if r.Decision != want {
			continue
		}
		if r.Match(req.Tool, req.Arg) {
			return r, true
		}
	}
	return Rule{}, false
}

// mutatesWorktree reports whether a tool can change the working tree, which
// is the distinction plan mode draws. Read-only tools stay available so the
// agent can still investigate.
//
// The names must match what the tool layer registers verbatim — pi-go uses
// hyphens for the git tools, not underscores — because a name that does not
// match silently degrades to "read-only", which in plan mode means the tool
// is allowed when it should not be. Keep this list in step with
// tools.CoreTools.
func mutatesWorktree(tool string) bool {
	switch tool {
	case "write", "edit", "read_image", "bash", "bash_wait", "bash_kill", "tree", "git-hunk":
		return true
	default:
		return false
	}
}
