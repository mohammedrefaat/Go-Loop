package keymap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withBindings points HOME at a temp dir holding a keybindings.json with the
// given content, and returns a Keymap already reloaded from it.
func withBindings(t *testing.T, content string) *Keymap {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir reads this on Windows

	if content != "" {
		dir := filepath.Join(home, ".pi-go")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "keybindings.json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	k := New()
	if _, err := k.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	return k
}

func TestDefaultsArePresentWithNoFile(t *testing.T) {
	k := withBindings(t, "")

	action, bound := k.Action(ContextChat, "ctrl+o")
	if !bound || action != ActionToggleTools {
		t.Errorf("ctrl+o = %q bound=%v, want %q bound", action, bound, ActionToggleTools)
	}
	if k.Path() != "" {
		t.Errorf("Path() = %q, want \"\" when there is no file", k.Path())
	}
}

func TestUserBindingShadowsTheDefault(t *testing.T) {
	k := withBindings(t, `[
	  {"context": "chat", "key": "ctrl+o", "action": "toggle.branch"}
	]`)

	action, bound := k.Action(ContextChat, "ctrl+o")
	if !bound || action != ActionToggleBranch {
		t.Errorf("ctrl+o = %q bound=%v, want the user's action", action, bound)
	}
}

func TestUnknownActionIsSkippedAndTheDefaultKept(t *testing.T) {
	// The spec's central promise: a typo must not cost the user the key.
	k := withBindings(t, `[
	  {"context": "chat", "key": "ctrl+o", "action": "does.not.exist"}
	]`)

	action, bound := k.Action(ContextChat, "ctrl+o")
	if !bound || action != ActionToggleTools {
		t.Errorf("ctrl+o = %q bound=%v, want the default kept", action, bound)
	}
	warnings := k.Warnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "unknown action") {
		t.Errorf("Warnings() = %v, want one naming the unknown action", warnings)
	}
}

func TestUnknownActionDoesNotFailTheLoad(t *testing.T) {
	k := withBindings(t, `[
	  {"context": "chat", "key": "ctrl+o", "action": "nope"},
	  {"context": "chat", "key": "ctrl+r", "action": "history.search"}
	]`)

	// The good entry beside the bad one must still land.
	if action, _ := k.Action(ContextChat, "ctrl+r"); action != ActionHistorySearch {
		t.Errorf("ctrl+r = %q, want history.search despite the bad entry above it", action)
	}
}

func TestNullActionUnbinds(t *testing.T) {
	k := withBindings(t, `[
	  {"context": "chat", "key": "ctrl+o", "action": null}
	]`)

	action, bound := k.Action(ContextChat, "ctrl+o")
	if !bound {
		t.Fatal("an explicit unbind must still report the key as bound")
	}
	if action != ActionNone {
		t.Errorf("ctrl+o = %q, want an explicit unbind", action)
	}
}

func TestReadlineKeysAreNotRemappable(t *testing.T) {
	k := withBindings(t, `[
	  {"context": "chat", "key": "ctrl+a", "action": "clear"}
	]`)

	// Not bound by any action, and certainly not rebound to clear.
	if action, bound := k.Action(ContextChat, "ctrl+a"); bound {
		t.Errorf("ctrl+a = %q bound=%v, want it left to the line editor", action, bound)
	}
	warnings := k.Warnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "text-editing") {
		t.Errorf("Warnings() = %v, want one saying the key cannot be rebound", warnings)
	}
}

func TestReadlineKeysCoverTheDocumentedSet(t *testing.T) {
	for _, key := range []string{"Ctrl+A", "ctrl+e", "CTRL+K", "ctrl+U", "ctrl+w"} {
		if !IsReadlineLocked(key) {
			t.Errorf("IsReadlineLocked(%q) = false, want true", key)
		}
	}
	if IsReadlineLocked("ctrl+o") {
		t.Error("ctrl+o must be remappable")
	}
}

func TestChordIsAcceptedAndStored(t *testing.T) {
	k := withBindings(t, `[
	  {"context": "chat", "key": "Ctrl+X Ctrl+K", "action": "clear"}
	]`)

	if len(k.Warnings()) != 0 {
		t.Errorf("a well-formed chord should not warn: %v", k.Warnings())
	}
	// Matched on its first step for now, because the TUI has no chord state
	// machine; the point is that it is accepted and shadowed, not rejected.
	action, bound := k.Action(ContextChat, "ctrl+x")
	if !bound || action != ActionClear {
		t.Errorf("ctrl+x = %q bound=%v, want clear from the chord", action, bound)
	}
}

func TestFlatClaudeFormatIsAccepted(t *testing.T) {
	// What a user actually copies out of ~/.claude is the flat form.
	k := withBindings(t, `{"ctrl+t": "toggle.tool-output"}`)

	if action, bound := k.Action(ContextChat, "ctrl+t"); !bound || action != ActionToggleTools {
		t.Errorf("ctrl+t = %q bound=%v, want toggle.tool-output", action, bound)
	}
}

func TestModifierOrderDoesNotMatter(t *testing.T) {
	k := withBindings(t, `[
	  {"context": "chat", "key": "shift+ctrl+g", "action": "clear"}
	]`)

	if action, bound := k.Action(ContextChat, "ctrl+shift+g"); !bound || action != ActionClear {
		t.Errorf("ctrl+shift+g = %q bound=%v, want the binding written shift+ctrl+g", action, bound)
	}
}

func TestUnknownContextFallsBackToChatWithAWarning(t *testing.T) {
	k := withBindings(t, `[
	  {"context": "sidebar", "key": "ctrl+g", "action": "clear"}
	]`)

	action, bound := k.Action(ContextChat, "ctrl+g")
	if !bound || action != ActionClear {
		t.Errorf("ctrl+g = %q bound=%v, want the binding applied in chat", action, bound)
	}
	warnings := k.Warnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "unknown context") {
		t.Errorf("Warnings() = %v, want one naming the unknown context", warnings)
	}
}

func TestMalformedFileKeepsTheLastGoodBindings(t *testing.T) {
	k := withBindings(t, `[
	  {"context": "chat", "key": "ctrl+g", "action": "clear"}
	]`)
	if action, _ := k.Action(ContextChat, "ctrl+g"); action != ActionClear {
		t.Fatalf("setup: ctrl+g = %q, want clear", action)
	}

	// Reload is stamped on mtime+size, which a same-length edit can preserve
	// at 1s resolution; write something a different length to be sure the
	// change is seen.
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Reload(); err == nil {
		t.Error("a malformed file should report an error")
	}
	if action, _ := k.Action(ContextChat, "ctrl+g"); action != ActionClear {
		t.Errorf("ctrl+g = %q, want the last good bindings kept", action)
	}
}

func TestDeletingTheFileRestoresDefaults(t *testing.T) {
	k := withBindings(t, `[
	  {"context": "chat", "key": "ctrl+g", "action": null}
	]`)
	if action, bound := k.Action(ContextChat, "ctrl+g"); !bound || action != ActionNone {
		t.Fatalf("setup: ctrl+g = %q bound=%v, want an explicit unbind", action, bound)
	}

	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	changed, err := k.Reload()
	if err != nil {
		t.Fatalf("Reload after delete: %v", err)
	}
	if !changed {
		t.Error("deleting the file should count as a change")
	}
	// Deleting overrides is not unbinding: the default comes back.
	if action, _ := k.Action(ContextChat, "ctrl+o"); action != ActionToggleTools {
		t.Errorf("ctrl+o = %q, want defaults restored", action)
	}
	if _, bound := k.Action(ContextChat, "ctrl+g"); bound {
		t.Error("an unbind should not survive the file being deleted")
	}
}

func TestUnchangedFileIsNotReread(t *testing.T) {
	// Reload runs on the hot path, so a file that has not changed must be
	// cheap; the bool says whether anything was replaced.
	k := withBindings(t, `[
	  {"context": "chat", "key": "ctrl+g", "action": "clear"}
	]`)
	changed, err := k.Reload()
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("a second Reload with no change should report no change")
	}
}

func TestEmptyKeyIsWarnedAbout(t *testing.T) {
	k := withBindings(t, `[{"context": "chat", "key": "  ", "action": "clear"}]`)

	warnings := k.Warnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "no key") {
		t.Errorf("Warnings() = %v, want one about the missing key", warnings)
	}
}

func TestWarningsAreReportedOnce(t *testing.T) {
	// The TUI surfaces these through a channel; reporting the same one on
	// every keystroke would flood the transcript.
	k := withBindings(t, `[{"context": "chat", "key": "ctrl+o", "action": "nope"}]`)

	if len(k.Warnings()) != 1 {
		t.Fatalf("first Warnings() = %v, want one", k.Warnings())
	}
	if got := k.Warnings(); len(got) != 0 {
		t.Errorf("second Warnings() = %v, want them cleared", got)
	}
}

func TestBoundIsSortedForDisplay(t *testing.T) {
	k := New()
	got := k.Bound(ContextTranscript)
	for i := 1; i < len(got); i++ {
		if got[i-1].Key > got[i].Key {
			t.Errorf("Bound() is not sorted: %q before %q", got[i-1].Key, got[i].Key)
		}
	}
}

func TestNormalizeKey(t *testing.T) {
	cases := map[string]string{
		"Ctrl+O":      "ctrl+o",
		"ctrl+O":      "ctrl+o",
		"  ctrl + b ": "ctrl+b",
		"C-k":         "ctrl+k",
		"ESC":         "escape",
		// Modifiers land in a fixed order, which is the point: the same chord
		// written either way has to normalize to one string.
		"Shift+Ctrl+G": "ctrl+shift+g",
		"alt+x":        "alt+x",
	}
	for in, want := range cases {
		if got := NormalizeKey(in); got != want {
			t.Errorf("NormalizeKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBindingAnActionMovesItOffTheDefaultKey(t *testing.T) {
	// Naming an action under a new key is a claim about where the action lives,
	// not an alias. A key means one thing, so ctrl+o has to stop meaning
	// toggle.tool-output when ctrl+g takes it over — otherwise a user rebinding
	// a key has two of them and no way to know which one they are really
	// pressing.
	k := withBindings(t, `[
	  {"context": "chat", "key": "ctrl+g", "action": "toggle.tool-output"}
	]`)

	if action, bound := k.Action(ContextChat, "ctrl+g"); !bound || action != ActionToggleTools {
		t.Errorf("ctrl+g = %q bound=%v, want %q bound", action, bound, ActionToggleTools)
	}
	if action, bound := k.Action(ContextChat, "ctrl+o"); bound {
		t.Errorf("ctrl+o is still bound to %q after the action moved to ctrl+g", action)
	}
}

func TestTwoKeysForOneActionLeaveTheLastOneHoldingIt(t *testing.T) {
	// Within one file the order is the user's, so the later entry is the one
	// they meant when they wrote both.
	k := withBindings(t, `[
	  {"context": "chat", "key": "ctrl+g", "action": "toggle.tool-output"},
	  {"context": "chat", "key": "ctrl+y", "action": "toggle.tool-output"}
	]`)

	if _, bound := k.Action(ContextChat, "ctrl+g"); bound {
		t.Error("ctrl+g should have given way to the later ctrl+y")
	}
	if action, bound := k.Action(ContextChat, "ctrl+y"); !bound || action != ActionToggleTools {
		t.Errorf("ctrl+y = %q bound=%v, want %q bound", action, bound, ActionToggleTools)
	}
	if _, bound := k.Action(ContextChat, "ctrl+o"); bound {
		t.Error("the default should still have given way")
	}
}

func TestUnbindDoesNotEvictAnActionBoundElsewhere(t *testing.T) {
	// A null frees a key and claims nothing. It must not go on to take
	// toggle.tool-output away from the key the user just moved it to.
	k := withBindings(t, `[
	  {"context": "chat", "key": "ctrl+o", "action": null},
	  {"context": "chat", "key": "ctrl+g", "action": "toggle.tool-output"}
	]`)

	if action, bound := k.Action(ContextChat, "ctrl+g"); !bound || action != ActionToggleTools {
		t.Errorf("ctrl+g = %q bound=%v, want %q bound", action, bound, ActionToggleTools)
	}
}

func TestAClaimDoesNotReachIntoAnotherContext(t *testing.T) {
	// Both contexts bind submit to something, and one context claiming an
	// action must not strip the other context's binding: "escape interrupts"
	// in an overlay and "escape interrupts" at the prompt are two independent
	// statements.
	k := withBindings(t, `[
	  {"context": "chat", "key": "ctrl+q", "action": "submit"}
	]`)

	if _, bound := k.Action(ContextOverlay, "enter"); !bound {
		t.Error("the overlay's own enter binding should survive")
	}
	if _, bound := k.Action(ContextOverlay, "escape"); !bound {
		t.Error("the overlay's own escape binding should survive")
	}
}

func TestRebindingDoesNotAffectTheContextThatWasNotNamed(t *testing.T) {
	k := withBindings(t, `[
	  {"context": "transcript", "key": "ctrl+v", "action": "scroll.page-up"}
	]`)

	if action, bound := k.Action(ContextTranscript, "ctrl+v"); !bound || action != ActionScrollUp {
		t.Errorf("ctrl+v = %q bound=%v, want %q bound", action, bound, ActionScrollUp)
	}
	if _, bound := k.Action(ContextTranscript, "pgup"); bound {
		t.Error("pgup should have given way to ctrl+v")
	}
	// ctrl+o belongs to the chat context and is not the action being claimed, so
	// it must be untouched.
	if action, bound := k.Action(ContextChat, "ctrl+o"); !bound || action != ActionToggleTools {
		t.Errorf("ctrl+o = %q bound=%v, want %q still bound", action, bound, ActionToggleTools)
	}
}
