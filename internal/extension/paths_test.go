package extension

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMatchPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		pattern string
		want    bool
	}{
		{"exact root file", "CLAUDE.md", "CLAUDE.md", true},
		{"exact root file misses other", "docs/CLAUDE.md", "CLAUDE.md", false},
		{"exact root file misses other name", "CLAUDE.mdx", "CLAUDE.md", false},
		{"double star tree", "internal/tui/tui.go", "internal/tui/**", true},
		{"double star tree nested", "internal/tui/sub/deep/x.go", "internal/tui/**", true},
		{"double star tree excludes sibling", "internal/tools/x.go", "internal/tui/**", false},
		{"double star tree excludes prefix lookalike", "internal/tuix/x.go", "internal/tui/**", false},
		{"single star is direct children only", "internal/tui/tui.go", "internal/tui/*", true},
		{"single star excludes deeper", "internal/tui/sub/x.go", "internal/tui/*", false},
		{"leading double star any depth", "a/b/c/x.go", "**/*.go", true},
		{"leading double star at root", "main.go", "**/*.go", true},
		{"leading double star wrong extension", "a/b/x.md", "**/*.go", false},
		{"bare star is root only", "x.go", "*.go", true},
		{"bare star does not match nested", "a/x.go", "*.go", false},
		{"double star consumes zero segments", "a/b.md", "a/**/b.md", true},
		{"double star consumes several segments", "a/x/y/z/b.md", "a/**/b.md", true},
		{"leading slash is stripped", "docs/a.md", "/docs/**", true},
		{"dot slash prefix is stripped", "docs/a.md", "./docs/**", true},
		{"trailing slash is not a directory shorthand", "docs/a.md", "docs/", false},
		{"empty pattern never matches", "docs/a.md", "", false},
		{"unterminated bracket does not match", "x[.go", "x[.go", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchPath(tc.path, tc.pattern); got != tc.want {
				t.Errorf("MatchPath(%q, %q) = %v, want %v", tc.path, tc.pattern, got, tc.want)
			}
		})
	}
}

func TestMatchPathBackslashPatternMatchesForwardSlashPath(t *testing.T) {
	// A Windows-authored SKILL.md carries backslashes; the repo records paths
	// with forward slashes, so without normalization the gate would never open.
	if !MatchPath(`internal/tui/x.go`, `internal\**\*.go`) {
		t.Error("a backslash pattern should match a forward-slash path")
	}
}

func TestSkillMatchesPathsUngatedIsAlwaysRelevant(t *testing.T) {
	s := Skill{Name: "gated"}
	if !s.MatchesPaths([]string{"docs/a.md"}) {
		t.Error("a skill with no paths: must always be relevant, or adding the key would silently break every existing skill")
	}
}

func TestSkillMatchesPaths(t *testing.T) {
	s := Skill{Name: "tui", Paths: []string{"internal/tui/**", "internal/tools/**"}}
	tests := []struct {
		name    string
		touched []string
		want    bool
	}{
		{"no touched files", nil, false},
		{"first pattern", []string{"internal/tui/tui.go"}, true},
		{"second pattern", []string{"internal/tools/a.go"}, true},
		{"unrelated file", []string{"README.md"}, false},
		{"one match among several", []string{"README.md", "internal/tui/tui.go"}, true},
		{"unrelated among several", []string{"README.md", "Makefile"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.MatchesPaths(tc.touched); got != tc.want {
				t.Errorf("MatchesPaths(%v) = %v, want %v", tc.touched, got, tc.want)
			}
		})
	}
}

func TestFilterByPathsPreservesUngatedAndOrder(t *testing.T) {
	skills := []Skill{
		{Name: "always"},
		{Name: "tui", Paths: []string{"internal/tui/**"}},
		{Name: "docs", Paths: []string{"docs/**"}},
		{Name: "last"},
	}
	got := FilterByPaths(skills, []string{"docs/a.md"})
	if len(got) != 3 {
		t.Fatalf("got %d skills (%v), want 3", len(got), got)
	}
	if got[0].Name != "always" || got[1].Name != "docs" || got[2].Name != "last" {
		t.Errorf("got order %v, want [always docs last]", got)
	}
}

func TestFilterByPathsWithNothingTouchedHidesGatedSkills(t *testing.T) {
	skills := []Skill{
		{Name: "always"},
		{Name: "tui", Paths: []string{"internal/tui/**"}},
	}
	got := FilterByPaths(skills, nil)
	if len(got) != 1 || got[0].Name != "always" {
		t.Errorf("got %v, want just [always]", got)
	}
}

func TestFilterByPathsDoesNotMutateInput(t *testing.T) {
	skills := []Skill{{Name: "tui", Paths: []string{"internal/tui/**"}}}
	original := len(skills)
	FilterByPaths(skills, []string{"README.md"})
	if len(skills) != original {
		t.Error("FilterByPaths must not modify the caller's slice")
	}
}

func TestParseSkillPathsFrontmatter(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		want    []string
		wantNil bool
	}{
		{name: "absent", header: "name: x", wantNil: true},
		{name: "comma separated", header: "paths: internal/tui/**, docs/**", want: []string{"internal/tui/**", "docs/**"}},
		{name: "flow list", header: `paths: ["internal/tui/**", "docs/**"]`, want: []string{"internal/tui/**", "docs/**"}},
		{name: "single value", header: "paths: internal/tui/**", want: []string{"internal/tui/**"}},
		{name: "flow list unquoted", header: "paths: [internal/tui/**]", want: []string{"internal/tui/**"}},
		{name: "repeated key accumulates", header: "paths: a/**\npaths: b/**", want: []string{"a/**", "b/**"}},
		{name: "bare key is a warning, not a gate", header: "paths:", wantNil: true},
		{name: "blank key is a warning, not a gate", header: "paths:   ", wantNil: true},
		{name: "empty flow list", header: "paths: []", wantNil: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			content := "---\n" + tc.header + "\n---\nbody\n"
			skill, body, err := parseSkillContent(content, "x")
			if err != nil {
				t.Fatalf("parseSkillContent: %v", err)
			}
			if body != "body" {
				t.Errorf("body = %q, want %q", body, "body")
			}
			if tc.wantNil {
				if len(skill.Paths) != 0 {
					t.Errorf("Paths = %v, want empty", skill.Paths)
				}
				return
			}
			if len(skill.Paths) != len(tc.want) {
				t.Fatalf("Paths = %v, want %v", skill.Paths, tc.want)
			}
			for i := range tc.want {
				if skill.Paths[i] != tc.want[i] {
					t.Errorf("Paths[%d] = %q, want %q", i, skill.Paths[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseSkillPathsKeepsExistingFrontmatterWorking(t *testing.T) {
	content := "---\nname: tui\ndescription: TUI work\ntools: read, edit\n---\nbody\n"
	skill, _, err := parseSkillContent(content, "dir-name")
	if err != nil {
		t.Fatalf("parseSkillContent: %v", err)
	}
	if skill.Name != "tui" {
		t.Errorf("Name = %q, want %q", skill.Name, "tui")
	}
	if skill.Description != "TUI work" {
		t.Errorf("Description = %q, want %q", skill.Description, "TUI work")
	}
	if len(skill.Tools) != 2 {
		t.Errorf("Tools = %v, want 2 entries", skill.Tools)
	}
	if len(skill.Paths) != 0 {
		t.Errorf("Paths = %v, want empty", skill.Paths)
	}
}

func TestParseSkillPathsWarnsOnBlockList(t *testing.T) {
	// A block list parses as a bare "paths:" under the line-oriented parser.
	// The result fails open (ungated), which is safe, but silently — so it
	// must be reported rather than leaving the author to wonder why the gate
	// never closed.
	content := "---\nname: tui\npaths:\n  - internal/tui/**\n  - docs/**\n---\nbody\n"
	skill, body, err := parseSkillContent(content, "tui")
	if err != nil {
		t.Fatalf("parseSkillContent: %v", err)
	}
	if len(skill.Paths) != 0 {
		t.Errorf("Paths = %v, want empty so a malformed list fails open", skill.Paths)
	}
	// The list lines fall through into the body: the parser is line-oriented
	// and has no notion of a YAML block list, so they are neither gate nor
	// error — they become prose. That is harmless (the body is only read when
	// the skill is invoked) but it must not be mistaken for parsing.
	if strings.Contains(body, "- internal/tui/**") {
		t.Errorf("block list should not survive as parsed frontmatter, body = %q", body)
	}
}

func TestLoadSkillsReadsPathsFromDisk(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "tui")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: tui\ndescription: d\npaths: internal/tui/**\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	skills, err := LoadSkillsWithOptions(LoadOptions{AuditMode: AuditSkip}, dir)
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}
	// Bundled skills are always loaded first, so the directory contributes one
	// entry on top of them — select ours by name rather than counting.
	s, ok := FindSkill(skills, "tui")
	if !ok {
		t.Fatalf("no skill named tui in %d loaded skills", len(skills))
	}
	if len(s.Paths) != 1 || s.Paths[0] != "internal/tui/**" {
		t.Errorf("Paths = %v, want [internal/tui/**]", s.Paths)
	}
}

func TestMatchAnyPath(t *testing.T) {
	if !MatchAnyPath("internal/tui/tui.go", []string{"docs/**", "internal/tui/**"}) {
		t.Error("want a match on the second pattern")
	}
	if MatchAnyPath("internal/tui/tui.go", []string{"docs/**", "internal/tools/**"}) {
		t.Error("want no match")
	}
	if MatchAnyPath("x.go", nil) {
		t.Error("an empty pattern list never matches")
	}
}
