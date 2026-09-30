package extension

import (
	"fmt"
	"regexp"
	"strings"
)

// The hook event set.
//
// A hook is a shell command that runs at a named moment and, for the events
// that can be blocked on, returns a decision. The set is deliberately wider
// than "before tool" and "after tool": a hook system that only sees tool calls
// cannot implement the policies people actually want (block writes to a branch
// you are not on, summarise before compaction, notify when a subagent stalls),
// because those policies are about turns and sessions, not about calls.
//
// Every event is fired, but they differ in whether their answer matters:
//
//   - PreToolUse is *blocking*: its decision reaches the permission seam and can
//     stop the call. Everything else is observational, and its return value is
//     logged rather than enforced.
//
// That asymmetry is the reason this is one type rather than fifteen. Treating
// an observational hook as if it could block would mean every hook author had
// to reason about a guarantee the system does not give.
type Event string

const (
	// EventPreToolUse fires before a tool runs, with the tool name and its
	// arguments. Its decision is enforced at the permission seam.
	EventPreToolUse Event = "PreToolUse"
	// EventPostToolUse fires after a tool succeeds, with its result.
	EventPostToolUse Event = "PostToolUse"
	// EventPostToolUseFailure fires after a tool fails, with the error. It is
	// separate from PostToolUse so a formatter can style the two differently
	// without inspecting the result payload to work out which it is.
	EventPostToolUseFailure Event = "PostToolUseFailure"
	// EventUserPromptSubmit fires when the user submits a prompt, before the
	// agent sees it.
	EventUserPromptSubmit Event = "UserPromptSubmit"
	// EventSessionStart fires once when a session begins, with its source.
	EventSessionStart Event = "SessionStart"
	// EventSessionEnd fires once when a session ends, with its reason.
	EventSessionEnd Event = "SessionEnd"
	// EventPreCompact fires before the transcript is compacted, with the
	// trigger and its size.
	EventPreCompact Event = "PreCompact"
	// EventStop fires when the agent finishes responding.
	EventStop Event = "Stop"
	// EventSubagentStop fires when a subagent finishes responding.
	EventSubagentStop Event = "SubagentStop"
	// EventNotification fires when the UI raises a notice.
	EventNotification Event = "Notification"
	// EventTaskCompleted fires when a tracked task reaches done.
	EventTaskCompleted Event = "TaskCompleted"
	// EventStopFailure fires when the agent stops because of an error.
	EventStopFailure Event = "StopFailure"
	// EventWorktreeCreate fires before pi-go creates a worktree for a subagent.
	EventWorktreeCreate Event = "WorktreeCreate"
	// EventWorktreeRemove fires before pi-go removes one.
	EventWorktreeRemove Event = "WorktreeRemove"
	// EventEndConversation fires when a conversation is archived or discarded.
	EventEndConversation Event = "EndConversation"
)

// Events lists every supported event, in the order `/hook` would present them.
//
// The order is grouped by phase — session, turn, tool, session — rather than
// alphabetical, because the list exists to answer "when does this fire?" and a
// reader looking for where a turn begins should not have to know the alphabet.
var Events = []Event{
	EventSessionStart,
	EventUserPromptSubmit,
	EventPreToolUse,
	EventPostToolUse,
	EventPostToolUseFailure,
	EventNotification,
	EventTaskCompleted,
	EventSubagentStop,
	EventStop,
	EventStopFailure,
	EventPreCompact,
	EventWorktreeCreate,
	EventWorktreeRemove,
	EventEndConversation,
	EventSessionEnd,
}

// ValidEvent reports whether e is a supported event name.
func ValidEvent(e Event) bool {
	for _, known := range Events {
		if known == e {
			return true
		}
	}
	return false
}

// legacyEvents maps the event names pi-go accepted before the set was widened
// to their replacements.
//
// The old names were positional ("before_tool") and the new ones are named for
// the moment in the conversation ("PreToolUse"), which is what makes them
// extensible: a name that encodes where it sits in the sequence has to be
// invented per event, and inventing twenty of them is how you end up with
// twenty slightly different spellings. Existing configs keep working; the
// mapping is one-way on purpose, so nothing is silently renamed on disk.
var legacyEvents = map[string]Event{
	"before_tool":         EventPreToolUse,
	"after_tool":          EventPostToolUse,
	"turn_complete":       EventStop,
	"user_input_required": EventNotification,
}

// ParseEvent resolves a configured event name, accepting both the current
// spelling and the legacy one. An unknown name is an error rather than a
// silent no-op: a hook wired to an event that does not exist looks exactly
// like a hook that never fires, and the difference matters.
func ParseEvent(name string) (Event, error) {
	if e, ok := legacyEvents[name]; ok {
		return e, nil
	}
	e := Event(name)
	if ValidEvent(e) {
		return e, nil
	}
	return "", fmt.Errorf("unknown hook event %q", name)
}

// Matcher selects the calls an event fires for.
//
// Claude Code spells this as a regular expression matched against the tool
// name. pi-go originally used an exact-match list, which cannot express "any
// MCP tool" or "anything that writes". The list is kept as the fallback so old
// configs keep their meaning, and both are honoured when both are present —
// the regex is an additional filter, not a replacement, because a config with
// both is asking for a narrower match than either alone.
type Matcher struct {
	// Tools are exact tool names. Empty means "any tool".
	Tools []string
	// Regex is matched against the tool name. Empty means "no regex filter".
	Regex *regexp.Regexp
}

// matcherRE compiles a matcher string, falling back to an exact-name match
// when it is not a valid regular expression.
//
// Falling back rather than failing matters for the common case: `mcp__*` is
// the spelling everyone writes, and it is not a valid regex character class.
// Rejecting it would turn the most natural way to express "any MCP tool" into
// a config error, so it is treated as the glob it looks like.
func compileMatcher(pattern string) (*regexp.Regexp, bool) {
	if pattern == "" {
		return nil, false
	}
	if re, err := regexp.Compile(pattern); err == nil {
		return re, true
	}
	// Not a regex: treat it as a shell-style glob over tool names. `*` is the
	// only metacharacter that appears in practice, and it is handled by
	// escaping everything else so `mcp__*` means the prefix rather than
	// whatever the regex engine would have made of it.
	var b strings.Builder
	for _, r := range pattern {
		if r == '*' {
			b.WriteString(".*")
			continue
		}
		b.WriteString(regexp.QuoteMeta(string(r)))
	}
	re, err := regexp.Compile("^" + b.String() + "$")
	if err != nil {
		return nil, false
	}
	return re, true
}

// matcherFrom builds a Matcher from the legacy `tools` list and the current
// `matcher` pattern.
func matcherFrom(tools []string, pattern string) Matcher {
	m := Matcher{Tools: tools}
	if re, ok := compileMatcher(pattern); ok {
		m.Regex = re
	}
	return m
}

// matches reports whether the matcher selects the given tool name.
//
// The two forms are alternatives, not a conjunction. A matcher exists for the
// cases a name list cannot express, so a config carrying both is expressing
// "these tools, or anything matching this" — and intersecting them would mean
// that adding a matcher to a hook narrowed it to the intersection, silently
// stopping a hook from firing for every tool it was already written for.
//
// A matcher with neither form selects everything, which is the documented
// default: "fires for all tools" is what an unqualified hook means.
func (m Matcher) matches(tool string) bool {
	if len(m.Tools) > 0 {
		for _, t := range m.Tools {
			if t == tool {
				return true
			}
		}
		// Not listed, so a pattern can still catch it — but with no pattern
		// there is nothing left to match on, and an unqualified hook is the
		// one that selects everything.
		if m.Regex == nil {
			return false
		}
	}
	return m.Regex == nil || m.Regex.MatchString(tool)
}
