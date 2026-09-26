package planner_test

import (
	"context"
	"testing"

	"github.com/dimetron/pi-go/internal/models"
	"github.com/dimetron/pi-go/internal/planner"
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
			Content: "1. Inspect codebase\n2. Modify target files\n3. Run tests",
		},
	}, nil
}

func TestPlanningEngine(t *testing.T) {
	ctx := context.Background()
	provider := &mockModelProvider{}
	engine := planner.NewPlanningEngine(provider)

	plan, err := engine.CreatePlan(ctx, nil, "Add new API endpoint")
	if err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}

	if len(plan.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(plan.Steps))
	}

	err = engine.UpdateStepStatus(plan, 1, true)
	if err != nil {
		t.Fatalf("UpdateStepStatus failed: %v", err)
	}

	if !plan.Steps[0].Completed {
		t.Errorf("expected step 1 to be marked as completed")
	}
}
