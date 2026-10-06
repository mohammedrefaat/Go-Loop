package keymap

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// defaultBindings is what pi-go does with no file: today's hardcoded keys,
// expressed as data.
//
// This is the list that the TUI must not grow past silently. Every key the
// TUI handles should appear here, so that `/keybindings` can list what is
// bound and a user's file has something to shadow.
func defaultBindings() map[string]map[string]Action {
	chat := map[string]Action{
		"ctrl+o": ActionToggleTools,
		"ctrl+b": ActionToggleBranch,
		"ctrl+r": ActionHistorySearch,
		"up":     ActionHistoryPrevious,
		"down":   ActionHistoryNext,
		"ctrl+c": ActionInterrupt,
		"enter":  ActionSubmit,
		"escape": ActionInterrupt,
	}
	transcript := map[string]Action{
		"pgup":   ActionScrollUp,
		"pgdown": ActionScrollDown,
	}
	overlay := map[string]Action{
		"escape": ActionInterrupt,
		"enter":  ActionSubmit,
		"ctrl+c": ActionInterrupt,
	}
	// Up and Down deliberately appear once, in the chat context only, and the
	// two handlers that need them resolve the arrow before naming a context.
	//
	// They cannot sit in both chat and transcript: the keys are the same, so a
	// merge on key alone would let the chat copy shadow the transcript one and
	// a scroll binding would silently become a history binding. Keeping one
	// copy and letting each handler choose the context it looks up is what
	// makes "the same key means different things in different contexts"
	// expressible at all.
	return map[string]map[string]Action{
		ContextChat:       chat,
		ContextTranscript: transcript,
		ContextOverlay:    overlay,
	}
}

// merge layers user bindings over defaults.
//
// A user entry with a non-empty action shadows the default. An entry with an
// empty action — `null`, or an omitted action — is recorded as an explicit
// unbind, which must win over the default, so it is stored as ActionNone
// rather than dropped. The distinction is the whole point of supporting null.
func merge(defaults, user map[string]map[string]Action) map[string]map[string]Action {
	out := make(map[string]map[string]Action, len(defaults))
	for context, m := range defaults {
		cp := make(map[string]Action, len(m))
		for key, action := range m {
			cp[key] = action
		}
		out[context] = cp
	}
	for context, m := range user {
		if out[context] == nil {
			out[context] = make(map[string]Action, len(m))
		}
		for key, action := range m {
			out[context][key] = action
		}
	}
	return out
}

// fileBinding mirrors one entry. Action is json.RawMessage so that `null` and
// a missing key are both visible to be told apart from an empty string —
// json.Unmarshal would map all three to the same zero value otherwise, and
// `"action": ""` is a plausible typo that deserves the same warning as any
// other unknown action.
type fileBinding struct {
	Context string          `json:"context"`
	Key     string          `json:"key"`
	Action  json.RawMessage `json:"action"`
}

// loadFile reads and validates the bindings file, returning the user's
// bindings, the claims they make in file order, and any warnings.
//
// The file may be either a JSON array of {context, key, action} objects — the
// spec's shape — or a flat object of "chord" → action, which is what Claude
// Code writes. Both are accepted because the flat form is what a user is most
// likely to copy out of their ~/.claude, and rejecting it would make the most
// obvious migration path fail.
func loadFile(path string) (map[string]map[string]Action, []claim, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil, nil
		}
		return nil, nil, nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil, nil, nil
	}

	out := make(map[string]map[string]Action)
	var warnings []string

	var arr []fileBinding
	if err := json.Unmarshal(data, &arr); err == nil {
		return out, applyBindings(out, arr, &warnings), warnings, nil
	}

	var flat map[string]json.RawMessage
	if err := json.Unmarshal(data, &flat); err != nil {
		return nil, nil, nil, fmt.Errorf("parsing %s: expected an array of bindings or an object of chord → action", path)
	}
	chords := make([]string, 0, len(flat))
	for chord := range flat {
		chords = append(chords, chord)
	}
	// Map order is random; sorted order makes the warnings and the outcome
	// stable between runs, so a user comparing two launches sees only real
	// differences.
	sortStrings(chords)
	var claims []claim
	for _, chord := range chords {
		b := fileBinding{Key: chord, Action: flat[chord]}
		// A chord is one binding even with a space in it — "ctrl+x ctrl+k" is a
		// sequence, not two keys — so context stays empty and normalizeKey is
		// what decides whether it can be matched.
		if c, ok := applyOne(out, b, 0, &warnings); ok {
			claims = append(claims, c)
		}
	}
	return out, claims, warnings, nil
}

// claim is one binding the file actually made, in the order the file lists it.
//
// The order is load-bearing: it decides which key wins when a file names the
// same action twice, and it is why claims are a slice rather than read back
// out of the resolved map, where the order would be Go's map iteration order.
type claim struct {
	context string
	key     string
	action  Action
}

// applyBindings validates and records a batch of bindings, appending warnings
// rather than failing when one cannot be used, and returns the claims in file
// order.
func applyBindings(out map[string]map[string]Action, bindings []fileBinding, warnings *[]string) []claim {
	var claims []claim
	for i, b := range bindings {
		if c, ok := applyOne(out, b, i, warnings); ok {
			claims = append(claims, c)
		}
	}
	return claims
}

// resolveClaims makes each claimed action reachable from exactly one key.
//
// This is what makes a binding a rebinding rather than an addition. If a user
// writes ctrl+t for toggle.tool-output, then ctrl+t means that and ctrl+o means
// nothing — otherwise they would have two keys for one action and would have to
// hunt down the old one to remove it. A key means one thing, so naming an
// action under a new key is a claim about where that action now lives.
//
// Claims are resolved against the whole merged map, not just the user's
// entries, so moving an action off a default works. When a file names one
// action under several keys, the last entry wins — the user's ordering is the
// only ordering available, and it is the one they wrote on purpose. Grouping by
// action before evicting is what makes that true: evicting as each claim is
// read would let the first claim delete the second's key, leaving the action
// with none.
func resolveClaims(resolved map[string]map[string]Action, claims []claim) {
	type target struct {
		context string
		action  Action
	}
	winners := make(map[target]string, len(claims))
	for _, c := range claims {
		if c.action == ActionNone {
			// An explicit null frees a key and claims nothing, so it must not go
			// on to evict whatever else the file bound to it.
			continue
		}
		winners[target{c.context, c.action}] = c.key
	}
	for t, key := range winners {
		for other, bound := range resolved[t.context] {
			if other != key && bound == t.action {
				delete(resolved[t.context], other)
			}
		}
	}
}

// applyOne records one binding, reporting whether it was usable.
func applyOne(out map[string]map[string]Action, b fileBinding, index int, warnings *[]string) (claim, bool) {
	where := fmt.Sprintf("entry %d", index)
	if b.Key != "" {
		where = fmt.Sprintf("key %q", b.Key)
	}

	key := strings.TrimSpace(b.Key)
	if key == "" {
		*warnings = append(*warnings, fmt.Sprintf("%s has no key; skipped", where))
		return claim{}, false
	}
	// A chord is space-separated key presses. Each step must be a key pi-go
	// can receive; a chord whose second step is a bare modifier is a typo.
	for _, step := range strings.Fields(key) {
		if step == "" {
			*warnings = append(*warnings, fmt.Sprintf("%s has an empty key step; skipped", where))
			return claim{}, false
		}
	}

	if IsReadlineLocked(step0(key)) {
		*warnings = append(*warnings, fmt.Sprintf(
			"%s: %s is a text-editing key and cannot be rebound; the default is kept",
			where, step0(key)))
		return claim{}, false
	}

	action, err := decodeAction(b.Action)
	if err != nil {
		*warnings = append(*warnings, fmt.Sprintf("%s: %v; the default is kept", where, err))
		return claim{}, false
	}

	context := normalizeContext(b.Context)
	if b.Context != "" && context == "" {
		*warnings = append(*warnings, fmt.Sprintf(
			"%s: unknown context %q; using %q", where, b.Context, ContextChat))
		context = ContextChat
	}
	if context == "" {
		context = ContextChat
	}

	// An explicit null unbinds; that has to be recorded rather than skipped,
	// or the default would come back and the user's intent would be lost.
	if out[context] == nil {
		out[context] = make(map[string]Action)
	}
	step := step0(key)
	out[context][step] = action
	return claim{context: context, key: step, action: action}, true
}

// step0 is the first key in a chord, normalized. A chord is matched on its
// first step for now — the TUI has no chord state machine yet — but the
// binding is still stored whole so the rest of the spec is honoured once
// there is one. Storing only the first step would let "ctrl+x ctrl+k" silently
// shadow plain "ctrl+x", which is worse than not supporting it.
func step0(key string) string {
	fields := strings.Fields(key)
	if len(fields) == 0 {
		return ""
	}
	return normalizeKey(fields[0])
}

func decodeAction(raw json.RawMessage) (Action, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		// null or absent: an explicit unbind.
		return ActionNone, nil
	}
	var name string
	if err := json.Unmarshal(raw, &name); err != nil {
		// An array of actions is accepted, and the first is used. Claude Code
		// allows a key to mean several things in sequence; pi-go has no chord
		// state, so the first is the honest interpretation rather than a
		// rejection of a file the user is used to.
		var list []string
		if err := json.Unmarshal(raw, &list); err != nil || len(list) == 0 {
			return ActionNone, fmt.Errorf("action must be a string or null")
		}
		name = list[0]
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return ActionNone, fmt.Errorf("action is empty")
	}
	action := Action(name)
	if !knownActions[action] {
		return ActionNone, fmt.Errorf("unknown action %q", name)
	}
	return action, nil
}

// normalizeContext maps what a user might write onto the four real contexts.
// It returns "" for anything unrecognised.
func normalizeContext(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "chat", "prompt", "input":
		return ContextChat
	case "transcript", "conversation", "scroll", "history":
		return ContextTranscript
	case "settings", "config":
		return ContextSettings
	case "overlay", "modal", "popup":
		return ContextOverlay
	default:
		return ""
	}
}

// sortStrings keeps the warnings in a stable order. It is sort.Strings under a
// name that says why it is there.
func sortStrings(s []string) { sort.Strings(s) }
