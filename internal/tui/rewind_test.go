package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"google.golang.org/adk/v2/session"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/checkpoint"
	pisession "github.com/dimetron/pi-go/internal/session"
)

// The tests below cover the three things /rewind can get wrong: losing a file
// the user wanted back, claiming a restore it did not perform, and firing the
// Esc-Esc shortcut when the user only meant to press Escape once.

// newRewindFixture builds a session with two turns of events plus a real
// working directory, a checkpoint manager, and a model wired to all of them.
func newRewindFixture(t *testing.T) (*model, *checkpoint.Manager, string) {
	t.Helper()

	sessionsDir := filepath.Join(t.TempDir(), "sessions")
	svc, err := pisession.NewFileService(sessionsDir)
	if err != nil {
		t.Fatalf("NewFileService: %v", err)
	}
	resp, err := svc.Create(context.Background(), &session.CreateRequest{
		AppName: agent.AppName,
		UserID:  agent.DefaultUserID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := resp.Session.ID()
	for i, text := range []string{"first question", "first answer", "second question", "second answer"} {
		role := "user"
		if i%2 == 1 {
			role = "model"
		}
		ev := makeTextEvent(role, text)
		ev.ID = "ev-" + string(rune('a'+i))
		if err := svc.AppendEvent(context.Background(), resp.Session, ev); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}

	m := newTestModel(t)
	m.cfg.SessionService = svc
	m.cfg.SessionID = id
	m.cfg.Checkpoints = checkpoint.New()
	m.cfg.WorkDir = t.TempDir()
	return m, m.cfg.Checkpoints, svc.SessionDir(id)
}

// openCheckpoint stands in for a completed turn: a checkpoint at index 2, one
// captured file, and the file then overwritten the way the agent would.
func openCheckpoint(t *testing.T, dir, file string) *checkpoint.Checkpoint {
	t.Helper()
	if err := os.WriteFile(file, []byte("before"), 0o644); err != nil {
		t.Fatalf("seeding file: %v", err)
	}
	mgr := checkpoint.New()
	cp, err := mgr.Begin(dir, 2, "second question")
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := mgr.Capture(dir, file); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if err := os.WriteFile(file, []byte("after"), 0o644); err != nil {
		t.Fatalf("overwriting file: %v", err)
	}
	return cp
}

func lastNotice(m *model) string {
	if n := len(m.chatModel.Messages); n > 0 {
		return m.chatModel.Messages[n-1].content
	}
	return ""
}

func TestRewindRestoresCodeAndConversation(t *testing.T) {
	m, mgr, dir := newRewindFixture(t)
	file := filepath.Join(m.cfg.WorkDir, "main.go")
	cp := openCheckpoint(t, dir, file)

	m.applyRewind(cp.ID, nil)

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("reading the restored file: %v", err)
	}
	if string(data) != "before" {
		t.Errorf("file = %q, want %q", data, "before")
	}
	if n, err := m.cfg.SessionService.EventCount(m.cfg.SessionID, agent.AppName, agent.DefaultUserID); err != nil || n != 2 {
		t.Errorf("session holds %d events (%v), want 2 — the conversation must end at the checkpoint", n, err)
	}
	if !strings.Contains(lastNotice(m), "code and conversation") {
		t.Errorf("notice = %q, want it to say both were restored", lastNotice(m))
	}
	_ = mgr
}

func TestRewindConversationOnlyLeavesFilesAlone(t *testing.T) {
	m, _, dir := newRewindFixture(t)
	file := filepath.Join(m.cfg.WorkDir, "main.go")
	cp := openCheckpoint(t, dir, file)

	m.applyRewind(cp.ID, []string{"conversation"})

	data, _ := os.ReadFile(file)
	if string(data) != "after" {
		t.Errorf("file = %q, want it untouched at %q", data, "after")
	}
	if n, _ := m.cfg.SessionService.EventCount(m.cfg.SessionID, agent.AppName, agent.DefaultUserID); n != 2 {
		t.Errorf("session holds %d events, want 2", n)
	}
}

func TestRewindCodeOnlyLeavesTheConversationAlone(t *testing.T) {
	m, _, dir := newRewindFixture(t)
	file := filepath.Join(m.cfg.WorkDir, "main.go")
	cp := openCheckpoint(t, dir, file)

	m.applyRewind(cp.ID, []string{"code"})

	data, _ := os.ReadFile(file)
	if string(data) != "before" {
		t.Errorf("file = %q, want %q", data, "before")
	}
	if n, _ := m.cfg.SessionService.EventCount(m.cfg.SessionID, agent.AppName, agent.DefaultUserID); n != 4 {
		t.Errorf("session holds %d events, want all 4 kept", n)
	}
}

func TestRewindUnknownIDIsReportedNotFatal(t *testing.T) {
	m, _, _ := newRewindFixture(t)
	m.applyRewind("no-such-checkpoint", nil)
	if !strings.Contains(lastNotice(m), "no-such-checkpoint") {
		t.Errorf("notice = %q, want it to name the checkpoint that could not be read", lastNotice(m))
	}
}

func TestRewindUnknownActionListsTheRealOnes(t *testing.T) {
	m, _, dir := newRewindFixture(t)
	cp := openCheckpoint(t, dir, filepath.Join(m.cfg.WorkDir, "f.txt"))

	m.applyRewind(cp.ID, []string{"teleport"})

	notice := lastNotice(m)
	if !strings.Contains(notice, "teleport") {
		t.Errorf("notice = %q, want it to name the unknown action", notice)
	}
	if !strings.Contains(notice, "code+conversation") {
		t.Errorf("notice = %q, want it to list the actions that do exist", notice)
	}
}

func TestRewindUnavailableWithoutAManager(t *testing.T) {
	m, _, _ := newRewindFixture(t)
	m.cfg.Checkpoints = nil
	m.handleSlashCommand("/rewind")
	if !strings.Contains(lastNotice(m), "unavailable") {
		t.Errorf("notice = %q, want it to say rewind is unavailable", lastNotice(m))
	}
	if m.rewindPicker != nil {
		t.Error("the picker opened with checkpointing disabled")
	}
}

func TestRewindIsRefusedWhileTheAgentRuns(t *testing.T) {
	// Rewinding mid-turn would restore a file the running turn is about to
	// write again, so the boundary would not mean what the user thinks.
	m, _, dir := newRewindFixture(t)
	cp := openCheckpoint(t, dir, filepath.Join(m.cfg.WorkDir, "f.txt"))
	m.running = true

	m.handleSlashCommand("/rewind")

	if m.rewindPicker != nil {
		t.Error("the picker opened while the agent was running")
	}
	data, _ := os.ReadFile(filepath.Join(m.cfg.WorkDir, "f.txt"))
	if string(data) != "after" {
		t.Errorf("file = %q, want it untouched", data)
	}
	_ = cp
}

func TestRewindPickerOpensAndListsNewestFirst(t *testing.T) {
	m, _, dir := newRewindFixture(t)
	mgr := checkpoint.New()
	// Three checkpoints at known times; the label is what the row shows.
	for i, label := range []string{"one", "two", "three"} {
		if _, err := mgr.Begin(dir, i*2, label); err != nil {
			t.Fatalf("Begin: %v", err)
		}
		// The ID embeds a millisecond timestamp, so distinct turns need
		// distinct stamps to keep the newest-first ordering unambiguous.
		time.Sleep(2 * time.Millisecond)
	}
	m.cfg.Checkpoints = mgr

	m.handleSlashCommand("/rewind")

	if m.rewindPicker == nil {
		t.Fatal("the picker did not open")
	}
	if got := len(m.rewindPicker.list.Items()); got != 3 {
		t.Errorf("picker shows %d rows, want 3", got)
	}
	first, ok := m.rewindPicker.list.Items()[0].(checkpointItem)
	if !ok {
		t.Fatalf("row 0 is %T, want checkpointItem", m.rewindPicker.list.Items()[0])
	}
	if first.Label != "three" {
		t.Errorf("row 0 = %q, want the newest checkpoint %q", first.Label, "three")
	}
}

func TestRewindPickerReportsAnEmptyHistory(t *testing.T) {
	m, _, _ := newRewindFixture(t)
	m.handleSlashCommand("/rewind")
	if m.rewindPicker != nil {
		t.Error("the picker opened with no checkpoints")
	}
	if !strings.Contains(lastNotice(m), "No checkpoints") {
		t.Errorf("notice = %q, want it to explain that there is nothing to rewind to", lastNotice(m))
	}
}

func TestEscEscOpensTheRewindPicker(t *testing.T) {
	m, _, dir := newRewindFixture(t)
	if _, err := checkpoint.New().Begin(dir, 0, "a turn"); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	if m.tryRewindEscape() {
		t.Fatal("the first Esc opened the picker")
	}
	if !m.tryRewindEscape() {
		t.Fatal("the second Esc did not open the picker")
	}
	if m.rewindPicker == nil {
		t.Error("the picker is nil after Esc Esc")
	}
}

func TestEscEscIsIgnoredWhileTyping(t *testing.T) {
	// The spec says "Esc Esc with empty input". A user who typed something and
	// then hit Esc twice meant to dismiss something, not to rewind.
	m, _, dir := newRewindFixture(t)
	if _, err := checkpoint.New().Begin(dir, 0, "a turn"); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	m.inputModel.Text = "half-typed thought"

	m.tryRewindEscape()
	if m.tryRewindEscape() {
		t.Error("Esc Esc rewound with text in the prompt")
	}
	if m.rewindPicker != nil {
		t.Error("the picker opened with text in the prompt")
	}
}

func TestEscEscExpires(t *testing.T) {
	m, _, dir := newRewindFixture(t)
	if _, err := checkpoint.New().Begin(dir, 0, "a turn"); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	m.tryRewindEscape()
	// Pretend the first press was long ago, as a paused terminal would leave it.
	m.lastEsc = time.Now().Add(-2 * escRewindWindow)
	if m.tryRewindEscape() {
		t.Error("a stale Esc still fired the shortcut")
	}
}

func TestEscEscIsDisarmedByAnotherKey(t *testing.T) {
	m, _, dir := newRewindFixture(t)
	if _, err := checkpoint.New().Begin(dir, 0, "a turn"); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	m.tryRewindEscape()
	m.disarmRewindEscape()
	if m.tryRewindEscape() {
		t.Error("the shortcut fired across an unrelated keystroke")
	}
}

func TestSingleEscStillCancelsARunningTurn(t *testing.T) {
	// The regression this guards: making Esc arm a gesture must not stop the
	// one thing a single Esc has always done.
	m, _, _ := newRewindFixture(t)
	m.running = true
	m.agentCancel = func() { m.running = false }

	_, _, handled := m.handleInterruptKey(tea.Key{Code: tea.KeyEsc})
	if !handled {
		t.Fatal("Esc was not handled while the agent was running")
	}
	if m.running {
		t.Error("Esc did not cancel the running turn")
	}
	if m.rewindPicker != nil {
		t.Error("Esc opened the rewind picker while a turn was running")
	}
}

func TestBeginCheckpointRecordsTheEventCountBeforeTheTurn(t *testing.T) {
	// The boundary index must be the count *before* the prompt is appended, or
	// a rewind to this checkpoint would keep the very turn being rewound.
	m, mgr, dir := newRewindFixture(t)
	m.cfg.Checkpoints = mgr
	before, err := m.cfg.SessionService.EventCount(m.cfg.SessionID, agent.AppName, agent.DefaultUserID)
	if err != nil {
		t.Fatalf("EventCount: %v", err)
	}

	m.beginCheckpoint("a new prompt")

	cps, err := mgr.List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(cps) != 1 {
		t.Fatalf("got %d checkpoints, want 1", len(cps))
	}
	if cps[0].EventIndex != before {
		t.Errorf("EventIndex = %d, want %d", cps[0].EventIndex, before)
	}
	if !mgr.Open(dir) {
		t.Error("the checkpoint is not open; the turn's file captures would be dropped")
	}
}

func TestEndCheckpointClosesTheBoundary(t *testing.T) {
	m, mgr, dir := newRewindFixture(t)
	m.cfg.Checkpoints = mgr
	m.beginCheckpoint("a prompt")
	m.endCheckpoint()
	if mgr.Open(dir) {
		t.Error("the checkpoint is still open after the turn ended")
	}
}