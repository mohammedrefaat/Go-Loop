package tools

import (
	"context"
	"testing"
)

func TestRiskLevelValidation(t *testing.T) {
	levels := []RiskLevel{
		RiskLevelLow,
		RiskLevelMedium,
		RiskLevelHigh,
		RiskLevelCritical,
	}

	for _, l := range levels {
		if err := l.Validate(); err != nil {
			t.Errorf("expected valid risk level for %s, got %v", l, err)
		}
	}

	invalid := RiskLevel("SUPER_DANGEROUS")
	if err := invalid.Validate(); err == nil {
		t.Errorf("expected error for invalid risk level, got nil")
	}
}

type mockTool struct{}

func (m *mockTool) Name() string                { return "read_file" }
func (m *mockTool) Description() string         { return "reads file content" }
func (m *mockTool) ParameterSchema() map[string]any { return map[string]any{"type": "object"} }
func (m *mockTool) RiskLevel() RiskLevel       { return RiskLevelLow }

type mockToolExecutor struct {
	tools []Tool
}

func (m *mockToolExecutor) Execute(ctx context.Context, name string, args map[string]any) (*ToolExecutionResult, error) {
	return &ToolExecutionResult{
		ToolName: name,
		Success:  true,
		Output:   "content",
	}, nil
}

func (m *mockToolExecutor) ListTools(ctx context.Context) ([]Tool, error) {
	return m.tools, nil
}

func TestToolInterfaces(t *testing.T) {
	mt := &mockTool{}
	exec := &mockToolExecutor{tools: []Tool{mt}}

	tools, err := exec.ListTools(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 1 || tools[0].Name() != "read_file" {
		t.Fatalf("unexpected tools list: %v", tools)
	}

	res, err := exec.Execute(context.Background(), "read_file", map[string]any{"path": "foo.go"})
	if err != nil {
		t.Fatalf("unexpected error executing tool: %v", err)
	}
	if !res.Success || res.Output != "content" {
		t.Errorf("unexpected result: %+v", res)
	}
}
