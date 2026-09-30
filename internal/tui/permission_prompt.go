package tui

import (
	"context"
	"strings"

	"github.com/dimetron/pi-go/internal/permission"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The interactive half of the permission system.
//
// A tool call asking for approval arrives on the agent's goroutine, while the
// answer has to be typed on the keyboard by the same person whose terminal the
// TUI is drawing. So the approver cannot return until the UI has answered, and
// the UI cannot know a question is pending unless the approver can reach the
// model. That round trip is a channel:
//
//	agent goroutine                TUI Update loop
//	---------------                ---------------
//	RequestApproval(req)  ──┐      ┌── Update: permissionPrompt = &pending
//	    blocks on reply    │      │        renderPermissionPrompt()
//	                      ▼      ▼
//	                  chan bool ◄── resolve(true)
//
// Two properties this shape buys, both of which a callback or a shared mutex
// does not:
//
//   - A blocked approver cannot deadlock against the Update loop, because the
//     answer travels over a channel rather than being written by the loop into
//     state the approver is holding a lock on.
//   - There is exactly one writer per question. If two tool calls somehow asked
//     at once, each gets its own reply channel, and the second overwrites the
//     prompt. The first question is answered by the first keypress; the second
//     has a live goroutine that has not yet been given a prompt, so it is
//     refused rather than silently approved. Failing closed is the only safe
//     answer to a question that was never shown.

// permissionPromptState is the pending question shown in the overlay.
type permissionPromptState struct {
	req   permission.Request
	reply chan bool
}

// Approver returns a permission.Approver bound to this TUI model.
//
// Every call it makes carries a ctx, but approval is not a cancellable wait
// from the UI's point of view: once a question is on screen the only ways out
// are answering it or cancelling the run. So the ctx is not raced against a
// goroutine — that would leave a question displayed with no way to dismiss it
// and a tool goroutine that has already given up. Interrupts and shutdown take
// the Escape path instead, which is the same guarantee expressed where the user
// is looking.
func (m *model) Approver() permission.Approver {
	return func(_ context.Context, req permission.Request) (bool, error) {
		return m.requestPermission(req), nil
	}
}

// requestPermission shows the question and blocks until it is answered. It
// returns true only for an explicit approval, so a closed UI, a mode change,
// or a dropped prompt all read as a refusal.
func (m *model) requestPermission(req permission.Request) bool {
	reply := make(chan bool, 1)

	m.permissionPromptMu.Lock()
	// A question is already on screen. Refuse rather than replace it: the user
	// can only be looking at one prompt, and swapping it out would leave a tool
	// goroutine blocked forever on a channel nobody holds.
	if m.permissionPrompt != nil {
		m.permissionPromptMu.Unlock()
		return false
	}
	m.permissionPrompt = &permissionPromptState{req: req, reply: reply}
	m.permissionPromptMu.Unlock()

	// Buffered, so the answer is never lost to a send that races the return.
	return <-reply
}

// handlePermissionPromptKey resolves the approval prompt, swallowing every other
// key so a stray press cannot approve a destructive command.
func (m *model) handlePermissionPromptKey(key tea.Key) (tea.Model, tea.Cmd, bool) {
	m.permissionPromptMu.Lock()
	p := m.permissionPrompt
	m.permissionPromptMu.Unlock()
	if p == nil {
		return nil, nil, false
	}
	switch {
	case key.Code == tea.KeyEnter || (key.Code == 'y' && key.Mod == 0):
		m.resolvePermission(p, true)
		return m, nil, true
	case key.Code == 'n' && key.Mod == 0:
		m.resolvePermission(p, false)
		return m, nil, true
	case isCancelKey(key):
		// Escape, and Ctrl+C, refuse rather than approve. A stray interrupt
		// must never be the thing that lets `rm -rf` through.
		m.resolvePermission(p, false)
		return m, nil, true
	}
	// Every other key is consumed but does nothing, so the prompt cannot be
	// missed and a letter typed by reflex is not an approval.
	return m, nil, true
}

// resolvePermission clears the prompt and delivers the answer exactly once.
func (m *model) resolvePermission(p *permissionPromptState, approved bool) {
	m.permissionPromptMu.Lock()
	// Clear only if this is still the displayed question. A mode switch or a
	// second question may have replaced it, and clobbering that would strand
	// whoever is waiting now.
	if m.permissionPrompt == p {
		m.permissionPrompt = nil
	}
	m.permissionPromptMu.Unlock()
	p.reply <- approved
}

// clearPermissionPrompt drops any pending question, answering it with a
// refusal. It is called when the mode changes or the UI is torn down, so a tool
// goroutine is never left blocked on a prompt that will never be drawn again.
func (m *model) clearPermissionPrompt() {
	m.permissionPromptMu.Lock()
	p := m.permissionPrompt
	m.permissionPrompt = nil
	m.permissionPromptMu.Unlock()
	if p != nil {
		p.reply <- false
	}
}

// renderPermissionPrompt draws the question. It is shown over the messages, in
// the same place and style as the other overlays, so it reads as part of the
// UI rather than as output that leaked onto the screen.
func (m *model) renderPermissionPrompt(width int) string {
	m.permissionPromptMu.Lock()
	p := m.permissionPrompt
	m.permissionPromptMu.Unlock()
	if p == nil {
		return ""
	}

	title := lipgloss.NewStyle().Foreground(m.palette.Accent).Render("Permission required")
	body := wrapForWidth(p.req.Description, width-4)
	if body == "" {
		body = p.req.Tool
	}
	// The rule that decided to ask is the thing the user needs in order to
	// judge the request, so it is shown rather than hidden behind a debug flag.
	var detail []string
	if p.req.Arg != "" && p.req.Arg != p.req.Description {
		detail = append(detail, lipgloss.NewStyle().Foreground(m.palette.Dim).Render("argument: "+truncate(p.req.Arg, width-8)))
	}
	keys := lipgloss.NewStyle().Foreground(m.palette.Dim).Render("y / enter allow   ·   n / esc deny")
	lines := []string{title, ""}
	lines = append(lines, strings.Split(body, "\n")...)
	if len(detail) > 0 {
		lines = append(lines, "")
		lines = append(lines, detail...)
	}
	lines = append(lines, "", keys)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.palette.Accent).
		Padding(0, 1).
		Width(width - 2).
		Render(strings.Join(lines, "\n"))
	return box
}

// overlayPermissionPrompt paints the prompt centered over the messages.
func (m *model) overlayPermissionPrompt(messages string, mainWidth int) string {
	width := min(mainWidth-4, 80)
	if width < 30 {
		width = 30
	}
	box := m.renderPermissionPrompt(width)
	if box == "" {
		return messages
	}

	// overlayCenteredBox clips the box to the viewport from the top, which is
	// right for a picker — a truncated list is still a list. It is wrong here.
	// A short viewport would keep the border and drop the question, and the
	// agent would sit blocked on a prompt with nothing on screen to answer,
	// which is the one failure this whole feature exists to prevent.
	lines := strings.Split(messages, "\n")
	viewport := len(lines)
	if viewport > 0 {
		boxLines := strings.Split(box, "\n")
		switch {
		case len(boxLines) <= viewport:
			// Fits as-is.
		case viewport >= 4:
			// Room for the question and the keys, but not the optional detail.
			// Trimmed from the bottom, since the top holds the question.
			box = strings.Join(boxLines[len(boxLines)-viewport:], "\n")
		default:
			// Too short for a bordered box at all. A compact one-liner is the
			// only form that can still be read, and reading it matters more
			// than it looking like the other overlays.
			box = m.renderCompactPermissionPrompt(width)
		}
	}
	return overlayCenteredBox(messages, box, mainWidth)
}

// renderCompactPermissionPrompt is the fallback for a viewport too short to
// hold the bordered box. It drops the border and the argument detail and keeps
// the two things that matter: what is being asked, and how to answer.
func (m *model) renderCompactPermissionPrompt(width int) string {
	m.permissionPromptMu.Lock()
	p := m.permissionPrompt
	m.permissionPromptMu.Unlock()
	if p == nil {
		return ""
	}
	question := p.req.Description
	if question == "" {
		question = p.req.Tool
	}
	line := "Permission required: " + truncate(question, max(8, width-38)) + "  ·  y/n"
	return truncate(line, width)
}

// handleModeCycleKey implements Shift+Tab, the way Claude Code surfaces
// permission modes.
//
// It is deliberately placed ahead of the input and popup handlers rather than
// among them. Loosening a session must be possible *while* the agent is
// running — that is the only time a deny rule is actually inconvenient — and
// the input box or a search popup may well be focused at that moment. Binding
// the key only when nothing else is open would make the escape hatch
// unavailable exactly when it is needed.
//
// The permission prompt keeps the key for itself: a mode change answers no
// question, so it must not also silently resolve one. That ordering is
// guaranteed by the handler order, not by a check here.
func (m *model) handleModeCycleKey(key tea.Key) (tea.Model, tea.Cmd, bool) {
	// Tab plus the shift modifier, the same spelling selectByTab already uses.
	// Bubble Tea v2 has no separate backtab code, so terminals that send ESC [ Z
	// arrive here as KeyTab/ModShift like every other one.
	if key.Code != tea.KeyTab || key.Mod != tea.ModShift {
		return nil, nil, false
	}
	bridge := m.cfg.PermissionBridge
	if bridge == nil {
		return nil, nil, false
	}
	engine := bridge.Engine()
	if engine == nil {
		// No policy is configured, so there is no mode to be in. Swallowing the
		// key silently would read as a dead key; the caller falls through to the
		// input handler, where Shift+Tab is inert.
		return m, nil, true
	}

	mode := engine.CycleMode()
	// A prompt on screen was raised under the old mode. It is not answered —
	// the user has not decided anything — but it must not stay on screen either,
	// or the next keypress is read as the answer to a question the user has
	// already navigated away from. It is dropped as a refusal, which fails
	// closed: if the new mode is looser, the agent retries the call and asks
	// again.
	m.clearPermissionPrompt()
	m.flash = "Permission mode: " + string(mode)
	return m, nil, true
}

// wrapForWidth hard-wraps text to a column count. The overlay is drawn by
// lipgloss but composed by hand into the message string, so the text has to be
// broken before it gets there — a 2000-character command would otherwise
// reflow the whole box and tear the overlay.
func wrapForWidth(s string, width int) string {
	if width < 8 {
		width = 8
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if len(line) <= width {
			out = append(out, line)
			continue
		}
		for len(line) > width {
			out = append(out, line[:width])
			line = line[width:]
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// truncate shortens s to n characters, marking that it was cut.
func truncate(s string, n int) string {
	if n < 4 {
		n = 4
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
