package context

import (
	"context"

	"github.com/dimetron/pi-go/internal/models"
)

// ContextSummary provides context metadata and token budget estimates.
type ContextSummary struct {
	TotalTokens int `json:"totalTokens"`
	MaxTokens   int `json:"maxTokens"`
	EventCount  int `json:"eventCount"`
}

// ContextProvider manages prompt context assembly and context window management.
type ContextProvider interface {
	// AssembleContext builds the list of chat messages for an LLM prompt.
	AssembleContext(ctx context.Context, sessionID string) ([]models.ChatMessage, error)

	// SummarizeContext returns token window metrics for a session.
	SummarizeContext(ctx context.Context, sessionID string) (*ContextSummary, error)

	// CompactContext compacts session prompt context to stay within token budgets.
	CompactContext(ctx context.Context, sessionID string, maxTokens int) error
}
