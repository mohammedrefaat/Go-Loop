// Package permission implements the tool-permission layer: rule parsing,
// evaluation, and the modes that decide whether a call is allowed outright,
// must be confirmed, or is refused.
//
// # Evaluation order
//
// Rules are evaluated deny → ask → allow, and the first match in that order
// wins. Specificity deliberately does not matter: a broad deny beats a
// narrower allow, so an allow rule can never carve an exception out of a deny.
// This is the same rule Claude Code uses, and the reason is safety — a user
// who writes `deny Bash(rm *)` must never have it silently weakened by
// `allow Bash` sitting further down the file.
//
// # Rule syntax
//
//	Tool              a bare name matches every call to that tool
//	Tool(specifier)   a scoped match, e.g. Bash(npm run build), Read(./.env)
//	*                 a wildcard, including spaces
//	:*                equivalent to a trailing `*`
//	mcp__*            a tool-name glob (deny and ask rules only)
//
// File rules use gitignore-style path patterns and match on the path a tool
// was given, not on a canonicalized one, so a rule matches what the model
// actually wrote.
package permission

import (
	"strings"
)

// Decision is the outcome of evaluating a tool call against the rule set.
type Decision int

const (
	// Allow runs the call without asking. It is also the outcome when no
	// rule matches, which is why Allow is the zero-independent default for
	// callers that have no rules configured.
	Allow Decision = iota
	// Ask requires confirmation before the call runs.
	Ask
	// Deny refuses the call outright.
	Deny
)

// String returns the lowercase name used in config, logs, and test failures.
func (d Decision) String() string {
	switch d {
	case Allow:
		return "allow"
	case Ask:
		return "ask"
	case Deny:
		return "deny"
	default:
		return "unknown"
	}
}

// Rule is one parsed permission rule: a decision plus the pattern it matches
// and the tool it applies to.
//
// A nil Specifier means the rule matches every call to Tool — a bare-name
// rule. A non-nil Specifier is scoped: Bash(rm *) matches the rm command but
// not ls, and Read(./.env) matches that file but not every file.
type Rule struct {
	// Decision is what happens when this rule matches.
	Decision Decision
	// Tool is the tool name, or a glob such as "mcp__*". Always non-empty.
	Tool string
	// Specifier is the in-parentheses argument pattern, or nil for a
	// bare-name rule.
	Specifier *string
	// PathPattern is the gitignore-style pattern for file rules, empty when
	// the rule is not a file rule.
	PathPattern string
	// Source records where the rule came from, for /doctor-style reporting of
	// where an unexpected decision originated.
	Source string
}

// String renders the rule back to its config syntax, so an error message can
// show the user the rule they actually wrote rather than a Go struct dump.
func (r Rule) String() string {
	var b strings.Builder
	b.WriteString(r.Decision.String())
	b.WriteString(" ")
	b.WriteString(r.Tool)
	if r.Specifier != nil {
		b.WriteString("(")
		b.WriteString(*r.Specifier)
		b.WriteString(")")
	} else if r.PathPattern != "" {
		b.WriteString("(")
		b.WriteString(r.PathPattern)
		b.WriteString(")")
	}
	return b.String()
}
