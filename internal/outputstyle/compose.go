package outputstyle

import (
	"fmt"
	"strings"
)

// Selection names the output style to use, carrying both the explicit request
// and whatever a role implied.
//
// The distinction is the whole point of this type. A role is a statement about
// *which model* to run; it is entitled to imply a voice as a convenience, but
// that implication is weaker than a user typing --output-style. Keeping both
// on one struct makes the precedence rule total rather than a comparison
// scattered across call sites.
type Selection struct {
	// Explicit is the style named by --output-style. It wins over Role.
	Explicit string
	// Role is the style a role implies, used only when Explicit is empty.
	Role string
}

// Name returns the style to apply, or "" for none.
func (s Selection) Name() string {
	if s.Explicit != "" {
		return s.Explicit
	}
	return s.Role
}

// Source describes where the chosen style came from, for a notice the user can
// act on. "explicit", "role", or "" when no style applies.
func (s Selection) Source() string {
	if s.Explicit != "" {
		return "explicit"
	}
	if s.Role != "" {
		return "role"
	}
	return ""
}

// RoleStyles are the voices the built-in roles imply. They are the same shapes
// the --smol/--slow/--plan flags select, so a user who already uses those gets
// a coherent register without configuring anything.
var RoleStyles = map[string]string{
	"plan": "Plan",
	"slow": "Deep",
	"smol": "Terse",
}

// RoleStyleFor returns the output style a role implies, or "" if the role
// implies none. An unmapped role is not an error: roles are user-extensible, and
// a custom role with no voice of its own is a perfectly ordinary thing.
func RoleStyleFor(role string) string {
	return RoleStyles[strings.TrimSpace(role)]
}

// Prompt renders the style's instruction as a section to prepend to the
// built-in system prompt.
//
// The style text leads, because it is the user's deliberate instruction for
// this session and a later section reads to the model as more local. The
// built-in prompt then still applies, which is what keeps a "be terse" style
// from also switching off the safety and honesty rules that the built-in
// carries. A style that wants to replace the prompt outright should say so with
// --system, which is an existing, unambiguous mechanism.
func (s Style) Prompt() string {
	if strings.TrimSpace(s.Instruction) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n# Output Style\n\n")
	if s.Name != "" {
		fmt.Fprintf(&b, "Respond in the following style (%s):\n\n", s.Name)
	}
	b.WriteString(s.Instruction)
	b.WriteString("\n")
	return b.String()
}
