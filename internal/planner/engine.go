package planner

import (
	"context"
	"fmt"
	"strings"

	"github.com/dimetron/pi-go/internal/models"
	"github.com/dimetron/pi-go/internal/repository"
)

// PlanningEngine manages plan generation, progress tracking, and plan updates.
type PlanningEngine struct {
	provider models.ModelProvider
}

// NewPlanningEngine creates a new PlanningEngine instance.
func NewPlanningEngine(provider models.ModelProvider) *PlanningEngine {
	return &PlanningEngine{provider: provider}
}

// CreatePlan decomposes a high-level goal and repository information into a structured Plan.
func (pe *PlanningEngine) CreatePlan(ctx context.Context, repo repository.Repository, goal string) (*Plan, error) {
	if goal == "" {
		return nil, fmt.Errorf("goal cannot be empty")
	}

	prompt := fmt.Sprintf("Create a step-by-step engineering plan for the goal: %q\nReturn numbered steps.", goal)
	messages := []models.ChatMessage{
		{Role: models.RoleSystem, Content: "You are an expert software engineering planning agent. Decompose tasks into clear, actionable steps."},
		{Role: models.RoleUser, Content: prompt},
	}

	if pe.provider != nil {
		resp, err := pe.provider.Generate(ctx, messages, nil)
		if err == nil && resp != nil && resp.Message.Content != "" {
			lines := strings.Split(resp.Message.Content, "\n")
			var steps []PlanStep
			idx := 1
			for _, l := range lines {
				trimmed := strings.TrimSpace(l)
				if trimmed != "" {
					steps = append(steps, PlanStep{
						Index:       idx,
						Description: trimmed,
						Completed:   false,
					})
					idx++
				}
			}
			return &Plan{
				ID:    "plan-1",
				Goal:  goal,
				Steps: steps,
			}, nil
		}
	}

	// Default fallback decomposition
	return &Plan{
		ID:   "plan-fallback",
		Goal: goal,
		Steps: []PlanStep{
			{Index: 1, Description: "Investigate repository structure and existing implementation", Completed: false},
			{Index: 2, Description: fmt.Sprintf("Implement changes for goal: %s", goal), Completed: false},
			{Index: 3, Description: "Verify changes with unit tests and build checks", Completed: false},
		},
	}, nil
}

// UpdateStepStatus updates the completion status of a specific step in the plan.
func (pe *PlanningEngine) UpdateStepStatus(plan *Plan, stepIndex int, completed bool) error {
	for i := range plan.Steps {
		if plan.Steps[i].Index == stepIndex {
			plan.Steps[i].Completed = completed
			return nil
		}
	}
	return fmt.Errorf("step index %d not found in plan", stepIndex)
}
