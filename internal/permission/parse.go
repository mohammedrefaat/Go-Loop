package permission

import (
	"fmt"
	"strings"
)

// ParseRule parses one rule in config syntax.
//
// The decision prefix is optional and defaults to allow, so `Bash(ls *)` and
// `allow Bash(ls *)` mean the same thing. A `:` between the decision and the
// tool is accepted and ignored (`allow:Bash` == `allow Bash`) because that
// spelling appears in hand-written configs.
//
// A tool-name glob is only meaningful for deny and ask: an allow rule whose
// tool is a glob would be meaningless too, since a glob allow cannot be more
// specific than the default allow, and rejecting it catches a config typo
// rather than a real need.
func ParseRule(s string) (Rule, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return Rule{}, fmt.Errorf("empty permission rule")
	}

	var decision Decision
	rest := raw
	switch {
	case hasDecisionPrefix(raw, "deny"):
		decision, rest = Deny, strings.TrimSpace(raw[len("deny"):])
	case hasDecisionPrefix(raw, "ask"):
		decision, rest = Ask, strings.TrimSpace(raw[len("ask"):])
	case hasDecisionPrefix(raw, "allow"):
		decision, rest = Allow, strings.TrimSpace(raw[len("allow"):])
	default:
		decision = Allow
	}
	rest = strings.TrimSpace(strings.TrimPrefix(rest, ":"))
	if rest == "" {
		return Rule{}, fmt.Errorf("permission rule %q has no tool", raw)
	}

	tool, spec, pathPattern, err := splitToolSpec(rest)
	if err != nil {
		return Rule{}, fmt.Errorf("permission rule %q: %w", raw, err)
	}
	// A tool name with a space in it cannot be a real tool. Without this check
	// a config typo — a dropped decision prefix, most often — parses as an
	// allow rule for a tool named after the whole sentence, which then never
	// matches and is never reported. The user believes the rule is doing
	// something. No pi-go tool name contains whitespace, so this cannot reject
	// a rule that was meant to work.
	if strings.ContainsAny(tool, " \t") {
		return Rule{}, fmt.Errorf("permission rule %q: %q is not a tool name; expected allow/ask/deny followed by one", raw, tool)
	}
	if isToolGlob(tool) && decision == Allow {
		return Rule{}, fmt.Errorf("permission rule %q: a tool glob cannot be an allow rule", raw)
	}
	return Rule{
		Decision:    decision,
		Tool:        tool,
		Specifier:   spec,
		PathPattern: pathPattern,
		Source:      raw,
	}, nil
}

// hasDecisionPrefix reports whether raw starts with word, on a word boundary,
// so a tool literally named "asktheuser" is not read as the decision "ask"
// followed by the tool "theuser".
func hasDecisionPrefix(raw, word string) bool {
	if !strings.HasPrefix(raw, word) {
		return false
	}
	rest := raw[len(word):]
	if rest == "" {
		return true
	}
	c := rest[0]
	// A decision is followed by a space, a colon, or the end of the rule.
	return c == ' ' || c == ':' || c == '\t'
}

// splitToolSpec separates `Tool(specifier)` into its parts. The specifier is
// the raw text inside the outermost parentheses, unparsed, because what to
// match against depends on the tool: a bash command for Bash, a path for
// Read, a domain for WebFetch.
//
// Splitting on the *first* '(' and the *last* ')' is deliberate. Bash
// commands contain parentheses constantly — `bash(ls (a|b))` — and matching
// the outermost pair is the only way that parses sensibly.
func splitToolSpec(s string) (tool string, spec *string, pathPattern string, err error) {
	open := strings.IndexByte(s, '(')
	if open < 0 {
		if strings.ContainsAny(s, ")") {
			return "", nil, "", fmt.Errorf("unbalanced parentheses")
		}
		return s, nil, "", nil
	}
	if !strings.HasSuffix(s, ")") {
		return "", nil, "", fmt.Errorf("missing closing parenthesis")
	}
	tool = strings.TrimSpace(s[:open])
	if tool == "" {
		return "", nil, "", fmt.Errorf("empty tool name")
	}
	inner := s[open+1 : len(s)-1]
	// An empty specifier, Tool(), is a bare-name rule: there is nothing to
	// scope on, so treating it as one avoids a rule that silently matches
	// nothing.
	if strings.TrimSpace(inner) == "" {
		return tool, nil, "", nil
	}
	spec = &inner
	return tool, spec, inner, nil
}

// isToolGlob reports whether a tool name is a glob rather than a literal.
func isToolGlob(tool string) bool {
	return strings.ContainsAny(tool, "*?")
}
