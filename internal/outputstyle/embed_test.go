package outputstyle

import "testing"

// TestBuiltinStylesParse guards the embedded files against a typo that would
// otherwise ship broken: nothing in the build checks that a .md under builtin/
// is well-formed, and the failure would only appear when a user ran --smol.
func TestBuiltinStylesParse(t *testing.T) {
	styles := ListBuiltin()
	if len(styles) == 0 {
		t.Fatal("no built-in styles were embedded")
	}
	for _, s := range styles {
		if s.Name == "" {
			t.Errorf("%s: empty Name", s.Path)
		}
		if s.Description == "" {
			t.Errorf("%s: empty Description, so the picker has nothing to show", s.Path)
		}
		if s.Instruction == "" {
			t.Errorf("%s: empty Instruction, so selecting the style would be a no-op", s.Path)
		}
		if s.Prompt() == "" {
			t.Errorf("%s: Prompt() is empty", s.Path)
		}
	}
}

// TestEveryRoleImpliedStyleShips pins the RoleStyles table to files that
// actually exist in the binary. A role that implies a style nobody ships fails
// only when the user runs that role.
func TestEveryRoleImpliedStyleShips(t *testing.T) {
	for role, name := range RoleStyles {
		s, err := LoadBuiltin(name)
		if err != nil {
			t.Errorf("role %q implies style %q, which does not ship: %v", role, name, err)
			continue
		}
		if s.Instruction == "" {
			t.Errorf("role %q implies style %q, which is empty", role, name)
		}
	}
}

func TestLoadBuiltinAcceptsNameWithOrWithoutExtension(t *testing.T) {
	for _, name := range []string{"terse", "terse.md"} {
		if _, err := LoadBuiltin(name); err != nil {
			t.Errorf("LoadBuiltin(%q): %v", name, err)
		}
	}
}

// TestLoadBuiltinIsCaseInsensitive covers the role table's spelling: it names
// styles by their display name ("Terse"), while the files are lowercase
// (terse.md). embed.FS is case-sensitive even where the host filesystem is
// not, so without the fallback a style reachable via --smol would fail to load
// on every platform.
func TestLoadBuiltinIsCaseInsensitive(t *testing.T) {
	canonical, err := LoadBuiltin("terse")
	if err != nil {
		t.Fatalf("LoadBuiltin(terse): %v", err)
	}
	for _, name := range []string{"Terse", "TERSE", "Terse.md", "TERSE.MD"} {
		got, err := LoadBuiltin(name)
		if err != nil {
			t.Errorf("LoadBuiltin(%q): %v", name, err)
			continue
		}
		if got.Instruction != canonical.Instruction {
			t.Errorf("LoadBuiltin(%q) resolved to a different style than %q", name, "terse")
		}
	}
}

func TestLoadBuiltinRejectsUnknownAndUnsafe(t *testing.T) {
	if _, err := LoadBuiltin("nope"); err == nil {
		t.Error("LoadBuiltin of an unknown style should error")
	}
	for _, name := range []string{"", "  ", "../outputstyle", "..\\builtin"} {
		if _, err := LoadBuiltin(name); err == nil {
			t.Errorf("LoadBuiltin(%q) succeeded, want an error", name)
		}
	}
}

func TestUserStyleShadowsNothingByAccident(t *testing.T) {
	// A user style and a built-in of the same name must both load: the
	// difference is which one the caller picks, not a silent override.
	user := t.TempDir()
	writeStyle(t, user, "terse.md", "---\nname: Terse\n---\nuser version")
	if _, err := Load(user, "terse"); err != nil {
		t.Errorf("Load of a user style shadowing a built-in: %v", err)
	}
	if _, err := LoadBuiltin("terse"); err != nil {
		t.Errorf("LoadBuiltin after a user style of the same name: %v", err)
	}
}
