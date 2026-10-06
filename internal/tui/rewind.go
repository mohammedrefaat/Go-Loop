package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/checkpoint"
	pisession "github.com/dimetron/pi-go/internal/session"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// rewindPickerState is the /rewind turn picker: one row per checkpoint, newest
// first, in the same overlay shape as the model and session pickers so moving
// between the three does not move the box.
type rewindPickerState struct {
	list   list.Model
	width  int
	height int
}

// checkpointItem adapts a checkpoint to the list. The ID is the value so a
// selection carries a checkpoint, not a row index — a picker rebuilt after a
// restore could otherwise resolve the same index to a different turn.
type checkpointItem struct {
	checkpoint.Checkpoint
}

func (i checkpointItem) Title() string       { return i.ID }
func (i checkpointItem) Description() string { return "" }
func (i checkpointItem) FilterValue() string { return i.ID + " " + i.Label }

// handleRewindCommand implements /rewind [id] [action]. With no argument it
// opens the turn picker.
func (m *model) handleRewindCommand(args []string) (tea.Model, tea.Cmd) {
	if m.cfg.Checkpoints == nil {
		m.appendNotice("Rewind is unavailable: checkpointing is disabled.")
		return m, nil
	}
	if m.running {
		m.appendNotice("Cannot rewind while the agent is running.")
		return m, nil
	}

	if len(args) == 0 {
		m.openRewindPicker()
		return m, nil
	}
	// Anything else is a checkpoint ID with an optional action, so a mistyped
	// ID is reported as such rather than opening a picker the user then has to
	// navigate away from.
	m.applyRewind(args[0], args[1:])
	return m, nil
}

// rewindActions are the five operations /rewind offers. They are named rather
// than numbered so the help text and the picker description can name the same
// thing.
var rewindActions = []struct{ name, desc string }{
	{"code+conversation", "restore both files and conversation"},
	{"conversation", "restore the conversation only"},
	{"code", "restore the files only"},
	{"summarize-from", "summarize the conversation from this point"},
	{"summarize-upto", "summarize the conversation up to this point"},
}

// openRewindPicker lists the session's checkpoints, newest first.
func (m *model) openRewindPicker() {
	cps, err := m.cfg.Checkpoints.List(m.sessionDir())
	if err != nil {
		m.appendNotice(fmt.Sprintf("Could not read checkpoints: %v", err))
		return
	}
	if len(cps) == 0 {
		m.appendNotice("No checkpoints yet — send a prompt to create one.")
		return
	}

	items := make([]list.Item, 0, len(cps))
	for _, cp := range cps {
		items = append(items, checkpointItem{Checkpoint: cp})
	}
	m.rewindPicker = &rewindPickerState{
		list:   newInteractiveList(items, m.width, m.height, "Rewind — select a turn, then choose an action"),
		width:  m.width,
		height: m.height,
	}
}

// applyRewind performs one action against a checkpoint. Every branch reports
// what it did through appendNotice: a rewind that restores three files and drops
// a conversation must say so, or the user cannot tell what state they are in.
func (m *model) applyRewind(id string, args []string) {
	sessionDir := m.sessionDir()
	cp, err := checkpoint.Get(sessionDir, id)
	if err != nil {
		m.appendNotice(fmt.Sprintf("Could not read checkpoint %s: %v", id, err))
		return
	}

	action := "code+conversation"
	if len(args) > 0 {
		action = strings.ToLower(args[0])
	}
	// Aliases a user is likely to type, mapped before the table so an unknown
	// action reports the same way regardless of how it was phrased.
	switch action {
	case "both", "all":
		action = "code+conversation"
	case "summary", "summarize":
		action = "summarize-from"
	}

	switch action {
	case "code+conversation":
		out := checkpoint.Restore(cp)
		m.truncateConversation(cp)
		m.noticeRewind(cp, "Restored code and conversation. "+out.String())
	case "conversation":
		m.truncateConversation(cp)
		m.noticeRewind(cp, "Restored conversation only; files left as they are.")
	case "code":
		out := checkpoint.Restore(cp)
		m.noticeRewind(cp, "Restored code only. "+out.String())
	case "summarize-from", "summarize-upto":
		m.summarizeFromCheckpoint(cp, action == "summarize-from")
	default:
		m.appendNotice(fmt.Sprintf(
			"Unknown rewind action %q. Try: %s.", action,
			strings.Join(rewindActionNames(), ", ")))
	}
}

func rewindActionNames() []string {
	names := make([]string, 0, len(rewindActions))
	for _, a := range rewindActions {
		names = append(names, a.name)
	}
	return names
}

// truncateConversation cuts the session back to the checkpoint's event index
// and rebuilds the visible transcript from what remains.
//
// The gauge refresh matters as much as the truncation: without it the context
// bar would keep reporting the window size of a conversation that no longer
// exists, which is the same lie /clear has to correct.
func (m *model) truncateConversation(cp *checkpoint.Checkpoint) {
	svc := m.cfg.SessionService
	if svc == nil {
		m.appendNotice("Rewind is unavailable: no session service.")
		return
	}
	id := m.cfg.SessionID
	if err := svc.TruncateEvents(id, agent.AppName, agent.DefaultUserID, cp.EventIndex); err != nil {
		m.appendNotice(fmt.Sprintf("Could not restore the conversation: %v", err))
		return
	}

	// Replay the surviving prefix rather than filtering the visible transcript:
	// the transcript is the model's view with streaming already merged, and
	// slicing it by turn count would not line up with an event index.
	m.chatModel.Messages = nil
	m.chatModel.Scroll = 0
	m.restoreSession()

	if tt := m.cfg.TokenTracker; tt != nil {
		if est, err := svc.EstimateTokens(id, agent.AppName, agent.DefaultUserID); err == nil {
			tt.SetLastPromptTokens(int64(est))
		}
	}
}

// noticeRewind reports a completed rewind, naming the turn restored to and any
// files the capture could not cover. A checkpoint that skipped a symlink must
// say so with the count: the point of /rewind is that the user knows the state
// of their tree.
func (m *model) noticeRewind(cp *checkpoint.Checkpoint, action string) {
	var b strings.Builder
	b.WriteString(action)
	if cp.Label != "" {
		fmt.Fprintf(&b, " (to %q)", truncateRunes(cp.Label, 60))
	} else {
		fmt.Fprintf(&b, " (to %s)", cp.CreatedAt.Format("2006-01-02 15:04"))
	}
	if cp.SkippedSymlinks > 0 {
		fmt.Fprintf(&b, "\n%d symlink(s) were not snapshotted and were left alone.", cp.SkippedSymlinks)
	}
	if cp.SkippedTooLarge > 0 {
		fmt.Fprintf(&b, "\n%d file(s) were too large to snapshot and were left alone.", cp.SkippedTooLarge)
	}
	m.appendNotice(b.String())
}

// summarizeFromCheckpoint compacts the conversation at a checkpoint boundary.
//
// The session service summarizes a whole session's history, which is what both
// actions reduce to here: "up to here" summarizes what stands without
// discarding anything, and "from here" truncates to the boundary first so the
// summary covers only the turns after it. Reporting the action as asked, plus
// the boundary it used, keeps the user able to tell which turn they are
// looking at afterwards.
func (m *model) summarizeFromCheckpoint(cp *checkpoint.Checkpoint, from bool) {
	svc := m.cfg.SessionService
	if svc == nil {
		m.appendNotice("Rewind is unavailable: no session service.")
		return
	}
	if from {
		if err := svc.TruncateEvents(
			m.cfg.SessionID, agent.AppName, agent.DefaultUserID, cp.EventIndex,
		); err != nil {
			m.appendNotice(fmt.Sprintf("Could not rewind to that turn: %v", err))
			return
		}
		m.chatModel.Messages = nil
		m.restoreSession()
	}
	if err := svc.Compact(
		m.cfg.SessionID, agent.AppName, agent.DefaultUserID,
		pisession.SimpleSummarizer, pisession.CompactConfig{},
	); err != nil {
		m.appendNotice(fmt.Sprintf("Could not summarize: %v", err))
		return
	}
	if tt := m.cfg.TokenTracker; tt != nil {
		if est, estErr := svc.EstimateTokens(
			m.cfg.SessionID, agent.AppName, agent.DefaultUserID,
		); estErr == nil {
			tt.SetLastPromptTokens(int64(est))
		}
	}

	which := "up to"
	if from {
		which = "from"
	}
	m.appendNotice(fmt.Sprintf(
		"Summarized the conversation %s this turn (%s).",
		which, cp.CreatedAt.Format("2006-01-02 15:04")))
}

// sessionDir is the on-disk directory for the current session, or "" when there
// is no session service. Checkpoints live under it so they travel with the
// session through archive.
func (m *model) sessionDir() string {
	if m.cfg.SessionService == nil || m.cfg.SessionID == "" {
		return ""
	}
	return m.cfg.SessionService.SessionDir(m.cfg.SessionID)
}

// escRewindWindow is how long the first Esc stays a live candidate for the
// second. It is generous rather than tight: the gesture is two presses of a
// key people hit reflexively, and a window that expires mid-gesture makes the
// shortcut feel broken rather than deliberate.
const escRewindWindow = 2 * time.Second

// tryRewindEscape implements the Esc-Esc shortcut: two Escs with an empty
// prompt and nothing running open the turn picker.
//
// The single-Esc case must stay exactly what it was — Esc already cancels a
// run, and nothing here may interfere with that — so the first press only arms
// the gesture, and the second consumes it.
//
// It reports whether the picker was opened, so the caller can tell the
// difference between an Esc that did something and one that only armed.
func (m *model) tryRewindEscape() bool {
	now := time.Now()
	armed := m.escArmed && now.Sub(m.lastEsc) <= escRewindWindow
	m.lastEsc = now

	// Anything that is not a bare Esc on an empty prompt breaks the gesture:
	// a user who typed something and then hit Esc twice meant to dismiss
	// something, not to rewind.
	if armed && m.inputModel.Text == "" {
		m.escArmed = false
		m.openRewindPicker()
		return true
	}
	m.escArmed = m.inputModel.Text == ""
	return false
}

// disarmRewindEscape drops a pending Esc-Esc. Called from the key handler for
// every key that is not the first Esc, so the gesture cannot survive across an
// unrelated keystroke.
func (m *model) disarmRewindEscape() {
	m.escArmed = false
}

// handleRewindPickerKey routes keys while the turn picker is open: Enter picks
// a checkpoint and shows the action list, Esc closes.
func (m *model) handleRewindPickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	if m.rewindPicker == nil {
		return nil, nil, false
	}

	key := msg.Key()

	if m.rewindPicker.list.SettingFilter() {
		switch key.Code {
		case tea.KeyEsc, tea.KeyEnter:
			var cmd tea.Cmd
			m.rewindPicker.list, cmd = m.rewindPicker.list.Update(msg)
			return m, cmd, true
		}
	}

	switch key.Code {
	case tea.KeyEsc:
		m.rewindPicker = nil
		return m, nil, true

	case tea.KeyEnter:
		selected := m.rewindPicker.list.SelectedItem()
		m.rewindPicker = nil
		if item, ok := selected.(checkpointItem); ok {
			m.applyRewind(item.ID, nil)
		}
		return m, nil, true
	}

	var cmd tea.Cmd
	m.rewindPicker.list, cmd = m.rewindPicker.list.Update(msg)
	return m, cmd, true
}

// renderRewindPicker draws the turn list in a bordered box.
func (m *model) renderRewindPicker(width int) string {
	if m.rewindPicker == nil {
		return ""
	}
	if width < 30 {
		width = 30
	}
	innerW := max(20, width-4)
	innerH := max(6, m.rewindPicker.height-2)
	m.rewindPicker.list.SetSize(innerW, innerH)

	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.palette.Cyan).
		Background(m.palette.Surface0).
		Padding(0, 1).
		Width(width)
	return style.Render(m.rewindPicker.list.View())
}

// overlayRewindPicker centers the picker over the messages, matching the model
// and session pickers.
func (m *model) overlayRewindPicker(messages string, mainWidth int) string {
	if m.rewindPicker == nil {
		return messages
	}
	pickerWidth := min(mainWidth-4, 80)
	if pickerWidth < 30 {
		pickerWidth = 30
	}
	return overlayCenteredBox(messages, m.renderRewindPicker(pickerWidth), mainWidth)
}

// checkpointLabel is the row title: the turn's own label, or its time when the
// prompt produced an empty one.
func checkpointLabel(cp *checkpoint.Checkpoint) string {
	if cp.Label != "" {
		return cp.Label
	}
	return cp.CreatedAt.Format(time.RFC3339)
}