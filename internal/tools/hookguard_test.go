package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/permission"
)

// The tests below pin the ordering that makes a hook worth having: a hook runs
// before the rules, a deny from either side wins over an allow from the other,
// and `ask` reaches a human rather than silently becoming "carry on".

// stubHook is a Hook that returns a fixed decision and records what it was
// asked about.
type stubHook struct {
	decision HookDecision
	// noDecision refuses every question, so a test can assert the guard asked
	// even when it is about to be refused.
	asked int
	tool  string
	arg   string
}

func (h *stubHook) RunDecision(_ context.Context, _ string, payload map[string]any, toolName string) HookDecision {
	h.asked++
	h.tool = toolName
	if a, ok := payload["arg"].(string); ok {
		h.arg = a
	}
	return h.decision
}

// fixedGuard is a rule engine that always returns one verdict. It deliberately
// does *not* implement Ask, so it stands in for a seam with nobody to prompt.
type fixedGuard struct {
	allowed bool
	reason  string
	calls   int
}

func (g *fixedGuard) Check(_ context.Context, _ permission.Request) (bool, permission.Result) {
	g.calls++
	d := permission.Deny
	if g.allowed {
		d = permission.Allow
	}
	return g.allowed, permission.Result{Decision: d, Reason: g.reason}
}

// askGuard is a fixedGuard that can also answer a confirmation, standing in for
// permission.Engine on a surface with a human attached.
type askGuard struct {
	fixedGuard
	askOK  bool
	askWhy string
	asks   int
}

func (g *askGuard) Ask(_ context.Context, _ permission.Request) (bool, string) {
	g.asks++
	return g.askOK, g.askWhy
}

func req() permission.Request {
	return permission.Request{Tool: "bash", Arg: "rm -rf build", Description: "Bash(rm -rf build)"}
}

func TestHookGuardDenyBeatsRuleAllow(t *testing.T) {
	rules := &fixedGuard{allowed: true, reason: "allowed by rule"}
	g := HookGuard{
		Hooks: &stubHook{decision: HookDecision{Decision: "deny", Reason: "no deleting build output"}},
		Next:  rules,
	}
	allowed, res := g.Check(context.Background(), req())
	if allowed {
		t.Fatal("hook deny was overridden by an allow rule")
	}
	if res.Decision != permission.Deny {
		t.Errorf("Decision = %v, want Deny", res.Decision)
	}
	// The rules must not even be consulted: a hook that denies has answered,
	// and asking again could only reverse a scripted decision.
	if rules.calls != 0 {
		t.Errorf("rules consulted %d times after a hook deny, want 0", rules.calls)
	}
	if !strings.Contains(res.Reason, "no deleting build output") {
		t.Errorf("Reason = %q, want the hook's reason", res.Reason)
	}
}

func TestHookGuardAllowIsTerminal(t *testing.T) {
	rules := &fixedGuard{allowed: false, reason: "denied by rule Bash(rm -*)"}
	g := HookGuard{
		Hooks: &stubHook{decision: HookDecision{Decision: "allow", Reason: "this exact call is fine"}},
		Next:  rules,
	}
	allowed, res := g.Check(context.Background(), req())
	if !allowed {
		t.Fatalf("hook allow was overridden by a deny rule: %s", res.Reason)
	}
	if rules.calls != 0 {
		t.Errorf("rules consulted %d times after a hook allow, want 0", rules.calls)
	}
}

func TestHookGuardDeferFallsThroughToRules(t *testing.T) {
	rules := &fixedGuard{allowed: false, reason: "denied by rule Bash(rm -*)"}
	g := HookGuard{
		Hooks: &stubHook{decision: HookDecision{}},
		Next:  rules,
	}
	allowed, res := g.Check(context.Background(), req())
	if allowed {
		t.Fatal("a deferring hook overrode a deny rule")
	}
	if rules.calls != 1 {
		t.Errorf("rules consulted %d times, want 1", rules.calls)
	}
	if res.Reason != "denied by rule Bash(rm -*)" {
		t.Errorf("Reason = %q, want the rule's own reason", res.Reason)
	}
}

// A hook is asked about the call, not about a generic tool: it is the one thing
// that lets a script look at the arguments.
func TestHookGuardPassesCallDetails(t *testing.T) {
	h := &stubHook{}
	g := HookGuard{Hooks: h, Next: &fixedGuard{allowed: true}}
	if _, _ = g.Check(context.Background(), req()); false {
		t.Fatal("unreachable")
	}
	if h.asked != 1 {
		t.Errorf("hook asked %d times, want 1", h.asked)
	}
	if h.tool != "bash" || h.arg != "rm -rf build" {
		t.Errorf("hook saw tool=%q arg=%q, want bash / rm -rf build", h.tool, h.arg)
	}
}

// A nil hook list must behave exactly as pi-go did before hooks could block:
// the rules decide and nothing else runs.
func TestHookGuardNilHooksIsPassThrough(t *testing.T) {
	rules := &fixedGuard{allowed: false, reason: "denied by rule"}
	g := HookGuard{Next: rules}
	allowed, res := g.Check(context.Background(), req())
	if allowed {
		t.Fatal("nil hooks let a denied call through")
	}
	if rules.calls != 1 {
		t.Errorf("rules consulted %d times, want 1", rules.calls)
	}
	_ = res
}

// A seam with neither hooks nor rules is an unguarded tool.
func TestHookGuardNoNextAllows(t *testing.T) {
	allowed, res := HookGuard{}.Check(context.Background(), req())
	if !allowed {
		t.Fatalf("an unguarded tool was denied: %s", res.Reason)
	}
}

// `ask` has to reach a human even when the rules would have allowed the call —
// that is the entire difference between ask and defer.
func TestHookGuardAskPromptsEvenWhenRulesAllow(t *testing.T) {
	rules := &askGuard{
		fixedGuard: fixedGuard{allowed: true, reason: "allowed by rule"},
		askOK:      true,
		askWhy:     "user said yes",
	}
	g := HookGuard{
		Hooks: &stubHook{decision: HookDecision{Decision: "ask", Reason: "this touches main"}},
		Next:  rules,
	}
	allowed, res := g.Check(context.Background(), req())
	if !allowed {
		t.Fatalf("ask was not granted after approval: %s", res.Reason)
	}
	if rules.calls != 1 {
		t.Errorf("rules consulted %d times, want 1 — the hook's ask is judged against them", rules.calls)
	}
	if rules.asks != 1 {
		t.Errorf("approver consulted %d times, want 1", rules.asks)
	}
	if !strings.Contains(res.Reason, "this touches main") {
		t.Errorf("Reason = %q, want the hook's reason preserved", res.Reason)
	}
}

// A hook asking does not get to overrule a deny rule — the user's standing
// answer outranks a script's request, and the user should not be prompted about
// a call their own rules already refused.
func TestHookGuardAskDoesNotOverrideRuleDeny(t *testing.T) {
	rules := &askGuard{
		fixedGuard: fixedGuard{allowed: false, reason: "denied by rule Bash(rm -*)"},
		askOK:      true,
	}
	g := HookGuard{
		Hooks: &stubHook{decision: HookDecision{Decision: "ask", Reason: "maybe fine?"}},
		Next:  rules,
	}
	allowed, res := g.Check(context.Background(), req())
	if allowed {
		t.Fatal("a hook ask overrode a deny rule")
	}
	if rules.asks != 0 {
		t.Errorf("user was prompted about a call the rules had already denied")
	}
	if !strings.Contains(res.Reason, "denied by rule") || !strings.Contains(res.Reason, "maybe fine?") {
		t.Errorf("Reason = %q, want both the rule's and the hook's reason", res.Reason)
	}
}

func TestHookGuardAskRefusedByUser(t *testing.T) {
	rules := &askGuard{
		fixedGuard: fixedGuard{allowed: true, reason: "allowed by rule"},
		askOK:      false,
		askWhy:     "no",
	}
	g := HookGuard{
		Hooks: &stubHook{decision: HookDecision{Decision: "ask", Reason: "confirm this"}},
		Next:  rules,
	}
	allowed, res := g.Check(context.Background(), req())
	if allowed {
		t.Fatal("a declined ask was granted")
	}
	if res.Decision != permission.Deny {
		t.Errorf("Decision = %v, want Deny", res.Decision)
	}
}

// With nobody to answer, `ask` cannot be granted. Granting it would be the one
// outcome ask must never produce.
func TestHookGuardAskWithNoAskerRefuses(t *testing.T) {
	rules := &fixedGuard{allowed: true, reason: "allowed by rule"}
	g := HookGuard{
		Hooks: &stubHook{decision: HookDecision{Decision: "ask", Reason: "confirm this"}},
		Next:  rules,
	}
	allowed, res := g.Check(context.Background(), req())
	if allowed {
		t.Fatal("ask was granted with no approver installed")
	}
	if !strings.Contains(res.Reason, "no approver") {
		t.Errorf("Reason = %q, want it to say no approver is installed", res.Reason)
	}
}
