package planner

import (
	"context"
	"testing"
)

type mockPlanner struct{}

func (m *mockPlanner) CreatePlan(ctx context.Context, goal string) (*Plan, error) {
	return &Plan{ID: "p1", Goal: goal, Steps: []PlanStep{{Index: 1, Description: "step 1"}}}, nil
}

func TestPlannerInterface(t *testing.T) {
	var p Planner = &mockPlanner{}
	plan, err := p.CreatePlan(context.Background(), "build feature")
	if err != nil || plan.ID != "p1" {
		t.Fatalf("unexpected plan: %+v, %v", plan, err)
	}
}
