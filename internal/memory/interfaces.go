package memory

import (
	"context"
)

// MemoryItem represents a generic memory entry in the core domain.
type MemoryItem struct {
	ID        string         `json:"id"`
	Project   string         `json:"project"`
	Title     string         `json:"title"`
	Text      string         `json:"text"`
	Type      string         `json:"type"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// MemoryQuery specifies parameters for searching stored memory.
type MemoryQuery struct {
	Project string `json:"project"`
	Text    string `json:"text"`
	Limit   int    `json:"limit"`
}

// MemoryStore defines abstract operations for long-term agent memory.
type MemoryStore interface {
	Save(ctx context.Context, item *MemoryItem) error
	Get(ctx context.Context, id string) (*MemoryItem, error)
	Search(ctx context.Context, query MemoryQuery) ([]*MemoryItem, error)
	Delete(ctx context.Context, id string) error
	Close() error
}
