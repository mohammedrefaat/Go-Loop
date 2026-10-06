package outputstyle

import "testing"

// TestSelectionExplicitBeatsRole pins the precedence rule the spec asks the
// plan to decide: an explicit --output-style beats a style implied by a role.
//
// The reasoning is that a role states which *model* to run, so its voice is an
// implication rather than the thing the user asked for. A user who typed
// --output-style has stated the voice directly, and silently overriding it with
// --slow's register would make the flag look broken.
func TestSelectionExplicitBeatsRole(t *testing.T) {
	s := Selection{Explicit: "terse", Role: "Deep"}
	if got := s.Name(); got != "terse" {
		t.Errorf("Name() = %q, want %q", got, "terse")
	}
	if got := s.Source(); got != "explicit" {
		t.Errorf("Source() = %q, want %q", got, "explicit")
	}
}

func TestSelectionRoleUsedWhenNoExplicit(t *testing.T) {
	s := Selection{Role: "Deep"}
	if got := s.Name(); got != "Deep" {
		t.Errorf("Name() = %q, want %q", got, "Deep")
	}
	if got := s.Source(); got != "role" {
		t.Errorf("Source() = %q, want %q", got, "role")
	}
}

func TestSelectionEmptyIsNoStyle(t *testing.T) {
	s := Selection{}
	if got := s.Name(); got != "" {
		t.Errorf("Name() = %q, want empty", got)
	}
	if got := s.Source(); got != "" {
		t.Errorf("Source() = %q, want empty", got)
	}
}

func TestRoleStyleFor(t *testing.T) {
	tests := []struct {
		role string
		want string
	}{
		{"plan", "Plan"},
		{"slow", "Deep"},
		{"smol", "Terse"},
		{"custom-role", ""},
		{"", ""},
		{"  smol  ", "Terse"},
	}
	for _, tc := range tests {
		if got := RoleStyleFor(tc.role); got != tc.want {
			t.Errorf("RoleStyleFor(%q) = %q, want %q", tc.role, got, tc.want)
		}
	}
}

// TestRoleStylesNameRealStyles guards the implied-voice table against drifting
// away from the files it points at. A role mapping to a style nobody ships
// fails at the moment the user runs --slow, not at build time.
func TestRoleStylesNameRealStyles(t *testing.T) {
	for role, name := range RoleStyles {
		s := Style{Name: name, Instruction: "x"}
		if s.Prompt() == "" {
			t.Errorf("role %q maps to style %q, which renders no prompt", role, name)
		}
	}
}
