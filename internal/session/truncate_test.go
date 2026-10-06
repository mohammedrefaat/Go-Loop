package session

import (
	"context"
	"testing"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// newSessionServiceAt builds a FileService over an existing sessions directory.
func newSessionServiceAt(t *testing.T, dir string) *FileService {
	t.Helper()
	svc, err := NewFileService(dir)
	if err != nil {
		t.Fatalf("NewFileService: %v", err)
	}
	return svc
}

func newSessionService(t *testing.T) *FileService {
	t.Helper()
	return newSessionServiceAt(t, t.TempDir())
}

// newSeededSession builds a FileService over a temp dir with one session holding
// `rounds` user/assistant pairs. The truncate primitive is about an exact
// boundary in a real event list, so the events are written through the normal
// AppendEvent path rather than poked into the file by hand.
func newSeededSession(t *testing.T, sessionID string, rounds int) *FileService {
	t.Helper()
	svc := newSessionService(t)
	if _, err := svc.Create(context.Background(), &session.CreateRequest{
		AppName: "pi", UserID: "user", SessionID: sessionID,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := svc.Get(context.Background(), &session.GetRequest{
		AppName: "pi", UserID: "user", SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for i := range rounds {
		_ = svc.AppendEvent(context.Background(), got.Session, &session.Event{
			Author:  "user",
			Content: genai.NewContentFromText("q", genai.RoleUser),
			Actions: session.EventActions{},
		})
		_ = svc.AppendEvent(context.Background(), got.Session, &session.Event{
			Author:  "model",
			Content: genai.NewContentFromText("a", genai.RoleModel),
			Actions: session.EventActions{},
		})
		_ = i
	}
	return svc
}

func eventTexts(t *testing.T, svc *FileService, sessionID string) []string {
	t.Helper()
	resp, err := svc.Get(context.Background(), &session.GetRequest{
		AppName: "pi", UserID: "user", SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	var out []string
	for ev := range resp.Session.Events().All() {
		if ev.Content == nil || len(ev.Content.Parts) == 0 {
			continue
		}
		out = append(out, ev.Content.Parts[0].Text)
	}
	return out
}

func TestTruncateEventsDropsEverythingAfterTheIndex(t *testing.T) {
	svc := newSeededSession(t, "sess-trunc", 3) // 3 pairs = 6 events

	if err := svc.TruncateEvents("sess-trunc", "pi", "user", 2); err != nil {
		t.Fatalf("TruncateEvents: %v", err)
	}

	got := eventTexts(t, svc, "sess-trunc")
	if len(got) != 2 {
		t.Fatalf("got %d events after truncating to 2, want 2", len(got))
	}
	if n, err := svc.EventCount("sess-trunc", "pi", "user"); err != nil || n != 2 {
		t.Errorf("EventCount() = %d/%v, want 2/nil", n, err)
	}
}

func TestTruncateEventsSurvivesAReload(t *testing.T) {
	// The in-memory slice and the file must agree, or the rewind is invisible
	// until the process restarts — which is exactly when the user has stopped
	// looking at it.
	dir := t.TempDir()
	svc := newSessionServiceAt(t, dir)
	if _, err := svc.Create(context.Background(), &session.CreateRequest{
		AppName: "pi", UserID: "user", SessionID: "sess-reload",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	resp, _ := svc.Get(context.Background(), &session.GetRequest{
		AppName: "pi", UserID: "user", SessionID: "sess-reload",
	})
	for i := range 4 {
		_ = svc.AppendEvent(context.Background(), resp.Session, &session.Event{
			Author:  "user",
			Content: genai.NewContentFromText(string(rune('a'+i)), genai.RoleUser),
		})
	}
	if err := svc.TruncateEvents("sess-reload", "pi", "user", 2); err != nil {
		t.Fatalf("TruncateEvents: %v", err)
	}

	fresh := newSessionServiceAt(t, dir)
	got := eventTexts(t, fresh, "sess-reload")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("after reload got %q, want [a b] — the on-disk rewrite must match the in-memory slice", got)
	}
}

func TestTruncateEventsClampsAnOutOfRangeIndex(t *testing.T) {
	// A checkpoint taken before a /clear, or one whose events were compacted
	// away, names an index past the end. Clamping to the current length keeps
	// everything: the conversation is already at or before that boundary, so
	// there is nothing after it to drop — and emptying a session because a
	// checkpoint outlived its events would be the destructive reading.
	svc := newSeededSession(t, "sess-clamp", 2) // 4 events

	if err := svc.TruncateEvents("sess-clamp", "pi", "user", 9999); err != nil {
		t.Fatalf("TruncateEvents with an out-of-range index = %v, want nil", err)
	}
	if n, err := svc.EventCount("sess-clamp", "pi", "user"); err != nil || n != 4 {
		t.Errorf("EventCount() = %d/%v, want 4/nil — an index past the end keeps everything", n, err)
	}
}

func TestTruncateEventsHandlesZeroAndNegative(t *testing.T) {
	svc := newSeededSession(t, "sess-zero", 2)
	for _, keep := range []int{0, -5} {
		if err := svc.TruncateEvents("sess-zero", "pi", "user", keep); err != nil {
			t.Errorf("TruncateEvents(%d) = %v, want nil", keep, err)
		}
		if n, _ := svc.EventCount("sess-zero", "pi", "user"); n != 0 {
			t.Errorf("after TruncateEvents(%d) the session holds %d events, want 0", keep, n)
		}
	}
}

func TestTruncateEventsAtTheEndIsANoOp(t *testing.T) {
	svc := newSeededSession(t, "sess-noop", 2)
	n, err := svc.EventCount("sess-noop", "pi", "user")
	if err != nil {
		t.Fatalf("EventCount: %v", err)
	}
	if err := svc.TruncateEvents("sess-noop", "pi", "user", n); err != nil {
		t.Errorf("TruncateEvents at the current length = %v, want nil", err)
	}
	if got := eventTexts(t, svc, "sess-noop"); len(got) != 4 {
		t.Errorf("got %d events, want all 4 kept", len(got))
	}
}

func TestTruncateEventsKeepsTheSessionUsableAfterwards(t *testing.T) {
	// The next turn must append onto the truncated history, not into the
	// dropped tail — a stale in-memory length would put the new event at the
	// old index and silently corrupt every later rewind boundary.
	svc := newSeededSession(t, "sess-append", 3)
	if err := svc.TruncateEvents("sess-append", "pi", "user", 2); err != nil {
		t.Fatalf("TruncateEvents: %v", err)
	}
	resp, _ := svc.Get(context.Background(), &session.GetRequest{
		AppName: "pi", UserID: "user", SessionID: "sess-append",
	})
	if err := svc.AppendEvent(context.Background(), resp.Session, &session.Event{
		Author:  "user",
		Content: genai.NewContentFromText("new", genai.RoleUser),
	}); err != nil {
		t.Fatalf("AppendEvent after truncate: %v", err)
	}
	if n, _ := svc.EventCount("sess-append", "pi", "user"); n != 3 {
		t.Errorf("EventCount() = %d, want 3", n)
	}
}

func TestTruncateEventsRejectsAnUnknownSession(t *testing.T) {
	svc := newSessionService(t)
	if err := svc.TruncateEvents("nope", "pi", "user", 1); err == nil {
		t.Error("TruncateEvents on an unknown session = nil, want an error")
	}
}

func TestEventCountOnAnUnknownSession(t *testing.T) {
	svc := newSessionService(t)
	if _, err := svc.EventCount("nope", "pi", "user"); err == nil {
		t.Error("EventCount on an unknown session = nil, want an error")
	}
}