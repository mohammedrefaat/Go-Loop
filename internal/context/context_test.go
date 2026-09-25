package context

import (
	"context"
	"testing"

	"github.com/dimetron/pi-go/internal/models"
)

type mockContextProvider struct{}

func (m *mockContextProvider) AssembleContext(ctx context.Context, sessionID string) ([]models.ChatMessage, error) {
	return []models.ChatMessage{{Role: models.RoleUser, Content: "test"}}, nil
}

func (m *mockContextProvider) SummarizeContext(ctx context.Context, sessionID string) (*ContextSummary, error) {
	return &ContextSummary{TotalTokens: 100, MaxTokens: 1000, EventCount: 2}, nil
}

func (m *mockContextProvider) CompactContext(ctx context.Context, sessionID string, maxTokens int) error {
	return nil
}

func TestContextProviderInterface(t *testing.T) {
	var cp ContextProvider = &mockContextProvider{}

	msgs, err := cp.AssembleContext(context.Background(), "s1")
	if err != nil || len(msgs) != 1 {
		t.Fatalf("unexpected msgs: %v, %v", msgs, err)
	}

	sum, err := cp.SummarizeContext(context.Background(), "s1")
	if err != nil || sum.TotalTokens != 100 {
		t.Fatalf("unexpected summary: %+v, %v", sum, err)
	}

	if err := cp.CompactContext(context.Background(), "s1", 500); err != nil {
		t.Fatalf("unexpected error compacting: %v", err)
	}
}
