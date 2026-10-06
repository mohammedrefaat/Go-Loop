package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// fakeClaudeHome points the process home at a temp dir containing a
// ~/.claude/settings.json with the given permission rules, and returns the
// project directory to run the import from.
func fakeClaudeHome(t *testing.T, rules string) (home, project string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir reads this on Windows

	claude := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := `{"permissions": {"deny": [` + rules + `]}}`
	if err := os.WriteFile(filepath.Join(claude, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}

	// pi-go's own config is written under the same home, so the import's
	// Save() cannot reach the real one.
	piDir := filepath.Join(home, ".pi-go")
	if err := os.MkdirAll(piDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(piDir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Project discovery walks up to the filesystem root, and on this machine
	// that reaches the real ~/.mcp.json — a temp dir is not a hermetic
	// project. Parking an empty .mcp.json at the temp root stops the walk
	// there, so a test cannot pick up whatever the developer's home contains.
	// Written as `{}` rather than deleted, because removing a file that may not
	// be ours is not a test's job.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		root = t.TempDir()
	}
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, filepath.Join(root, "project")
}

func TestImportClaudePreviewsWithoutWriting(t *testing.T) {
	_, project := fakeClaudeHome(t, `"Bash(rm *)"`)
	cfgBefore, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".pi-go", "config.json"))
	if err != nil {
		t.Fatal(err)
	}

	m := newTestModel(t)
	m.cfg.WorkDir = project
	m.handleSlashCommand("/import claude")

	if m.pendingClaudeImport == nil {
		t.Fatalf("no pending import; last message: %s", lastMessage(t, m))
	}
	out := lastMessage(t, m)
	if !strings.Contains(out, "deny Bash(rm *)") {
		t.Errorf("preview should name the rule it would add:\n%s", out)
	}
	if !strings.Contains(out, "Enter") || !strings.Contains(out, "Esc") {
		t.Errorf("preview must ask before writing:\n%s", out)
	}

	cfgAfter, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".pi-go", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(cfgBefore) != string(cfgAfter) {
		t.Error("the preview wrote to config.json before confirmation")
	}
}

func TestImportClaudeConfirmWritesRules(t *testing.T) {
	home, project := fakeClaudeHome(t, `"Bash(rm *)", "Read(.env)"`)
	m := newTestModel(t)
	m.cfg.WorkDir = project
	m.handleSlashCommand("/import claude")

	updated, _ := m.handleImportClaudeConfirm()
	m = updated.(*model)

	if m.pendingClaudeImport != nil {
		t.Error("the pending plan should be cleared once applied")
	}
	data, err := os.ReadFile(filepath.Join(home, ".pi-go", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Bash(rm *)", "Read(.env)"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("config.json is missing %q:\n%s", want, data)
		}
	}
	// Restarting is the honest report: the engine is built at startup.
	if !strings.Contains(lastMessage(t, m), "Restart") {
		t.Errorf("summary should say a restart is needed:\n%s", lastMessage(t, m))
	}
}

func TestImportClaudeCancelWritesNothing(t *testing.T) {
	home, project := fakeClaudeHome(t, `"Bash(rm *)"`)
	m := newTestModel(t)
	m.cfg.WorkDir = project
	m.handleSlashCommand("/import claude")

	updated, _ := m.handleImportClaudeCancel()
	m = updated.(*model)

	if m.pendingClaudeImport != nil {
		t.Error("cancelling must clear the pending plan")
	}
	data, err := os.ReadFile(filepath.Join(home, ".pi-go", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Bash") {
		t.Errorf("cancelling wrote rules anyway:\n%s", data)
	}
	if !strings.Contains(lastMessage(t, m), "Nothing was written") {
		t.Errorf("cancel should say nothing was written:\n%s", lastMessage(t, m))
	}
}

func TestImportClaudeKeySwallowsStrayPresses(t *testing.T) {
	// A confirmation that leaks a keypress to the prompt would let any stray
	// character apply a plan the user never read.
	_, project := fakeClaudeHome(t, `"Bash(rm *)"`)
	m := newTestModel(t)
	m.cfg.WorkDir = project
	m.handleSlashCommand("/import claude")

	before := len(m.chatModel.Messages)
	updated, cmd, handled := m.handleImportClaudeKey(tea.Key{Code: 'x', Text: "x"})
	if !handled {
		t.Error("a stray key must be handled, not fall through to the prompt")
	}
	if cmd != nil {
		t.Error("a stray key must not produce a command")
	}
	m = updated.(*model)
	if m.pendingClaudeImport == nil {
		t.Error("a stray key must not apply the plan")
	}
	if len(m.chatModel.Messages) != before {
		t.Error("a stray key must not write to the transcript")
	}
}

func TestImportClaudeEnterConfirms(t *testing.T) {
	_, project := fakeClaudeHome(t, `"Bash(rm *)"`)
	m := newTestModel(t)
	m.cfg.WorkDir = project
	m.handleSlashCommand("/import claude")

	updated, _, handled := m.handleImportClaudeKey(tea.Key{Code: tea.KeyEnter})
	if !handled {
		t.Fatal("Enter must be handled while a plan is pending")
	}
	if updated.(*model).pendingClaudeImport != nil {
		t.Error("Enter should have applied the plan")
	}
}

func TestImportClaudeKeyIgnoredWithNothingPending(t *testing.T) {
	m := newTestModel(t)
	if _, _, handled := m.handleImportClaudeKey(tea.Key{Code: tea.KeyEnter}); handled {
		t.Error("with no pending import the key must fall through to the prompt")
	}
}

func TestImportClaudeWithNothingNewToImport(t *testing.T) {
	// pi-go already has the rule Claude has, so the plan is empty. Note this
	// cannot be tested by pointing at a home with no ~/.claude: project
	// discovery walks up to the filesystem root, so a stray .mcp.json in a temp
	// directory's ancestor makes that case environment-dependent. A missing
	// installation is covered in the importclaude package instead.
	home, project := fakeClaudeHome(t, `"Bash(rm *)"`)
	piCfg := filepath.Join(home, ".pi-go", "config.json")
	if err := os.WriteFile(piCfg, []byte(`{"permissions":{"rules":["deny Bash(rm *)"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}

	m := newTestModel(t)
	m.cfg.WorkDir = project
	m.handleSlashCommand("/import claude")

	if m.pendingClaudeImport != nil {
		t.Error("nothing to import must not leave a pending plan")
	}
	if !strings.Contains(lastMessage(t, m), "Nothing to import") {
		t.Errorf("should say plainly that there is nothing to import:\n%s", lastMessage(t, m))
	}
}

func TestImportClaudeRequiresTheClaudeSubcommand(t *testing.T) {
	fakeClaudeHome(t, `"Bash(rm *)"`)
	m := newTestModel(t)
	m.cfg.WorkDir = t.TempDir()
	m.handleSlashCommand("/import")

	if m.pendingClaudeImport != nil {
		t.Error("`/import` alone must not import")
	}
	if !strings.Contains(lastMessage(t, m), "Usage") {
		t.Errorf("should show usage:\n%s", lastMessage(t, m))
	}
}
