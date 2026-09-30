package extension

import "testing"

func TestParseEventCurrentNames(t *testing.T) {
	for _, name := range []string{
		"PreToolUse", "PostToolUse", "PostToolUseFailure", "UserPromptSubmit",
		"SessionStart", "SessionEnd", "PreCompact", "Stop", "SubagentStop",
		"Notification", "TaskCompleted", "StopFailure",
		"WorktreeCreate", "WorktreeRemove", "EndConversation",
	} {
		parsed, err := ParseEvent(name)
		if err != nil {
			t.Errorf("ParseEvent(%q) error: %v", name, err)
			continue
		}
		if string(parsed) != name {
			t.Errorf("ParseEvent(%q) = %q", name, parsed)
		}
	}
}

// The legacy spellings are mapped one way — a config written against the old
// names has to keep working, and it must not be possible to map a new name back
// onto an old one and have that silently accepted.
func TestParseEventLegacyNames(t *testing.T) {
	for legacy, want := range legacyEvents {
		got, err := ParseEvent(legacy)
		if err != nil {
			t.Errorf("ParseEvent(%q) error: %v", legacy, err)
			continue
		}
		if got != want {
			t.Errorf("ParseEvent(%q) = %q, want %q", legacy, got, want)
		}
	}
}

func TestParseEventUnknownIsError(t *testing.T) {
	for _, name := range []string{"", "nonsense", "pretooluse", "Before_Tool"} {
		if _, err := ParseEvent(name); err == nil {
			t.Errorf("ParseEvent(%q) accepted an unknown event", name)
		}
	}
}

func TestMatcherTools(t *testing.T) {
	m := matcherFrom([]string{"read", "bash"}, "")
	if !m.matches("read") || !m.matches("bash") {
		t.Error("a listed tool did not match")
	}
	if m.matches("write") {
		t.Error("an unlisted tool matched")
	}
}

func TestMatcherEmptyMatchesEverything(t *testing.T) {
	m := matcherFrom(nil, "")
	for _, tool := range []string{"read", "write", "mcp__github__create_issue"} {
		if !m.matches(tool) {
			t.Errorf("%q did not match an unrestricted matcher", tool)
		}
	}
}

func TestMatcherRegex(t *testing.T) {
	m := matcherFrom(nil, `mcp__.*`)
	if !m.matches("mcp__github__create_issue") {
		t.Error("regex matcher did not match")
	}
	if m.matches("read") {
		t.Error("regex matcher matched an unrelated tool")
	}
}

func TestMatcherRegexAnchors(t *testing.T) {
	m := matcherFrom(nil, `.*_write$`)
	if !m.matches("notebook_write") {
		t.Error("anchored regex did not match a suffix")
	}
	if m.matches("write_notebook") {
		t.Error("anchored regex matched a prefix it should not have")
	}
}

// A pattern that is not a valid regex is treated as a glob, not a config
// error: `mcp__*` is what a user naturally writes, and failing the whole hook
// set over it would be a hostile reading.
func TestMatcherInvalidRegexFallsBackToGlob(t *testing.T) {
	m := matcherFrom(nil, "mcp__*")
	if !m.matches("mcp__github") {
		t.Error("glob fallback did not match a prefixed tool")
	}
	if m.matches("read") {
		t.Error("glob fallback matched an unrelated tool")
	}
}

// Tools and Matcher are alternatives, not a conjunction: a hook written with
// both is matching either. Intersecting them would make a matcher that broadens
// a tool list impossible, which is the opposite of what it is for.
func TestMatcherToolsAndMatcherAreAlternatives(t *testing.T) {
	m := matcherFrom([]string{"read"}, "mcp__.*")
	if !m.matches("read") {
		t.Error("a listed tool did not match")
	}
	if !m.matches("mcp__github") {
		t.Error("a pattern-matched tool did not match")
	}
	if m.matches("write") {
		t.Error("an unrelated tool matched")
	}
}
