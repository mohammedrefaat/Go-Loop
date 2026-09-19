package planner

import (
	"context"
)

// PlanStep represents a single executable action step in a plan.
type PlanStep struct {
	Index       int    `json:"index"`
	Description string `json:"description"`
	Completed   bool   `json:"completed"`
}

// Plan represents an ordered set of task steps.
type Plan struct {
	ID    string     `json:"id"`
	Goal  string     `json:"goal"`
	Steps []PlanStep `json:"steps"`
}

// Planner decomposes user goals into structured plans.
type Planner interface {
	// CreatePlan generates a step-by-step plan for a goal.
	CreatePlan(ctx context.Context, goal string) (*Plan, error)
}
