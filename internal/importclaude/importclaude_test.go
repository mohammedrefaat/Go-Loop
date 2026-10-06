package importclaude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeClaude builds a ~/.claude directory containing the given files, points
// the process's home at its parent, and returns the .claude path.
func fakeClaude(t *testing.T, files map[string]string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	useHome(t, home)
	return dir
}

func useHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir) // os.UserHomeDir reads this on Windows
}

const settingsWithAllDecisions = `{
  "permissions": {
    "deny": ["Bash(rm *)", "Read(.env)"],
    "ask": ["Bash(git push *)"],
    "allow": ["Read(**)", "Bash(npm run test)"]
  }
}`

func TestBuildImportsAllThreeDecisionLists(t *testing.T) {
	fakeClaude(t, map[string]string{"settings.json": settingsWithAllDecisions})

	s := Find("")
	plan, err := Build(s, nil, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.PermissionRules) != 5 {
		t.Fatalf("got %d rules %v, want 5", len(plan.PermissionRules), plan.PermissionRules)
	}
	// deny → ask → allow, matching how pi-go evaluates: position is not
	// load-bearing, but a config that reads in evaluation order is easier to
	// reason about when a broad deny is beating a narrow allow.
	want := []string{
		"deny Bash(rm *)", "deny Read(.env)",
		"ask Bash(git push *)",
		"allow Read(**)", "allow Bash(npm run test)",
	}
	for i, w := range want {
		if plan.PermissionRules[i] != w {
			t.Errorf("rule %d = %q, want %q", i, plan.PermissionRules[i], w)
		}
	}
}

func TestBuildIsIdempotentAgainstExistingRules(t *testing.T) {
	// Re-importing must not stack duplicates: the user will run this more than
	// once, and a config with the same deny rule four times is unreadable.
	fakeClaude(t, map[string]string{"settings.json": settingsWithAllDecisions})

	existing := []string{"deny Bash(rm *)", "allow Read(**)"}
	plan, err := Build(Find(""), existing, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, r := range plan.PermissionRules {
		for _, e := range existing {
			if r == e {
				t.Errorf("rule %q is already present but was proposed again", r)
			}
		}
	}
	if len(plan.PermissionRules) != 3 {
		t.Errorf("got %d new rules %v, want 3", len(plan.PermissionRules), plan.PermissionRules)
	}
	if len(plan.SkippedRules) != 2 {
		t.Errorf("got %d skipped %v, want the 2 already present", len(plan.SkippedRules), plan.SkippedRules)
	}
	for _, s := range plan.SkippedRules {
		if !strings.Contains(s.Reason, "already present") {
			t.Errorf("Skipped %q reason = %q, want it to say why", s.Rule, s.Reason)
		}
	}
}

func TestBuildSkipsUnparseableRuleWithAReason(t *testing.T) {
	// A rule that does not parse would look like a working rule in /doctor
	// while matching no tool. Dropping it silently is the one thing an import
	// must not do.
	_ = fakeClaude(t, map[string]string{
		"settings.json": `{"permissions": {"deny": ["Bash(rm *)", "!!!garbage!!!"]}}`,
	})

	plan, err := Build(Find(""), nil, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.PermissionRules) != 1 {
		t.Errorf("got %d rules %v, want only the parseable one", len(plan.PermissionRules), plan.PermissionRules)
	}
	if len(plan.SkippedRules) != 1 {
		t.Fatalf("got %d skipped, want 1", len(plan.SkippedRules))
	}
	if plan.SkippedRules[0].Reason == "" {
		t.Error("a skipped rule must carry the reason it was skipped")
	}
}

func TestBuildSkipsBlankRules(t *testing.T) {
	_ = fakeClaude(t, map[string]string{
		"settings.json": `{"permissions": {"allow": ["", "  ", "Read(**)"]}}`,
	})

	plan, _ := Build(Find(""), nil, nil)
	if len(plan.PermissionRules) != 1 {
		t.Errorf("got %v, want only the non-blank rule", plan.PermissionRules)
	}
}

func TestBuildWithNoClaudeInstallation(t *testing.T) {
	// A user who has never run Claude Code must get a clear answer, not an
	// error and not a panic.
	home := t.TempDir()
	useHome(t, home)

	s := Find("")
	if s.Found() {
		t.Errorf("Found() = true for an empty home (%+v)", s)
	}
	plan, err := Build(s, nil, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.PermissionRules) != 0 || len(plan.MCPServers) != 0 || plan.Memory != "" {
		t.Error("nothing should be imported from an empty installation")
	}
	if !strings.Contains(plan.Preview(), "Nothing to import") {
		t.Errorf("Preview() should say there is nothing to import:\n%s", plan.Preview())
	}
}

func TestBuildReportsMalformedSettingsRatherThanGuessing(t *testing.T) {
	_ = fakeClaude(t, map[string]string{"settings.json": "{not json"})

	plan, err := Build(Find(""), nil, nil)
	if err != nil {
		t.Fatalf("Build returned a hard error; a bad user file should be a note, not a failure: %v", err)
	}
	joined := strings.Join(plan.Notes, "\n")
	if !strings.Contains(joined, "settings.json") {
		t.Errorf("Notes = %v, want them to name the unparseable file", plan.Notes)
	}
}

func TestBuildImportsMCPServersFromProject(t *testing.T) {
	project := t.TempDir()
	mcp := `{"mcpServers": {
		"zeta": {"command": "npx", "args": ["-y", "zeta"]},
		"alpha": {"url": "https://example.com/mcp", "type": "http"}
	}}`
	if err := os.WriteFile(filepath.Join(project, ".mcp.json"), []byte(mcp), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	useHome(t, home)

	s := Find(project)
	plan, err := Build(s, nil, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.MCPServers) != 2 {
		t.Fatalf("got %d servers, want 2", len(plan.MCPServers))
	}
	// Sorted, so the preview is diffable between runs.
	if plan.MCPServers[0].Name != "alpha" || plan.MCPServers[1].Name != "zeta" {
		t.Errorf("got order %s,%s want alpha,zeta", plan.MCPServers[0].Name, plan.MCPServers[1].Name)
	}
	if plan.MCPServers[0].Spec.URL != "https://example.com/mcp" {
		t.Errorf("alpha URL = %q", plan.MCPServers[0].Spec.URL)
	}
}

func TestBuildSkipsMCPServerWithAnExistingName(t *testing.T) {
	project := t.TempDir()
	mcp := `{"mcpServers": {"alpha": {"command": "npx"}}}`
	if err := os.WriteFile(filepath.Join(project, ".mcp.json"), []byte(mcp), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	useHome(t, home)

	plan, _ := Build(Find(project), nil, []string{"ALPHA"}) // case-insensitive match
	if len(plan.MCPServers) != 0 {
		t.Errorf("got %v, want the existing server not re-proposed", plan.MCPServers)
	}
	if len(plan.SkippedRules) != 1 || !strings.Contains(plan.SkippedRules[0].Reason, "already has") {
		t.Errorf("Skipped = %v, want a note that the name is taken", plan.SkippedRules)
	}
}

func TestBuildFindsProjectMCPFromSubdirectory(t *testing.T) {
	// A monorepo's .mcp.json applies from any subdirectory, matching how
	// pi-go discovers project context.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{"a":{"command":"x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "sub", "deeper")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	useHome(t, home)

	s := Find(nested)
	if s.ProjectMCP == "" {
		t.Fatal("ProjectMCP not found from a subdirectory")
	}
	plan, _ := Build(s, nil, nil)
	if len(plan.MCPServers) != 1 {
		t.Errorf("got %d servers, want 1", len(plan.MCPServers))
	}
}

func TestBuildReportsUnmappedKeybindings(t *testing.T) {
	_ = fakeClaude(t, map[string]string{
		"keybindings.json": `{"ctrl+k ctrl+e": "undo", "ctrl+j": ["newline"]}`,
	})

	plan, _ := Build(Find(""), nil, nil)
	if len(plan.UnmappedKeybindings) != 2 {
		t.Fatalf("got %v, want both chords reported", plan.UnmappedKeybindings)
	}
	// Sorted, so the preview is stable between runs.
	if plan.UnmappedKeybindings[0] != "ctrl+j" {
		t.Errorf("got %v, want sorted order", plan.UnmappedKeybindings)
	}
	if !strings.Contains(plan.Preview(), "not carried over") {
		t.Error("Preview must say the keybindings were not carried over, not silently omit them")
	}
}

func TestBuildDoesNotWriteAnything(t *testing.T) {
	// The whole safety property of a preview: Build is pure.
	home := fakeClaude(t, map[string]string{"settings.json": settingsWithAllDecisions})

	before := dirSnapshot(t, filepath.Dir(home))
	plan, _ := Build(Find(""), nil, nil)
	if len(plan.PermissionRules) == 0 {
		t.Fatal("expected a non-empty plan for this fixture")
	}
	after := dirSnapshot(t, filepath.Dir(home))
	if len(before) != len(after) {
		t.Errorf("Build changed the filesystem: %d files before, %d after", len(before), len(after))
	}
}

func dirSnapshot(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
