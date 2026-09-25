package session

import (
	"context"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/models"
)

type mockSessionStore struct {
	sessions map[string]*SessionData
}

func (m *mockSessionStore) CreateSession(ctx context.Context, session *SessionData) error {
	m.sessions[session.ID] = session
	return nil
}

func (m *mockSessionStore) GetSession(ctx context.Context, sessionID string) (*SessionData, error) {
	return m.sessions[sessionID], nil
}

func (m *mockSessionStore) UpdateSession(ctx context.Context, session *SessionData) error {
	m.sessions[session.ID] = session
	return nil
}

func (m *mockSessionStore) DeleteSession(ctx context.Context, sessionID string) error {
	delete(m.sessions, sessionID)
	return nil
}

func (m *mockSessionStore) ListSessions(ctx context.Context, appName, userID string) ([]*SessionData, error) {
	var res []*SessionData
	for _, s := range m.sessions {
		res = append(res, s)
	}
	return res, nil
}

func TestSessionStoreInterface(t *testing.T) {
	store := &mockSessionStore{sessions: make(map[string]*SessionData)}
	var ss SessionStore = store

	sData := &SessionData{
		ID:        "s1",
		AppName:   "pi",
		UserID:    "u1",
		State:     models.TaskStateCreated,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := ss.CreateSession(context.Background(), sData); err != nil {
		t.Fatalf("unexpected error creating session: %v", err)
	}

	got, err := ss.GetSession(context.Background(), "s1")
	if err != nil || got == nil || got.ID != "s1" {
		t.Fatalf("unexpected session returned: %+v, %v", got, err)
	}
}
