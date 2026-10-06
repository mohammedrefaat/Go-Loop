package agent

import (
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/extension"
)

// filterFor builds the skills menu the system prompt would carry, gating each
// named skill behind the given `paths:` value ("" means ungated) and then
// filtering against the files the session touched.
func filterFor(t *testing.T, gates map[string]string, touched []string) string {
	t.Helper()
	skills := make([]extension.Skill, 0, len(gates))
	for name, paths := range gates {
		s := extension.Skill{Name: name, Description: name + " skill"}
		if paths != "" {
			s.Paths = []string{paths}
		}
		skills = append(skills, s)
	}
	return appendSkillsMenu(extension.FilterByPaths(skills, touched))
}

func menuHasSkill(menu, name string) bool {
	return strings.Contains(menu, "- /"+name+":")
}

// TestSkillsMenuOmitsGatedSkillUntilRelevant pins the user-visible effect of
// the gate: a `paths:`-scoped skill must not appear in the "# Available
// Skills" menu until the session has touched a matching file, and must appear
// once it has. Suppressing a relevant skill costs a round trip to recover, so
// the gate has to open on the first matching touch, not on the second.
func TestSkillsMenuOmitsGatedSkillUntilRelevant(t *testing.T) {
	gates := map[string]string{"tui": "internal/tui/**", "always": ""}

	t.Run("hidden before any file is touched", func(t *testing.T) {
		menu := filterFor(t, gates, nil)
		if menuHasSkill(menu, "tui") {
			t.Error("a gated skill must not be offered before the session touches a matching file")
		}
		if !menuHasSkill(menu, "always") {
			t.Error("an ungated skill must always be offered")
		}
	})

	t.Run("shown once a matching file is touched", func(t *testing.T) {
		menu := filterFor(t, gates, []string{"internal/tui/tui.go"})
		if !menuHasSkill(menu, "tui") {
			t.Error("a gated skill must be offered once the session touches a matching file")
		}
		if !menuHasSkill(menu, "always") {
			t.Error("gating one skill must not suppress an ungated one")
		}
	})

	t.Run("stays hidden when an unrelated file is touched", func(t *testing.T) {
		menu := filterFor(t, gates, []string{"README.md"})
		if menuHasSkill(menu, "tui") {
			t.Error("a gated skill must stay hidden for an unrelated file")
		}
	})

	t.Run("opens on a deep path under the pattern", func(t *testing.T) {
		menu := filterFor(t, gates, []string{"internal/tui/sub/deep/x.go"})
		if !menuHasSkill(menu, "tui") {
			t.Error("internal/tui/** must match at any depth below internal/tui")
		}
	})
}

func TestAppendSkillsMenuRendersOneLinePerSkill(t *testing.T) {
	menu := appendSkillsMenu([]extension.Skill{
		{Name: "alpha", Description: "first"},
		{Name: "beta", Description: "second"},
	})
	for _, want := range []string{"# Available Skills", "- /alpha: first", "- /beta: second"} {
		if !strings.Contains(menu, want) {
			t.Errorf("menu missing %q:\n%s", want, menu)
		}
	}
}

func TestAppendSkillsMenuOnEmptySlice(t *testing.T) {
	if got := appendSkillsMenu(nil); got != "" {
		t.Errorf("appendSkillsMenu(nil) = %q, want empty so no empty heading is emitted", got)
	}
}
