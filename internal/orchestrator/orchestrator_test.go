package orchestrator

import (
	"context"
	"testing"

	"github.com/dimetron/pi-go/internal/models"
)

type mockOrchestrator struct{}

func (m *mockOrchestrator) ExecuteTask(ctx context.Context, prompt string) (models.TaskState, error) {
	return models.TaskStateCompleted, nil
}

func TestOrchestratorInterface(t *testing.T) {
	var o Orchestrator = &mockOrchestrator{}
	st, err := o.ExecuteTask(context.Background(), "do work")
	if err != nil || st != models.TaskStateCompleted {
		t.Fatalf("unexpected result: %s, %v", st, err)
	}
}
