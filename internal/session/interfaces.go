package session

import (
	"context"
	"time"

	"github.com/dimetron/pi-go/internal/models"
)

// SessionData represents session state and history in the domain layer.
type SessionData struct {
	ID        string               `json:"id"`
	AppName   string               `json:"appName"`
	UserID    string               `json:"userID"`
	WorkDir   string               `json:"workDir"`
	Title     string               `json:"title"`
	State     models.TaskState     `json:"state"`
	Messages  []models.ChatMessage `json:"messages"`
	CreatedAt time.Time            `json:"createdAt"`
	UpdatedAt time.Time            `json:"updatedAt"`
}

// SessionStore defines domain persistence interface for session lifecycle management.
type SessionStore interface {
	CreateSession(ctx context.Context, session *SessionData) error
	GetSession(ctx context.Context, sessionID string) (*SessionData, error)
	UpdateSession(ctx context.Context, session *SessionData) error
	DeleteSession(ctx context.Context, sessionID string) error
	ListSessions(ctx context.Context, appName, userID string) ([]*SessionData, error)
}
