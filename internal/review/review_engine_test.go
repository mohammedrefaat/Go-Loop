package review_test

import (
	"context"
	"testing"

	"github.com/dimetron/pi-go/internal/models"
	"github.com/dimetron/pi-go/internal/review"
)

type mockModelProvider struct{}

func (m *mockModelProvider) Name() string { return "mock" }
func (m *mockModelProvider) StreamGenerate(ctx context.Context, messages []models.ChatMessage, opts *models.GenerateOptions, handler func(chunk *models.GenerateResponse) error) error {
	return nil
}
func (m *mockModelProvider) Generate(ctx context.Context, messages []models.ChatMessage, opts *models.GenerateOptions) (*models.GenerateResponse, error) {
	return &models.GenerateResponse{
		Message: models.ChatMessage{
			Role:    models.RoleAssistant,
			Content: "Code changes look good and satisfy Clean Architecture principles.",
		},
	}, nil
}

func TestCodeReviewAgent(t *testing.T) {
	ctx := context.Background()
	provider := &mockModelProvider{}
	agent := review.NewCodeReviewAgent(provider)

	res, err := agent.ReviewDiff(ctx, nil, "diff --git a/main.go b/main.go\n+ func NewFeature() {}")
	if err != nil {
		t.Fatalf("ReviewDiff failed: %v", err)
	}

	if !res.Approved {
		t.Errorf("expected review result to be approved")
	}

	if res.Summary == "" {
		t.Errorf("expected non-empty summary")
	}
}
