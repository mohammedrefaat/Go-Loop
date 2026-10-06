package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dimetron/pi-go/internal/keymap"
)

// withKeybindings points HOME at a temp dir holding a keybindings.json and
// returns a model wired to it.
func withKeybindings(t *testing.T, content string) *model {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// Stop the project-discovery walk at this temp root, as in the import
	// tests: on this machine it would otherwise reach the real ~/.mcp.json.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(home, ".pi-go")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keybindings.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newTestModel(t)
	m.cfg.WorkDir = root
	m.keymap = keymap.New()
	if _, err := m.keymap.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	return m
}

func TestKeymapKeyRendersTheSpec(t *testing.T) {
	// The whole feature depends on this string matching what normalizeKey
	// produces for the same chord written in a config file.
	cases := []struct {
		name string
		key  tea.Key
		want string
	}{
		{"ctrl letter", tea.Key{Code: 'o', Mod: tea.ModCtrl}, "ctrl+o"},
		{"plain letter", tea.Key{Code: 'k'}, "k"},
		{"shift reported in the code", tea.Key{Code: 'G'}, "g"},
		{"escape", tea.Key{Code: tea.KeyEscape}, "esc"},
		{"enter", tea.Key{Code: tea.KeyEnter}, "enter"},
		{"page up", tea.Key{Code: tea.KeyPgUp}, "pgup"},
		{"page down", tea.Key{Code: tea.KeyPgDown}, "pgdown"},
		{"up", tea.Key{Code: tea.KeyUp}, "up"},
		{"alt", tea.Key{Code: 'x', Mod: tea.ModAlt}, "alt+x"},
		{"ctrl+alt", tea.Key{Code: 'x', Mod: tea.ModCtrl | tea.ModAlt}, "ctrl+alt+x"},
	}
	for _, tc := range cases {
		if got := keymapKey(tc.key); got != tc.want {
			t.Errorf("%s: keymapKey(%+v) = %q, want %q", tc.name, tc.key, got, tc.want)
		}
	}
}

func TestKeymapKeyRoundTripsThroughNormalize(t *testing.T) {
	// The two sides have to agree, or a binding written in any spelling never
	// matches what the terminal sends.
	keys := []tea.Key{
		{Code: 'o', Mod: tea.ModCtrl},
		{Code: 'b', Mod: tea.ModCtrl},
		{Code: 'r', Mod: tea.ModCtrl},
		{Code: tea.KeyEscape},
		{Code: tea.KeyUp},
		{Code: tea.KeyDown},
		{Code: tea.KeyPgUp},
		{Code: tea.KeyPgDown},
	}
	// PgUp and PgDown are transcript-scoped; the rest live in the chat
	// context, so each key is checked against the context that owns it.
	contextFor := func(spec string) string {
		if strings.HasPrefix(spec, "pg") {
			return keymap.ContextTranscript
		}
		return keymap.ContextChat
	}
	for _, k := range keys {
		spec := keymapKey(k)
		normalized := keymap.NormalizeKey(spec)
		// Looking up the normalized form of what the terminal sent must find
		// whatever default is bound there.
		if _, bound := keymap.New().Action(contextFor(normalized), normalized); !bound {
			t.Errorf("keymapKey(%+v) = %q normalizes to %q, which is not bound by default",
				k, spec, normalized)
		}
	}
}

func TestRebindRedirectsAnActionToANewKey(t *testing.T) {
	m := withKeybindings(t, `[
	  {"context": "chat", "key": "ctrl+g", "action": "toggle.tool-output"}
	]`)
	m.chatModel.ToolDisplay.CompactTools = false

	// The new key works...
	if _, _, handled := m.handleToggleKey(tea.Key{Code: 'g', Mod: tea.ModCtrl}); !handled {
		t.Fatal("the rebound key should be handled")
	}
	if !m.chatModel.ToolDisplay.CompactTools {
		t.Error("ctrl+g should have toggled tool output")
	}

	// ...and the old key no longer does, which is what rebinding means.
	before := m.chatModel.ToolDisplay.CompactTools
	if _, _, handled := m.handleToggleKey(tea.Key{Code: 'o', Mod: tea.ModCtrl}); handled {
		t.Error("ctrl+o should no longer be bound once rebound elsewhere")
	}
	if m.chatModel.ToolDisplay.CompactTools != before {
		t.Error("the old key still had an effect")
	}
}

func TestUnbindSilencesTheDefault(t *testing.T) {
	m := withKeybindings(t, `[
	  {"context": "chat", "key": "ctrl+o", "action": null}
	]`)
	before := m.chatModel.ToolDisplay.CompactTools

	if _, _, handled := m.handleToggleKey(tea.Key{Code: 'o', Mod: tea.ModCtrl}); !handled {
		t.Fatal("an unbound key should still be swallowed, not fall through to the input")
	}
	if m.chatModel.ToolDisplay.CompactTools != before {
		t.Error("an unbound key must not run the default")
	}
}

func TestRebindingScrollKeys(t *testing.T) {
	m := withKeybindings(t, `[
	  {"context": "transcript", "key": "ctrl+v", "action": "scroll.page-up"}
	]`)

	if _, _, handled := m.handleScrollKey(tea.Key{Code: 'v', Mod: tea.ModCtrl}); !handled {
		t.Error("the rebound scroll key should be handled")
	}
	if _, _, handled := m.handleScrollKey(tea.Key{Code: tea.KeyPgUp}); handled {
		t.Error("pgup should no longer be bound once rebound")
	}
}

func TestRebindingHistoryArrows(t *testing.T) {
	m := withKeybindings(t, `[
	  {"context": "chat", "key": "ctrl+p", "action": "history.previous"}
	]`)

	if _, _, handled := m.handleHistoryKey(tea.Key{Code: 'p', Mod: tea.ModCtrl}); !handled {
		t.Error("the rebound history key should be handled")
	}
	if _, _, handled := m.handleHistoryKey(tea.Key{Code: tea.KeyUp}); handled {
		t.Error("up should no longer be bound once rebound")
	}
}

func TestUnknownActionLeavesTheDefaultWorking(t *testing.T) {
	m := withKeybindings(t, `[
	  {"context": "chat", "key": "ctrl+o", "action": "not.a.real.action"}
	]`)
	before := m.chatModel.ToolDisplay.CompactTools

	if _, _, handled := m.handleToggleKey(tea.Key{Code: 'o', Mod: tea.ModCtrl}); !handled {
		t.Fatal("ctrl+o should still work")
	}
	if m.chatModel.ToolDisplay.CompactTools == before {
		t.Error("the default should have been kept and run")
	}
}

func TestKeymapWarningsReachTheTranscript(t *testing.T) {
	// A binding that silently does nothing is the failure mode the spec warns
	// about; the user has to be told.
	m := withKeybindings(t, `[
	  {"context": "chat", "key": "ctrl+o", "action": "nope"}
	]`)

	m.handleToggleKey(tea.Key{Code: 'o', Mod: tea.ModCtrl})
	m.reportKeymapWarnings()

	found := false
	for _, msg := range m.chatModel.Messages {
		if strings.Contains(msg.content, "nope") {
			found = true
		}
	}
	if !found {
		t.Errorf("no message named the unknown action; transcript: %+v", m.chatModel.Messages)
	}
}

func TestKeybindingsCommandNamesTheFile(t *testing.T) {
	m := withKeybindings(t, `[]`)

	m.handleKeybindingsCommand(nil)
	out := lastMessage(t, m)
	if !strings.Contains(out, "keybindings.json") {
		t.Errorf("/keybindings must say which file to edit:\n%s", out)
	}
	if !strings.Contains(out, "toggle.tool-output") {
		t.Errorf("/keybindings should list the default bindings:\n%s", out)
	}
}

func TestKeybindingsCommandListsTheRequestedContext(t *testing.T) {
	m := withKeybindings(t, `[]`)

	m.handleKeybindingsCommand([]string{"transcript"})
	out := lastMessage(t, m)
	if !strings.Contains(out, "transcript") || !strings.Contains(out, "pgup") {
		t.Errorf("/keybindings transcript should list pgup:\n%s", out)
	}
}

func TestKeybindingsCommandRejectsAnUnknownContext(t *testing.T) {
	m := withKeybindings(t, `[]`)

	m.handleKeybindingsCommand([]string{"sidebar"})
	if !strings.Contains(lastMessage(t, m), "Unknown context") {
		t.Errorf("should reject an unknown context:\n%s", lastMessage(t, m))
	}
}

func TestNilKeymapLeavesDefaultsToTheHandlers(t *testing.T) {
	// A test model and any startup that could not build a keymap must behave
	// exactly as before rather than losing every binding.
	m := newTestModel(t)
	m.keymap = nil
	before := m.chatModel.ToolDisplay.CompactTools

	if _, _, handled := m.handleToggleKey(tea.Key{Code: 'o', Mod: tea.ModCtrl}); !handled {
		t.Fatal("with no keymap the hardcoded key must still work")
	}
	if m.chatModel.ToolDisplay.CompactTools == before {
		t.Error("with no keymap ctrl+o should still toggle")
	}
}
