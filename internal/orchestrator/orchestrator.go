package orchestrator

import (
	"context"

	"github.com/dimetron/pi-go/internal/models"
)

// Orchestrator coordinates agent workflow execution across planning, execution, and verification.
type Orchestrator interface {
	// ExecuteTask runs a complete task lifecycle given an input user prompt.
	ExecuteTask(ctx context.Context, prompt string) (models.TaskState, error)
}
