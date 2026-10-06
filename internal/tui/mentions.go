package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/permission"
	"github.com/dimetron/pi-go/internal/tools"
	"github.com/dimetron/pi-go/internal/tui/refs"
)

// The TUI surface for the two input prefixes that reach outside the prompt:
// `@path` attaches a file to the next turn, `!cmd` runs a shell command here
// and now and attaches what it printed.
//
// Both produce context the agent did not ask for, which is what makes them a
// permission question. A mention reads a file the user did not name in a tool
// call, and it is the one path into file content that does not pass through a
// tool — so if it skipped the engine, a deny rule written for Read would be
// bypassed by typing "@" instead of letting the agent read the file. Both go
// through Engine.Check with the same Request shape the tool layer uses, so one
// rule covers both.

// maxShellOutputLines caps how much of a `!cmd` result is attached. The output
// goes into the model's context, so an unbounded `!cat` on a large file is a
// context blow-up the user did not ask for; the cap says so in the transcript
// rather than silently dropping the tail.
const maxShellOutputLines = 500

// mentionOutcome is what resolving the prefixes produced, and what the user is
// told about it. Every refusal lands in Refused rather than in an error, so one
// denied mention does not discard the rest of the prompt.
type mentionOutcome struct {
	// Text is the prompt to send: the user's text with any attachments appended.
	Text string
	// Attached names each expansion that succeeded, in prompt order.
	Attached []string
	// Refused explains each expansion that did not happen. Non-empty means the
	// user must be told, because silence would read as "it worked".
	Refused []string
}

// resolveMentions expands the `@path` and `!cmd` prefixes in text.
//
// The engine may be nil — a TUI without a permission bridge, as in tests or a
// surface that never configured one. In that case the sandbox root is the only
// boundary: attachments inside the work directory work, and anything outside is
// refused rather than assumed safe. Withholding the engine must not silently
// widen what can be read, so "no engine" means "root only", not "no limits".
func (m *model) resolveMentions(text string) mentionOutcome {
	return m.expandAttachments(text)
}

// expandAttachments parses the prefixes out of text and rebuilds the prompt with
// their contents inlined. It is the whole of the attachment path; everything
// else in this file is a decision about one prefix.
func (m *model) expandAttachments(text string) mentionOutcome {
	commands, stripped := stripShellCommands(text)
	refsFound, stripped := stripFileRefs(stripped)

	out := mentionOutcome{Text: text}
	if len(commands) == 0 && len(refsFound) == 0 {
		return out
	}

	var sections []string
	for _, command := range commands {
		content, refusal := m.runShellAttachment(command)
		if refusal != "" {
			out.Refused = append(out.Refused, refusal)
			continue
		}
		out.Attached = append(out.Attached, "!"+command)
		sections = append(sections, content)
	}
	for _, ref := range refsFound {
		content, refusal := m.expandFileAttachment(ref)
		if refusal != "" {
			out.Refused = append(out.Refused, refusal)
			continue
		}
		out.Attached = append(out.Attached, "@"+refLabel(ref))
		sections = append(sections, content)
	}

	if len(sections) == 0 {
		// Nothing attached. The prompt goes as typed, minus the prefixes that
		// did not resolve — leaving a raw "@missing.go" in the text would have
		// the agent read it on its own authority, which is the exact thing the
		// permission check just refused.
		out.Text = strings.TrimSpace(stripped)
		return out
	}

	var sb strings.Builder
	sb.WriteString(strings.TrimSpace(stripped))
	sb.WriteString("\n\n--- Attached Context ---\n\n")
	sb.WriteString(strings.Join(sections, "\n\n---\n\n"))
	out.Text = sb.String()
	return out
}

// expandFileAttachment resolves one @path, gating it on the permission engine
// before any content is read.
//
// The check comes first and reads nothing on the way: the engine is asked about
// the path, and only an allow leads to the file being opened. Reading first and
// asking afterwards would make the refusal cosmetic — the content would already
// be in this process, and the only thing left to undo is the decision not to
// pass it on.
func (m *model) expandFileAttachment(ref refs.ParsedRef) (string, string) {
	// With an engine present, it alone decides whether a path may be attached.
	// An explicit allow rule is the scoped exception that justifies reading
	// outside the sandbox root, so the expander must not veto it: otherwise the
	// only way to attach such a file is to turn the engine off entirely.
	if engine := m.permissionEngine(); engine != nil {
		allowed, res := engine.Check(context.Background(), permission.Request{
			Tool: "Read",
			Arg:  ref.Value,
			Description: fmt.Sprintf("Read(%s) — from an @ mention", ref.Value),
		})
		if !allowed {
			return "", fmt.Sprintf("@%s was not attached: %s", refLabel(ref), res.Reason)
		}
		// The engine has allowed an outside-root path; bypass the sandbox
		// traversal check while still protecting against sensitive files and
		// binary content.
		result, err := m.refsExpander().ExpandFile(ref)
		if err != nil {
			return "", fmt.Sprintf("@%s was not attached: %v", refLabel(ref), err)
		}
		if result.Warning != "" {
			return "", fmt.Sprintf("@%s was not attached: %s", refLabel(ref), result.Warning)
		}
		return result.Content, ""
	}
	// No engine: the sandbox root is the only boundary. Reuse the normal path
	// so traversal, blocked-directory and sensitive-file checks stay in place.
	result, err := m.refsExpander().ExpandFile(ref)
	if err != nil {
		return "", fmt.Sprintf("@%s was not attached: %v", refLabel(ref), err)
	}
	if result.Warning != "" {
		return "", fmt.Sprintf("@%s was not attached: %s", refLabel(ref), result.Warning)
	}
	return result.Content, ""
}

// runShellAttachment runs one !cmd and formats its output as an attachment.
//
// The command goes through the permission engine as a Bash request, with the
// same shape the bash tool uses. That is the point: `!` is a way for the *user*
// to run a command without the agent's involvement, but it is still a shell,
// and a deny rule for Bash(rm -rf) has to mean it here too.
func (m *model) runShellAttachment(command string) (string, string) {
	allowed, reason := m.checkShellPermission(command)
	if !allowed {
		return "", fmt.Sprintf("!%s was not run: %s", command, reason)
	}

	out, err := runShellCapture(m.cfg.WorkDir, command)
	if err != nil {
		// The command failed, but whatever it printed is usually the reason it
		// failed and is the part the user wants in the prompt. Attaching it
		// with the failure named is more useful than discarding both.
		if strings.TrimSpace(out) == "" {
			return "", fmt.Sprintf("!%s failed: %v", command, err)
		}
		body, truncated := refs.Truncate(strings.TrimRight(out, "\n"), maxShellOutputLines)
		var sb strings.Builder
		fmt.Fprintf(&sb, "[Shell output: !%s — failed: %v]\n```\n", command, err)
		sb.WriteString(body)
		sb.WriteString("\n```")
		if truncated {
			fmt.Fprintf(&sb, "\n\n[Truncated: output exceeds %d lines]", maxShellOutputLines)
		}
		return sb.String(), ""
	}

	body, truncated := refs.Truncate(strings.TrimRight(out, "\n"), maxShellOutputLines)
	var sb strings.Builder
	fmt.Fprintf(&sb, "[Shell output: !%s]\n```\n", command)
	sb.WriteString(body)
	sb.WriteString("\n```")
	if truncated {
		fmt.Fprintf(&sb, "\n\n[Truncated: output exceeds %d lines]", maxShellOutputLines)
	}
	return sb.String(), ""
}

// checkMentionPermission asks the engine whether a path may be attached, and
// returns the reason when it may not. A nil engine is answered by the sandbox
// root alone.
func (m *model) checkMentionPermission(path string) (string, bool) {
	// A directory is a legitimate thing to attach — it expands to a listing —
	// so it is checked as a Read of that path rather than rejected for not
	// naming a file.
	if engine := m.permissionEngine(); engine != nil {
		allowed, res := engine.Check(context.Background(), permission.Request{
			Tool:       "Read",
			Arg:        path,
			Description: fmt.Sprintf("Read(%s) — from an @ mention", path),
		})
		if allowed {
			return "", true
		}
		return res.Reason, false
	}
	if err := m.refsValidator().ValidatePath(path); err != nil {
		return err.Error(), false
	}
	return "", true
}

// checkShellPermission applies the same gate to a `!cmd`. With no engine there
// is no rule set to consult, so the command runs — the user typed it
// themselves, which is the one intent signal no policy layer can improve on.
func (m *model) checkShellPermission(command string) (bool, string) {
	engine := m.permissionEngine()
	if engine == nil {
		return true, ""
	}
	allowed, res := engine.Check(context.Background(), permission.Request{
		Tool:       "Bash",
		Arg:        command,
		Description: fmt.Sprintf("Bash(%s) — from ! shell mode", command),
	})
	if allowed {
		return true, ""
	}
	return false, res.Reason
}

// shellTimeout bounds a single `!cmd`. It is short because the command runs
// synchronously between the user pressing Enter and the turn starting — a
// ten-minute build would freeze the UI with no way to see or cancel it. Output
// already printed is kept when the timeout fires, so a slow command that got
// somewhere still tells the user where it got to.
const shellTimeout = 30 * time.Second

// runShellCapture executes one `!cmd` in the session's working directory.
func runShellCapture(workDir, command string) (string, error) {
	return tools.CaptureOutput(context.Background(), workDir, command, shellTimeout)
}

// hasAttachmentPrefix reports whether text carries anything to expand, so the
// common case — an ordinary prompt — skips the expansion command entirely and
// starts its turn on the spot.
//
// It is deliberately a cheap syntactic check rather than a call into the
// parser: its only job is to decide whether the asynchronous path is worth
// entering, so a false negative here is harmless (nothing was lost) and a
// false positive costs one round trip.
func hasAttachmentPrefix(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if _, ok := shellPrefix(line); ok {
			return true
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if _, isCommand := shellPrefix(line); isCommand {
			continue
		}
		if len(parseMentionRefs(line)) > 0 {
			return true
		}
	}
	return false
}

// permissionEngine returns the policy engine, or nil when none is attached yet.
// A pending bridge reports nil, which the callers read as "no rule set".
func (m *model) permissionEngine() *permission.Engine {
	if m.cfg.PermissionBridge == nil {
		return nil
	}
	return m.cfg.PermissionBridge.Engine()
}

func (m *model) refsExpander() *refs.Expander {
	return refs.NewExpander(m.cfg.WorkDir)
}

func (m *model) refsValidator() *refs.Validator {
	return refs.NewValidator(m.cfg.WorkDir)
}

// refLabel names a reference the way the user typed it, including the line
// range, so a refusal points at the thing they actually wrote.
func refLabel(ref refs.ParsedRef) string {
	if ref.LineRange != nil {
		return fmt.Sprintf("%s:%d-%d", ref.Value, ref.LineRange.Start, ref.LineRange.End)
	}
	return ref.Value
}

// shellPrefix reports whether a line is a `!cmd` attachment, returning the
// command. It is the single definition of the syntax: the stripper and the
// runner must agree on what counts, or a line could be lifted out of the prompt
// without being run.
func shellPrefix(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "!") || len(trimmed) == 1 {
		return "", false
	}
	command := strings.TrimSpace(trimmed[1:])
	// "!!" is history expansion in an interactive bash and "!=" is a very
	// common thing to write in prose. Neither is a command.
	if command == "" || strings.HasPrefix(command, "!") || strings.HasPrefix(command, "=") {
		return "", false
	}
	return command, true
}

// stripShellCommands lifts the `!cmd` lines out of text, returning the commands
// in order and the text without them.
func stripShellCommands(text string) ([]string, string) {
	var (
		commands []string
		kept     []string
	)
	for _, line := range strings.Split(text, "\n") {
		if command, ok := shellPrefix(line); ok {
			commands = append(commands, command)
			continue
		}
		kept = append(kept, line)
	}
	return commands, strings.Join(kept, "\n")
}

// stripFileRefs lifts the @path tokens out of text, returning the references in
// the order they were written and the text without them.
//
// Only lines that are not themselves a `!cmd` are scanned. A shell command
// routinely contains @ — `git log --author=@me`, an email in a grep — and
// treating those as mentions would attach files nobody asked for.
func stripFileRefs(text string) ([]refs.ParsedRef, string) {
	var (
		found []refs.ParsedRef
		kept  []string
	)
	for _, line := range strings.Split(text, "\n") {
		if _, isCommand := shellPrefix(line); !isCommand {
			found = append(found, parseMentionRefs(line)...)
			line = removeMentionTokens(line)
		}
		kept = append(kept, line)
	}
	return found, strings.Join(kept, "\n")
}

type tokenSpan struct{ start, end int }

// mentionTokenSpans locates the @ref tokens in a line.
func mentionTokenSpans(line string) []tokenSpan {
	var spans []tokenSpan
	for i := 0; i < len(line); i++ {
		if line[i] != '@' {
			continue
		}
		j := i + 1
		for j < len(line) && !strings.ContainsRune(" \t@\"'`,;()[]{}", rune(line[j])) {
			j++
		}
		if j == i+1 {
			continue
		}
		// Trailing sentence punctuation is not part of the path, and leaving it
		// in would name a file that does not exist.
		token := strings.TrimRight(line[i:j], ".;!?")
		if len(token) == 1 {
			continue
		}
		spans = append(spans, tokenSpan{start: i, end: i + len(token)})
		i += len(token) - 1
	}
	return spans
}

// removeMentionTokens deletes the @ref tokens from a line, leaving the words
// around them and the spaces that separated them.
func removeMentionTokens(line string) string {
	var sb strings.Builder
	last := 0
	for _, span := range mentionTokenSpans(line) {
		sb.WriteString(line[last:span.start])
		// Keep one separating space, or "read @a.go and @b.go" collapses to
		// "readand".
		if span.start > last && line[span.start-1] != ' ' && sb.Len() > 0 {
			sb.WriteString(" ")
		}
		last = span.end
	}
	sb.WriteString(line[last:])
	return sb.String()
}

// parseMentionRefs resolves the mention tokens in a line into references,
// dropping anything that is not a path. An @-handle in prose and an email
// address are the two cases worth naming: neither carries a path signal, and
// expanding either would attach a file the user never named.
func parseMentionRefs(line string) []refs.ParsedRef {
	var out []refs.ParsedRef
	for _, span := range mentionTokenSpans(line) {
		if ref, ok := refs.ParseBareRef(line[span.start:span.end]); ok {
			out = append(out, ref)
		}
	}
	return out
}