package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/session"
)

// newSessionModel builds a model wired to a real FileService, the same setup
// the branch-command tests use. Session commands touch the filesystem, so a
// real service on a temp dir is the only honest fixture.
func newSessionModel(t *testing.T, sessionID string) (*model, *session.FileService) {
	t.Helper()

	svc, err := session.NewFileService(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(context.Background(), newCreateReq(sessionID)); err != nil {
		t.Fatal(err)
	}

	return &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			SessionService: svc,
			SessionID:      sessionID,
		},
	}, svc
}

// --- /rename ---

func TestHandleRenameCommand(t *testing.T) {
	m, svc := newSessionModel(t, "test-sess")

	m.handleRenameCommand([]string{"my", "session"})

	title, err := svc.GetSessionTitle("test-sess")
	if err != nil {
		t.Fatal(err)
	}
	if title != "my session" {
		t.Errorf("title = %q, want %q", title, "my session")
	}
	if len(m.chatModel.Messages) != 1 || !m.chatModel.Messages[0].isMeta {
		t.Fatalf("expected 1 meta notice, got %+v", m.chatModel.Messages)
	}
}

func TestHandleRenameCommand_EmptyClearsTitle(t *testing.T) {
	m, svc := newSessionModel(t, "test-sess")
	m.handleRenameCommand([]string{"first"})
	m.chatModel.Messages = nil

	m.handleRenameCommand(nil)

	title, err := svc.GetSessionTitle("test-sess")
	if err != nil {
		t.Fatal(err)
	}
	if title != "" {
		t.Errorf("title = %q, want empty", title)
	}
	if len(m.chatModel.Messages) != 1 || !strings.Contains(m.chatModel.Messages[0].content, "cleared") {
		t.Errorf("expected a cleared notice, got %+v", m.chatModel.Messages)
	}
}

func TestHandleRenameCommand_NoService(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:       Config{SessionID: "test-sess"},
	}

	m.handleRenameCommand([]string{"x"})

	if len(m.chatModel.Messages) != 1 || !strings.Contains(m.chatModel.Messages[0].content, "unavailable") {
		t.Fatalf("expected an unavailable notice, got %+v", m.chatModel.Messages)
	}
}

// A title is echoed into the terminal title escape and into JSONL metadata, so
// control characters in it corrupt both.
func TestSanitizeSessionName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"strips newline", "bad\ntitle", "bad title"},
		{"strips escape", "bad\x1b[31mtitle", "bad [31mtitle"},
		{"strips del", "bad\x7ftitle", "bad title"},
		{"collapses whitespace", "  a   b  ", "a b"},
		{"caps length", strings.Repeat("x", 300), strings.Repeat("x", 200)},
		{"keeps normal text", "release notes", "release notes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeSessionName(tt.in); got != tt.want {
				t.Errorf("sanitizeSessionName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// --- /export ---

func TestHandleExportCommand_ExplicitPath(t *testing.T) {
	m, _ := newSessionModel(t, "test-sess")
	m.chatModel.Messages = append(m.chatModel.Messages,
		message{role: "user", content: "hello"},
		message{role: "assistant", content: "hi there"},
	)

	path := filepath.Join(t.TempDir(), "nested", "out.txt")
	m.handleExportCommand([]string{path})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("export file not written: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "## User") || !strings.Contains(text, "hello") {
		t.Errorf("transcript missing the user turn:\n%s", text)
	}
	if !strings.Contains(text, "## Assistant") || !strings.Contains(text, "hi there") {
		t.Errorf("transcript missing the assistant turn:\n%s", text)
	}
}

func TestHandleExportCommand_DefaultPathLandsInSessionDir(t *testing.T) {
	m, svc := newSessionModel(t, "test-sess")
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "user", content: "hello"})

	m.handleExportCommand(nil)

	dir := svc.SessionDir("test-sess")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "transcript-") && strings.HasSuffix(e.Name(), ".txt") {
			found = true
		}
	}
	if !found {
		t.Errorf("no transcript-*.txt written to %s", dir)
	}
}

func TestHandleExportCommand_EmptyConversation(t *testing.T) {
	m, _ := newSessionModel(t, "test-sess")

	m.handleExportCommand([]string{filepath.Join(t.TempDir(), "out.txt")})

	if len(m.chatModel.Messages) != 1 || !strings.Contains(m.chatModel.Messages[0].content, "empty") {
		t.Fatalf("expected an empty-conversation notice, got %+v", m.chatModel.Messages)
	}
	if _, err := os.Stat(filepath.Join(t.TempDir(), "out.txt")); err == nil {
		t.Error("export wrote a file for an empty conversation")
	}
}

// Errors and tool chatter are noise in an exported transcript.
func TestRenderTranscriptText_SkipsMetaAndToolMessages(t *testing.T) {
	m, _ := newSessionModel(t, "test-sess")
	m.chatModel.Messages = []message{
		{role: "user", content: "question"},
		{role: "assistant", content: "", preRendered: true},
		{role: "assistant", content: "notice", isMeta: true},
		{role: "assistant", content: "boom", isError: true},
		{role: "assistant", content: "careful", isWarning: true},
		{role: "assistant", content: "answer"},
	}

	text := m.renderTranscriptText()

	if !strings.Contains(text, "question") || !strings.Contains(text, "answer") {
		t.Errorf("transcript missing real turns:\n%s", text)
	}
	for _, unwanted := range []string{"notice", "boom", "careful"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("transcript contains %q, which should be filtered:\n%s", unwanted, text)
		}
	}
}

// --- /resume ---

func TestOpenSessionPicker_ExcludesCurrentSession(t *testing.T) {
	m, svc := newSessionModel(t, "current")
	if _, err := svc.Create(context.Background(), newCreateReq("older")); err != nil {
		t.Fatal(err)
	}

	m.openSessionPicker()

	if m.sessionPicker == nil {
		t.Fatal("expected the picker to open")
	}
	for _, item := range m.sessionPicker.list.Items() {
		if item.(MenuItem).Value() == "current" {
			t.Error("picker offers the session already in use")
		}
	}
	if got := len(m.sessionPicker.list.Items()); got != 1 {
		t.Errorf("picker has %d items, want 1", got)
	}
}

func TestOpenSessionPicker_OnlyCurrentSession(t *testing.T) {
	m, _ := newSessionModel(t, "only")

	m.openSessionPicker()

	if m.sessionPicker != nil {
		t.Error("picker opened with nothing to resume")
	}
	if len(m.chatModel.Messages) != 1 || !strings.Contains(m.chatModel.Messages[0].content, "No other sessions") {
		t.Errorf("expected a no-sessions notice, got %+v", m.chatModel.Messages)
	}
}

func TestResumeSessionID_SwitchesSession(t *testing.T) {
	m, svc := newSessionModel(t, "current")
	if _, err := svc.Create(context.Background(), newCreateReq("target")); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetSessionTitle("target", "the other one"); err != nil {
		t.Fatal(err)
	}
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "user", content: "old turn"})

	m.resumeSessionID("target")

	if m.cfg.SessionID != "target" {
		t.Errorf("session ID = %q, want %q", m.cfg.SessionID, "target")
	}
	if m.sessionTitle != "the other one" {
		t.Errorf("session title = %q, want %q", m.sessionTitle, "the other one")
	}
	if len(m.chatModel.Messages) != 1 || !m.chatModel.Messages[0].isMeta {
		t.Errorf("transcript was not cleared down to the resume notice: %+v", m.chatModel.Messages)
	}
}

func TestResumeSessionID_UnknownID(t *testing.T) {
	m, _ := newSessionModel(t, "current")

	m.resumeSessionID("nope")

	if m.cfg.SessionID != "current" {
		t.Errorf("session changed to %q despite an unknown target", m.cfg.SessionID)
	}
	if len(m.chatModel.Messages) != 1 || !strings.Contains(m.chatModel.Messages[0].content, "nope") {
		t.Errorf("expected a no-such-session notice, got %+v", m.chatModel.Messages)
	}
}

func TestResumeSessionID_SameSession(t *testing.T) {
	m, _ := newSessionModel(t, "current")

	m.resumeSessionID("current")

	if len(m.chatModel.Messages) != 1 || !strings.Contains(m.chatModel.Messages[0].content, "Already in this session") {
		t.Errorf("expected an already-active notice, got %+v", m.chatModel.Messages)
	}
}
