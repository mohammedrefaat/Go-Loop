package tools

import (
	"context"

	"github.com/dimetron/pi-go/internal/permission"
)

// The hook half of the permission seam.
//
// A PreToolUse hook and a permission rule are the same question asked by two
// different people: may this call run? The rule set is the user's standing
// answer; a hook is a script's answer to one specific call. They are therefore
// evaluated in one place, in a fixed order, rather than each in its own wrapper
// around the tool.
//
// The order is hooks first, then rules, and that is not interchangeable. A hook
// runs per call and can inspect the arguments; a rule matches by pattern and is
// cheap. Running hooks second would mean the rules had already decided, and a
// deny rule would short-circuit before the hook ever ran — so a hook written to
// enforce a policy the rules do not express would work everywhere except the
// one place it was written for. Running hooks first also means a hook can deny
// a call the rules would have allowed, which is the whole reason a user writes
// one.
//
// The reverse is also true and is why this cannot be reordered the other way: a
// deny *rule* still beats a hook's allow, because rule evaluation inside
// permission.Engine is deny → ask → allow and the hook's allow is merely an
// absence of objection. See HookGuard.Check.

// HookDecision is the outcome of a PreToolUse hook, in the terms the seam needs.
//
// The decision is one of three: an empty string means defer, and the caller
// carries on to the permission rules exactly as if no hook existed.
type HookDecision struct {
	// Decision is "allow", "deny", "ask", or "" to defer.
	Decision string
	// Reason is the hook's explanation, shown to the model on a deny.
	Reason string
}

// Hook is the subset of a hook system that the seam needs.
//
// It is an interface rather than the extension package's concrete runner
// because internal/extension already imports this package (read_image.go
// reaches for the sandbox), so importing it back would be a cycle. Declaring
// the narrow interface here keeps the dependency pointing one way and lets a
// test supply a decision without running a shell.
type Hook interface {
	// RunDecision evaluates every hook registered for an event and folds the
	// answers into one outcome. An empty Decision means no hook had an opinion.
	RunDecision(ctx context.Context, event string, payload map[string]any, toolName string) HookDecision
}

// HookGuard adapts hooks to the permission Guard, so a hook decision and a rule
// decision reach the tool through one call.
//
// Hooks is a runner rather than a slice of configs because the seam runs per
// tool call, on the agent's goroutine, and re-selecting and re-folding a hook
// list on every call would put a process spawn in front of every tool
// invocation whether or not any hook is registered for it.
type HookGuard struct {
	// Hooks evaluates PreToolUse hooks. A nil Hooks makes the guard a pass-through,
	// which is what keeps a surface with no hooks configured behaving exactly as
	// it did before hooks could block.
	Hooks Hook
	// Next is the permission engine, consulted after the hooks defer.
	//
	// It is an interface rather than a concrete engine so a test can supply a
	// fixed verdict, and so the ordering — hooks, then rules — is expressed as
	// two collaborators rather than one object doing both.
	Next Guard
}

// Check implements Guard.
//
// Three outcomes, in order of authority:
//
//   - A hook says allow or deny: terminal. The rules are not consulted, because
//     the hook has already made the call and re-asking the rules could only
//     reverse a decision the user deliberately scripted.
//   - A hook says ask: evaluation continues, but the rules are told the hook
//     wants confirmation. A hook that cannot see the rules' verdict would be a
//     hook that could not tell whether it needed to be louder.
//   - A hook defers, or there are none: the rules decide, unchanged.
func (g HookGuard) Check(ctx context.Context, req permission.Request) (bool, permission.Result) {
	if g.Hooks != nil {
		decision := g.hooksDecision(ctx, req)
		switch decision.Decision {
		case "deny":
			return false, permission.Result{
				Decision: permission.Deny,
				Reason:   "denied by a PreToolUse hook" + reasonSuffix(decision.Reason),
			}
		case "allow":
			return true, permission.Result{
				Decision: permission.Allow,
				Reason:   "allowed by a PreToolUse hook" + reasonSuffix(decision.Reason),
			}
		case "ask":
			// The rules run inside askForRules, which also forces the question
			// through the approver when they alone would have allowed. It is not
			// called here first: running them twice would evaluate every rule
			// twice per call and could prompt about a call the rules already
			// refused.
			return g.askForRules(ctx, req, decision.Reason)
		}
	}
	allowed, res := g.checkRules(ctx, req)
	return allowed, res
}

// checkRules delegates to the wrapped guard, allowing anything when there is
// none — a seam with neither hooks nor rules is an unguarded tool, which is how
// pi-go behaved before either existed.
func (g HookGuard) checkRules(ctx context.Context, req permission.Request) (bool, permission.Result) {
	if g.Next == nil {
		return true, permission.Result{Decision: permission.Allow, Reason: "no permission rules configured"}
	}
	return g.Next.Check(ctx, req)
}

// askForRules runs the rules and forces the result through the prompt even when
// the rules alone would have allowed.
//
// This is what makes `ask` mean something. If it merely meant "carry on", a hook
// could not request confirmation and the decision would be indistinguishable
// from defer — a hook author would write one, see the call run anyway, and have
// no way to tell they had.
func (g HookGuard) askForRules(ctx context.Context, req permission.Request, hookReason string) (bool, permission.Result) {
	allowed, res := g.checkRules(ctx, req)
	if !allowed {
		return false, withHookReason(res, hookReason)
	}
	// The rules would have allowed it. The hook asked anyway, so the decision
	// goes to the human — through the same approver the rules use, so there is
	// still exactly one place a question can be answered.
	ask, ok := g.asker()
	if !ok {
		// No way to ask. Refusing is the only safe reading: with nobody to
		// answer, "ask" cannot be granted.
		return false, withHookReason(permission.Result{
			Decision: permission.Deny,
			Reason:   "a hook asked for confirmation but no approver is installed",
		}, hookReason)
	}
	approved, reason := ask(ctx, req)
	res = permission.Result{Decision: permission.Allow, Reason: reason}
	if !approved {
		res.Decision = permission.Deny
	}
	return approved, withHookReason(res, hookReason)
}

// asker reaches the engine's Ask, which resolves a hook-driven confirmation
// exactly the way a rule-driven one is resolved — including on a headless
// surface, where it applies the configured headless policy rather than
// pretending nobody was there to answer.
//
// The seam has to be able to ask even though Guard does not expose an Ask, and
// widening Guard would give every guard implementation the obligation to supply
// one. A guard that cannot answer falls through to refusing, which is the
// behaviour already required of it.
type askerGuard interface {
	Ask(ctx context.Context, req permission.Request) (bool, string)
}

func (g HookGuard) asker() (func(context.Context, permission.Request) (bool, string), bool) {
	if a, ok := g.Next.(askerGuard); ok {
		return a.Ask, true
	}
	return nil, false
}

// hooksDecision asks the hook runner about a call.
func (g HookGuard) hooksDecision(ctx context.Context, req permission.Request) HookDecision {
	return g.Hooks.RunDecision(ctx, "PreToolUse", map[string]any{
		"tool": req.Tool,
		"arg":  req.Arg,
	}, req.Tool)
}

// reasonSuffix renders a hook's reason as a sentence fragment, so a Result's
// Reason reads as one sentence rather than two run together.
func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return ": " + reason
}

// withHookReason appends the hook's reason to a rule result, preserving the
// rule's own explanation — the user needs to know both which rule matched and
// that a hook asked, and collapsing them to one loses whichever came second.
func withHookReason(res permission.Result, hookReason string) permission.Result {
	if hookReason == "" {
		return res
	}
	if res.Reason == "" {
		res.Reason = "a hook asked for confirmation: " + hookReason
		return res
	}
	res.Reason = res.Reason + "; a hook also asked for confirmation: " + hookReason
	return res
}
