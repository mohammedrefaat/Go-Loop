// Package keymap makes pi-go's key bindings rebindable from
// ~/.pi-go/keybindings.json.
//
// The bindings themselves stay where they are — the TUI's handlers still own
// the behaviour. What this package provides is the lookup that decides *which
// action a key means right now*, given the file, so a user can rebind without
// a recompile.
//
// Three rules shape the design, all of them about not breaking a working
// setup when the file is wrong:
//
//   - An unknown action is a warning, not an error. The binding is skipped and
//     the default is kept. A typo in a config file must not cost the user
//     Ctrl+O.
//   - Defaults are never destroyed, only shadowed. Removing a line from the
//     file restores the default rather than unbinding the key.
//   - `action: null` is the one explicit way to unbind, and it is a permanent
//     statement of intent: the key does nothing, and the default is not used.
package keymap

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Context names where a binding applies. pi-go's contexts, matching the spec.
const (
	// ContextChat is the prompt: history navigation, input toggles.
	ContextChat = "chat"
	// ContextTranscript is the conversation viewport: scrolling.
	ContextTranscript = "transcript"
	// ContextSettings is the settings surface.
	ContextSettings = "settings"
	// ContextOverlay is a modal overlay: pickers and confirmations.
	ContextOverlay = "overlay"
)

// Action is a named behaviour. It is a string rather than a function so that
// the file can name one, and so the TUI can switch on it without this package
// depending on the TUI.
type Action string

// Actions the TUI understands. An action outside this set is skipped with a
// warning — the list is deliberately not "any string", because accepting an
// unknown name would make a typo silently do nothing forever.
const (
	ActionNone            Action = ""
	ActionToggleTools     Action = "toggle.tool-output"
	ActionToggleBranch    Action = "toggle.branch"
	ActionHistorySearch   Action = "history.search"
	ActionHistoryPrevious Action = "history.previous"
	ActionHistoryNext     Action = "history.next"
	ActionScrollUp        Action = "scroll.page-up"
	ActionScrollDown      Action = "scroll.page-down"
	ActionScrollUpLine    Action = "scroll.line-up"
	ActionScrollDownLine  Action = "scroll.line-down"
	ActionInterrupt       Action = "interrupt"
	ActionSubmit          Action = "submit"
	ActionNewline         Action = "newline"
	ActionHelp            Action = "help"
	ActionClear           Action = "clear"
	ActionModel           Action = "model"
	ActionContext         Action = "context"
	ActionQuit            Action = "quit"
)

// knownActions is the set a binding may name.
var knownActions = map[Action]bool{
	ActionToggleTools:     true,
	ActionToggleBranch:    true,
	ActionHistorySearch:   true,
	ActionHistoryPrevious: true,
	ActionHistoryNext:     true,
	ActionScrollUp:        true,
	ActionScrollDown:      true,
	ActionScrollUpLine:    true,
	ActionScrollDownLine:  true,
	ActionInterrupt:       true,
	ActionSubmit:          true,
	ActionNewline:         true,
	ActionHelp:            true,
	ActionClear:           true,
	ActionModel:           true,
	ActionContext:         true,
	ActionQuit:            true,
}

// readlineLocked are the text-editing keys Claude Code keeps non-remappable,
// and so does pi-go.
//
// The reason is that these are not pi-go's to give away: they are the line
// editor's own primitives, and rebinding them would change what the input
// field *is* rather than what a key does in the app. A user who loses Ctrl+A
// mid-sentence has to exit the app to find it again.
var readlineLocked = map[string]bool{
	"ctrl+a": true,
	"ctrl+e": true,
	"ctrl+k": true,
	"ctrl+u": true,
	"ctrl+w": true,
}

// IsReadlineLocked reports whether key is one of the text-editing keys that
// cannot be rebound.
func IsReadlineLocked(key string) bool {
	return readlineLocked[normalizeKey(key)]
}

// Binding is one entry from the file: a key, in a context, meaning an action.
//
// Action is an Action rather than a pointer because `null` and a missing
// action mean the same thing here — both unbind — and json.Unmarshal into a
// string field maps `null` to "" for free. A *Action would only make the two
// cases distinguishable, and there is no behaviour that needs them apart.
type Binding struct {
	Context string `json:"context"`
	Key     string `json:"key"`
	Action  Action `json:"action"`
}

// Map is the resolved set of bindings for one context: key → action, where an
// empty action means explicitly unbound.
type Map map[string]Action

// Keymap resolves keys to actions, layering a user's file over the defaults.
type Keymap struct {
	mu       sync.RWMutex
	bindings map[string]map[string]Action // context → key → action
	// warnings are the problems found in the last load. They are kept rather
	// than logged from here so the TUI can surface them once, through the
	// channel it is allowed to write to.
	warnings []string
	// path is the file loaded, or "" when only defaults are in play.
	path string
	// stamp is the file's mtime and size at the last load, used to skip a
	// re-read that cannot have changed anything.
	stamp fileStamp
}

type fileStamp struct {
	mod  time.Time
	size int64
	ok   bool
}

// New returns a keymap with the built-in defaults and no user overrides.
func New() *Keymap {
	return &Keymap{bindings: defaultBindings()}
}

// Path is where the user's bindings live.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home dir: %w", err)
	}
	return filepath.Join(home, ".pi-go", "keybindings.json"), nil
}

// FilePath is Path for callers that only want to name the file in a message
// and have nothing to do with a home directory that cannot be read.
func FilePath() string {
	path, err := Path()
	if err != nil {
		return "~/.pi-go/keybindings.json"
	}
	return path
}

// Reload re-reads the file if it has changed since the last read, and reports
// whether the bindings were replaced.
//
// The mod-time check is what makes auto-reload cheap enough to call on every
// key press: a stat is nothing next to parsing, and a key press is not a hot
// path. A missing file is not an error — most users have none.
func (k *Keymap) Reload() (bool, error) {
	path, err := Path()
	if err != nil {
		return false, err
	}
	stamp, statErr := statFile(path)
	if os.IsNotExist(statErr) {
		k.mu.Lock()
		defer k.mu.Unlock()
		if k.path == "" {
			return false, nil // already defaults-only
		}
		// The file went away. Defaults are the right answer: the user deleted
		// their overrides, which is not the same as asking to unbind keys.
		k.bindings = defaultBindings()
		k.path = ""
		k.stamp = fileStamp{}
		k.warnings = nil
		return true, nil
	}
	if statErr != nil {
		return false, statErr
	}

	k.mu.RLock()
	unchanged := k.stamp.ok && k.stamp == stamp
	k.mu.RUnlock()
	if unchanged {
		return false, nil
	}

	bindings, claims, warnings, err := loadFile(path)
	if err != nil {
		// Keep the bindings we already have. A file that has become unreadable
		// is a moment where the last known-good set is more useful than none.
		k.mu.Lock()
		k.warnings = append(k.warnings, err.Error())
		k.mu.Unlock()
		return false, err
	}

	k.mu.Lock()
	resolved := merge(defaultBindings(), bindings)
	// Resolved against the merged map, not the user's entries alone, so naming
	// an action under a new key frees the default that used to hold it.
	resolveClaims(resolved, claims)
	k.bindings = resolved
	k.warnings = warnings
	k.path = path
	k.stamp = stamp
	k.mu.Unlock()
	return true, nil
}

// Action returns the action bound to key in context.
//
// The bool reports whether the key is bound at all — false means the caller
// should fall through to its own handling. A key bound to ActionNone returns
// true with an empty action: that is an explicit unbind, and the caller must
// not fall back to the default.
func (k *Keymap) Action(context, key string) (Action, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.lookupLocked(context, normalizeKey(key))
}

// lookupLocked is Action without taking the lock. Callers holding it already
// must not call Action.
func (k *Keymap) lookupLocked(context, key string) (Action, bool) {
	if m, ok := k.bindings[context]; ok {
		if action, bound := m[key]; bound {
			return action, true
		}
	}
	// Fall back to the chat context, so a user who binds a key without naming
	// a context gets it in the place they were working rather than nowhere.
	// Overlay is deliberately excluded: a binding meant for the prompt must
	// not start firing over a confirmation the user has to answer.
	if context != ContextChat && context != ContextOverlay {
		if m, ok := k.bindings[ContextChat]; ok {
			if action, bound := m[key]; bound {
				return action, true
			}
		}
	}
	return ActionNone, false
}

// Warnings returns the problems found in the last load, and clears them so
// each is reported once rather than on every keystroke.
func (k *Keymap) Warnings() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.warnings) == 0 {
		return nil
	}
	out := k.warnings
	k.warnings = nil
	return out
}

// Bound reports the bindings for a context, for display. Sorted, so the list
// is stable between calls.
func (k *Keymap) Bound(context string) []Binding {
	k.mu.RLock()
	defer k.mu.RUnlock()
	m := k.bindings[context]
	out := make([]Binding, 0, len(m))
	for key, action := range m {
		out = append(out, Binding{Context: context, Key: key, Action: action})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Path returns the file this keymap was loaded from, or "" if none.
func (k *Keymap) Path() string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.path
}

func statFile(path string) (fileStamp, error) {
	st, err := os.Stat(path)
	if err != nil {
		return fileStamp{}, err
	}
	return fileStamp{mod: st.ModTime(), size: st.Size(), ok: true}, nil
}

// specialNames maps the spellings a key can arrive under onto the canonical
// name used for lookup. Bubble Tea calls the escape key KeyEscape while a
// config file and a user both write "esc", so both have to land on one name.
var specialNames = map[string]string{
	"esc":       "escape",
	"escape":    "escape",
	"return":    "enter",
	"enter":     "enter",
	"cr":        "enter",
	"space":     "space",
	"tab":       "tab",
	"backspace": "backspace",
	"bs":        "backspace",
	"delete":    "delete",
	"del":       "delete",
	"up":        "up",
	"down":      "down",
	"left":      "left",
	"right":     "right",
	"home":      "home",
	"end":       "end",
	"pgup":      "pgup",
	"pageup":    "pgup",
	"pgdown":    "pgdown",
	"pagedown":  "pgdown",
	"insert":    "insert",
	"f1":        "f1",
	"f2":        "f2",
	"f3":        "f3",
	"f4":        "f4",
	"f5":        "f5",
	"f6":        "f6",
	"f7":        "f7",
	"f8":        "f8",
	"f9":        "f9",
	"f10":       "f10",
	"f11":       "f11",
	"f12":       "f12",
}

// emacsModifiers are the readline spellings — "C-k", "M-x", "S-tab" — which
// appear in enough people's muscle memory and dotfiles that accepting them is
// cheaper than explaining why not.
var emacsModifiers = map[string]string{
	"c": "ctrl",
	"m": "alt",
	"s": "shift",
	"a": "alt",
}

// normalizeKey puts a key spec into the canonical form used for lookup:
// modifiers in the fixed order ctrl+alt+shift, then the base key.
//
// The canonical form exists because the spellings are not canonical:
// "ctrl+shift+k", "shift+ctrl+k", "C-S-k" and "Ctrl+Shift+K" all name one
// chord, and a user will write more than one of them. Anything that cannot be
// reduced is returned as-is rather than dropped, so an unrecognised key
// produces a lookup miss and a fallback instead of silently matching something.
func normalizeKey(spec string) string {
	spec = strings.ToLower(strings.TrimSpace(spec))
	if spec == "" {
		return ""
	}

	// A chord is space-separated, and a single key may additionally be written
	// with "+" or an emacs prefix. A user will write "ctrl + b" with spaces
	// around the plus, so both separators are always split on — the tokens are
	// trimmed afterwards, which is what makes the padding harmless.
	fields := strings.Fields(spec)
	joined := strings.Join(fields, " ")
	if strings.Contains(joined, "+") {
		fields = strings.FieldsFunc(joined, func(r rune) bool {
			return r == '+' || r == ' ' || r == '\t'
		})
	} else if mod, rest, ok := splitEmacs(joined); ok {
		fields = append([]string{mod}, strings.Fields(rest)...)
	}

	var ctrl, alt, shift bool
	var base []string
	for _, tok := range fields {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		switch tok {
		case "ctrl", "control":
			ctrl = true
		case "alt", "meta", "option":
			alt = true
		case "shift":
			shift = true
		default:
			base = append(base, tok)
		}
	}
	if len(base) == 0 {
		// Modifiers alone are not a key. Returning "" makes it a lookup miss
		// rather than a binding that matches every bare modifier.
		return ""
	}

	key := base[0]
	if canonical, ok := specialNames[key]; ok && !shift && !ctrl && !alt {
		key = canonical
	}

	var parts []string
	if ctrl {
		parts = append(parts, "ctrl")
	}
	if alt {
		parts = append(parts, "alt")
	}
	if shift {
		parts = append(parts, "shift")
	}
	parts = append(parts, key)
	out := strings.Join(parts, "+")

	// A spec that carried a modifier must not be re-resolved through
	// specialNames: "shift+esc" is not "escape".
	if canonical, ok := specialNames[out]; ok && len(parts) == 1 {
		return canonical
	}
	return out
}

// splitEmacs recognises the "C-k", "M-x", "S-C-k" prefix spellings, returning
// the canonical modifier name and the remaining key.
func splitEmacs(tok string) (string, string, bool) {
	i := strings.Index(tok, "-")
	if i <= 0 || i == len(tok)-1 {
		return "", "", false
	}
	name, ok := emacsModifiers[tok[:i]]
	if !ok {
		return "", "", false
	}
	return name, tok[i+1:], true
}

// NormalizeKey is the exported form of normalizeKey, for callers formatting a
// key before comparing it.
func NormalizeKey(spec string) string { return normalizeKey(spec) }
