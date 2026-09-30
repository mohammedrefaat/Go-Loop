package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/agent"
	pisession "github.com/dimetron/pi-go/internal/session"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// sessionPickerState manages the interactive /resume screen: the list of saved
// sessions, newest first, that the user can pick from to restore.
type sessionPickerState struct {
	list   list.Model
	width  int
	height int
}

// newSessionPicker builds the session list. Items carry the session ID in
// MenuItem.value so selecting one yields the ID to resume rather than the
// display title, which may be empty for a session that never got named.
func (m *model) newSessionPicker(sessions []pisession.Meta, width, height int) {
	items := make([]list.Item, 0, len(sessions))
	for _, s := range sessions {
		title := s.Title
		if title == "" {
			title = s.ID
		}
		desc := fmt.Sprintf("%s · %s", s.Model, s.UpdatedAt.Format("2006-01-02 15:04"))
		// The filter value carries the ID and workdir too, so searching for a
		// session by ID works even when its title says nothing useful.
		filter := fmt.Sprintf("%s %s %s", s.ID, s.WorkDir, s.Model)
		items = append(items, MenuItem{
			title:       title,
			description: desc,
			filterVal:   filter,
			value:       s.ID,
		})
	}
	m.sessionPicker = &sessionPickerState{
		list:   newInteractiveList(items, width, height, "Select Session to Resume"),
		width:  width,
		height: height,
	}
}

// openSessionPicker lists saved sessions for /resume with no argument. The
// current session is excluded: resuming the session you are already in would
// discard the conversation you are looking at to get the same one back.
func (m *model) openSessionPicker() {
	if m.cfg.SessionService == nil {
		m.appendNotice("Resume is unavailable: no session service.")
		return
	}
	metas, err := m.cfg.SessionService.ListMeta(agent.AppName, agent.DefaultUserID)
	if err != nil {
		m.appendNotice(fmt.Sprintf("Could not list sessions: %v", err))
		return
	}
	other := make([]pisession.Meta, 0, len(metas))
	for _, s := range metas {
		if s.ID != m.cfg.SessionID {
			other = append(other, s)
		}
	}
	if len(other) == 0 {
		m.appendNotice("No other sessions to resume.")
		return
	}
	m.newSessionPicker(other, m.width, m.height)
}

// handleResumeCommand implements /resume [session-id]. With an argument it
// resumes that session directly; with none it opens the picker.
func (m *model) handleResumeCommand(args []string) (tea.Model, tea.Cmd) {
	if len(args) > 0 {
		m.resumeSessionID(strings.Join(args, " "))
		return m, nil
	}
	m.openSessionPicker()
	return m, nil
}

// resumeSessionID is the seam the picker, the CLI flag, and /clear's
// "previous session" hint all funnel into. Resuming means starting a fresh
// transcript in the target session's directory; the event history stays on
// disk, so a later /resume of the same ID sees the same conversation.
func (m *model) resumeSessionID(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	if id == m.cfg.SessionID {
		m.appendNotice("Already in this session.")
		return
	}
	metas, err := m.cfg.SessionService.ListMeta(agent.AppName, agent.DefaultUserID)
	if err != nil {
		m.appendNotice(fmt.Sprintf("Could not list sessions: %v", err))
		return
	}
	var target *pisession.Meta
	for i := range metas {
		if metas[i].ID == id {
			target = &metas[i]
			break
		}
	}
	if target == nil {
		m.appendNotice(fmt.Sprintf("No session with id %q.", id))
		return
	}
	m.switchSession(*target)
}

// switchSession moves the TUI onto another session: it swaps the session ID
// and workdir, clears the visible transcript, and hands the caller a message
// to paste. A running turn is never interrupted by a resume — the caller
// checks m.running first.
func (m *model) switchSession(target pisession.Meta) {
	if err := m.cfg.SessionService.SetSessionWorkDir(m.cfg.SessionID, m.cfg.WorkDir); err != nil {
		m.loggerErrorf("set session workdir: %v", err)
	}
	m.cfg.SessionID = target.ID
	if target.WorkDir != "" {
		m.cfg.WorkDir = target.WorkDir
	}
	m.cfg.ModelName = target.Model
	m.cfg.ProviderName = target.Provider

	// The title must be read *after* the ID swap: GetSessionTitle is keyed by
	// ID, and clearConversation below resets m.sessionTitle as part of the
	// /clear semantics. Setting the field directly rather than through
	// setSessionTitle also avoids pushing the title back to the agent, which
	// would write the resumed session's title onto whichever session the
	// agent still holds open.
	m.clearConversation()
	if title, err := m.cfg.SessionService.GetSessionTitle(target.ID); err == nil && title != "" {
		m.sessionTitle = title
	}

	m.appendNotice(fmt.Sprintf("Resumed session %s.", target.ID))
}

// handleRenameCommand implements /rename [name]. An empty name clears the
// title so the session falls back to its ID in pickers.
func (m *model) handleRenameCommand(args []string) {
	if m.cfg.SessionService == nil {
		m.appendNotice("Rename is unavailable: no session service.")
		return
	}
	name := sanitizeSessionName(strings.Join(args, " "))
	if err := m.cfg.SessionService.SetSessionTitle(m.cfg.SessionID, name); err != nil {
		m.appendNotice(fmt.Sprintf("Could not rename session: %v", err))
		return
	}
	if name == "" {
		m.appendNotice("Session title cleared.")
		return
	}
	m.setSessionTitle(name)
	m.appendNotice(fmt.Sprintf("Session renamed to %q.", name))
}

// sanitizeSessionName replaces control and invisible characters with spaces
// and caps the length. A title is echoed into the terminal title, into the
// session picker, and into JSONL metadata; a raw control character there
// corrupts the terminal title escape and the file.
func sanitizeSessionName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, name)
	name = strings.Join(strings.Fields(name), " ")
	if len(name) > 200 {
		name = strings.TrimSpace(name[:200])
	}
	return name
}

// handleExportCommand implements /export [path]. With no path the transcript
// is written to a timestamped file in the session directory; with one it goes
// exactly there, creating parent directories as needed.
func (m *model) handleExportCommand(args []string) {
	path := strings.TrimSpace(strings.Join(args, " "))
	if path == "" {
		if m.cfg.SessionService == nil {
			m.appendNotice("Export is unavailable: no session service.")
			return
		}
		dir := m.cfg.SessionService.SessionDir(m.cfg.SessionID)
		path = filepath.Join(dir, fmt.Sprintf("transcript-%s.txt", time.Now().Format("20060102-150405")))
	}
	text := m.renderTranscriptText()
	if strings.TrimSpace(text) == "" {
		m.appendNotice("Nothing to export: the conversation is empty.")
		return
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			m.appendNotice(fmt.Sprintf("Could not create %s: %v", dir, err))
			return
		}
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		m.appendNotice(fmt.Sprintf("Could not write %s: %v", path, err))
		return
	}
	m.appendNotice(fmt.Sprintf("Conversation exported to %s.", path))
}

// renderTranscriptText flattens the visible chat into plain text. Only user
// and assistant turns are included: tool chatter is a TUI concern, and
// exporting it produces a transcript nobody wants to read. Messages are
// already in order — they are only ever appended — so no sort is needed.
func (m *model) renderTranscriptText() string {
	var b strings.Builder
	for _, msg := range m.chatModel.Messages {
		if msg.isMeta || msg.isError || msg.isWarning {
			continue
		}
		switch msg.role {
		case "user":
			fmt.Fprintf(&b, "## User\n\n%s\n\n", msg.content)
		case "assistant":
			if strings.TrimSpace(msg.content) != "" {
				fmt.Fprintf(&b, "## Assistant\n\n%s\n\n", msg.content)
			}
		}
	}
	return b.String()
}

// appendNotice adds a short system line to the chat. It is the sanctioned
// way to surface feedback from a command: writing to stdout from inside the
// TUI corrupts the alternate screen. isMeta renders it as a dim one-line
// note rather than a chat bubble, which is what a notice should look like.
func (m *model) appendNotice(text string) {
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: text,
		isMeta:  true,
	})
}

// loggerErrorf routes a diagnostic to the session log rather than the terminal.
func (m *model) loggerErrorf(format string, args ...any) {
	if m.cfg.Logger != nil {
		m.cfg.Logger.Errorf(format, args...)
	}
}

// handleSessionPickerKey handles keyboard input while /resume's picker is
// open: Enter resumes the highlighted session, Esc closes the picker, and
// everything else — arrows, typing to fuzzy-filter, pagination — goes to the
// list. It mirrors handleModelPickerKey so the two menus behave identically.
func (m *model) handleSessionPickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	if m.sessionPicker == nil {
		return nil, nil, false
	}

	key := msg.Key()

	// While the user is typing a filter, Esc and Enter belong to the filter
	// input; let it cancel or accept before the picker claims them.
	if m.sessionPicker.list.SettingFilter() {
		switch key.Code {
		case tea.KeyEsc, tea.KeyEnter:
			var cmd tea.Cmd
			m.sessionPicker.list, cmd = m.sessionPicker.list.Update(msg)
			return m, cmd, true
		}
	}

	switch key.Code {
	case tea.KeyEsc:
		m.sessionPicker = nil
		return m, nil, true

	case tea.KeyEnter:
		selected := m.sessionPicker.list.SelectedItem()
		m.sessionPicker = nil
		if mi, ok := selected.(MenuItem); ok {
			m.resumeSessionID(mi.Value())
		}
		return m, nil, true
	}

	var cmd tea.Cmd
	m.sessionPicker.list, cmd = m.sessionPicker.list.Update(msg)
	return m, cmd, true
}

// renderSessionPicker draws the /resume session list in a bordered box, sized
// the same way as the model picker so the two overlays do not jump around as
// the user moves between them.
func (m *model) renderSessionPicker(width int) string {
	if m.sessionPicker == nil {
		return ""
	}
	if width < 30 {
		width = 30
	}

	innerW := max(20, width-4)
	innerH := max(6, m.sessionPicker.height-2)
	m.sessionPicker.list.SetSize(innerW, innerH)

	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.palette.Cyan).
		Background(m.palette.Surface0).
		Padding(0, 1).
		Width(width)

	return style.Render(m.sessionPicker.list.View())
}

// overlaySessionPicker renders the session picker centered over the messages,
// reusing the model picker's placement.
func (m *model) overlaySessionPicker(messages string, mainWidth int) string {
	if m.sessionPicker == nil {
		return messages
	}

	pickerWidth := min(mainWidth-4, 80)
	if pickerWidth < 30 {
		pickerWidth = 30
	}
	return overlayCenteredBox(messages, m.renderSessionPicker(pickerWidth), mainWidth)
}
