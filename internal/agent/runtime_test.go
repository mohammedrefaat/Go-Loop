package agent_test

import (
	"context"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/models"
	"github.com/dimetron/pi-go/internal/tools"
)

type mockTool struct {
	name string
}

func (m *mockTool) Name() string                     { return m.name }
func (m *mockTool) Description() string              { return "mock description" }
func (m *mockTool) ParameterSchema() map[string]any { return nil }
func (m *mockTool) RiskLevel() tools.RiskLevel       { return tools.RiskLevelLow }

type mockExecutor struct {
	executed bool
}

func (m *mockExecutor) ListTools(ctx context.Context) ([]tools.Tool, error) {
	return []tools.Tool{&mockTool{name: "echo"}}, nil
}

func (m *mockExecutor) Execute(ctx context.Context, name string, args map[string]any) (*tools.ToolExecutionResult, error) {
	m.executed = true
	return &tools.ToolExecutionResult{
		ToolName: name,
		Success:  true,
		Output:   "echo output",
	}, nil
}

type mockProvider struct {
	calls int
}

func (m *mockProvider) Name() string { return "mock" }
func (m *mockProvider) StreamGenerate(ctx context.Context, messages []models.ChatMessage, opts *models.GenerateOptions, handler func(chunk *models.GenerateResponse) error) error {
	return nil
}

func (m *mockProvider) Generate(ctx context.Context, messages []models.ChatMessage, opts *models.GenerateOptions) (*models.GenerateResponse, error) {
	m.calls++
	if m.calls == 1 {
		return &models.GenerateResponse{
			Message: models.ChatMessage{
				Role:    models.RoleAssistant,
				Content: "Executing tool...",
				ToolCalls: []models.ToolCall{
					{ID: "tc-1", Name: "echo", Arguments: `{"text":"hello"}`},
				},
			},
		}, nil
	}
	return &models.GenerateResponse{
		Message: models.ChatMessage{
			Role:    models.RoleAssistant,
			Content: "Task complete.",
		},
	}, nil
}

func TestRuntimeLoop(t *testing.T) {
	ctx := context.Background()
	provider := &mockProvider{}
	executor := &mockExecutor{}

	rl := agent.NewRuntimeLoop(provider, executor, agent.AgentLoopConfig{
		MaxIterations: 5,
		Timeout:       10 * time.Second,
	})

	initial := []models.ChatMessage{
		{Role: models.RoleUser, Content: "Hello agent"},
	}

	state, err := rl.Run(ctx, initial)
	if err != nil {
		t.Fatalf("RuntimeLoop failed: %v", err)
	}

	if !state.Completed {
		t.Errorf("expected state to be completed")
	}

	if !executor.executed {
		t.Errorf("expected tool executor to be executed")
	}

	if provider.calls != 2 {
		t.Errorf("expected 2 provider calls, got %d", provider.calls)
	}
}
