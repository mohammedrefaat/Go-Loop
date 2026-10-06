package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/dimetron/pi-go/internal/keymap"
)

// keymapKey turns a tea.Key into the spec string the keymap is keyed by.
//
// The rendering has to be exact rather than approximate: it is the join between
// what the terminal sends and what a user wrote in their bindings file, and a
// key that renders differently here than it normalizes there is a binding
// that silently does nothing. So this uses the same names the keymap's
// specialNames table normalizes onto ("esc" → "escape", "pgup" → "pgup"),
// and returns the bare lowercase rune for printable keys.
func keymapKey(key tea.Key) string {
	var sb strings.Builder
	// Modifier order matches keymap.normalizeKey's canonical order.
	if key.Mod&tea.ModCtrl != 0 {
		sb.WriteString("ctrl+")
	}
	if key.Mod&tea.ModAlt != 0 {
		sb.WriteString("alt+")
	}
	if key.Mod&tea.ModShift != 0 {
		sb.WriteString("shift+")
	}
	sb.WriteString(keymapBase(key))
	return sb.String()
}

// keymapBase is the unmodified part of a key: a name for the special keys, the
// rune itself for everything else.
func keymapBase(key tea.Key) string {
	switch key.Code {
	case 0:
		return ""
	// KeyReturn and KeyEsc are aliases of KeyEnter and KeyEscape in this
	// version of Bubble Tea, so listing them would be a duplicate case.
	case tea.KeyEnter:
		return "enter"
	case tea.KeyEscape:
		return "esc"
	case tea.KeyBackspace:
		return "backspace"
	case tea.KeyDelete:
		return "delete"
	case tea.KeyTab:
		return "tab"
	case tea.KeySpace:
		return "space"
	case tea.KeyUp:
		return "up"
	case tea.KeyDown:
		return "down"
	case tea.KeyLeft:
		return "left"
	case tea.KeyRight:
		return "right"
	case tea.KeyHome:
		return "home"
	case tea.KeyEnd:
		return "end"
	case tea.KeyPgUp:
		return "pgup"
	case tea.KeyPgDown:
		return "pgdown"
	case tea.KeyInsert:
		return "insert"
	}
	if r := key.Code; r >= tea.KeyF1 && r <= tea.KeyF12 {
		return "f" + strconv.Itoa(int(r-tea.KeyF1)+1)
	}
	// A printable rune. Uppercase means shift reached it, which some terminals
	// report in the code rather than the modifier, so it is lowercased here and
	// the shift is left to normalizeKey's spelling handling.
	if key.Code < ' ' {
		// A control character the table above did not name.
		return "ctrl+" + string(rune('a'+key.Code-1))
	}
	return strings.ToLower(string(key.Code))
}

// keymapAction resolves a key in a context, reloading the file first so a
// bindings change takes effect without a restart.
//
// Reload is stamped on mtime+size and the TUI already reloads skills on
// completion, so this is the same cost model as behaviour the app has today.
// A nil keymap — a test model, or a startup that could not build one — falls
// through to the caller's own handling.
func (m *model) keymapAction(context string, key tea.Key) (keymap.Action, bool) {
	if m.keymap == nil {
		return keymap.ActionNone, false
	}
	if _, err := m.keymap.Reload(); err != nil {
		// Reported through Warnings below rather than returned: a broken file
		// must not change what every key does.
		_ = err
	}
	return m.keymap.Action(context, keymapKey(key))
}

// reportKeymapWarnings surfaces binding problems in the transcript, once each.
//
// This is the only way a user learns that "ctrl+t" does nothing because they
// misspelled the action, so it goes through the chat rather than the session
// log where they would never see it.
func (m *model) reportKeymapWarnings() {
	if m.keymap == nil {
		return
	}
	warnings := m.keymap.Warnings()
	if len(warnings) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("Keybinding problems (the default is kept for each):\n")
	for _, w := range warnings {
		fmt.Fprintf(&b, "  ! %s\n", w)
	}
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: strings.TrimRight(b.String(), "\n"),
	})
}

// keymapHelp renders the bindings for a context, for /keybindings.
func keymapHelp(k *keymap.Keymap, context string) string {
	if k == nil {
		return "Keybindings are not loaded."
	}
	bindings := k.Bound(context)
	if len(bindings) == 0 {
		return fmt.Sprintf("No bindings in the %s context.", context)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Keybindings (%s)", context)
	if path := k.Path(); path != "" {
		fmt.Fprintf(&b, " — %s", path)
	}
	b.WriteString(":\n")
	for _, bind := range bindings {
		if bind.Action == keymap.ActionNone {
			fmt.Fprintf(&b, "  %-20s (unbound)\n", bind.Key)
			continue
		}
		fmt.Fprintf(&b, "  %-20s %s\n", bind.Key, bind.Action)
	}
	if context != keymap.ContextOverlay {
		fmt.Fprintf(&b, "\nText-editing keys (%s) cannot be rebound.",
			strings.Join(readlineKeyList(), ", "))
	}
	return b.String()
}

func readlineKeyList() []string {
	return []string{"Ctrl+A", "Ctrl+E", "Ctrl+K", "Ctrl+U", "Ctrl+W"}
}

// handleKeybindingsCommand lists the bindings, and says where to change them.
//
// Listing without a path would make the command half-useful: the user sees
// that ctrl+o does something and has no way to learn which file to edit.
func (m *model) handleKeybindingsCommand(args []string) (tea.Model, tea.Cmd) {
	m.inputModel.Clear()
	if m.keymap == nil {
		m.keymap = keymap.New()
	}
	if _, err := m.keymap.Reload(); err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Could not read %s: %v\n\nThe default bindings are in use.", keymap.FilePath(), err),
		})
		return m, nil
	}

	context := keymap.ContextChat
	if len(args) > 0 {
		switch strings.ToLower(args[0]) {
		case "chat", "transcript", "overlay", "settings":
			context = strings.ToLower(args[0])
		default:
			m.chatModel.Messages = append(m.chatModel.Messages, message{
				role:    "assistant",
				content: fmt.Sprintf("Unknown context %q. Use one of: chat, transcript, overlay, settings.", args[0]),
			})
			return m, nil
		}
	}

	content := keymapHelp(m.keymap, context)
	if path, err := keymap.Path(); err == nil {
		content += fmt.Sprintf("\n\nEdit %s to rebind. An unknown action is skipped and the default kept;\n"+
			"\"action\": null unbinds a key.", path)
	} else {
		content += fmt.Sprintf("\n\nNo home directory, so there is no %s to edit.", keymap.FilePath())
	}
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: content,
	})
	return m, nil
}
