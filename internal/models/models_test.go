package models

import (
	"context"
	"testing"
)

func TestTaskStateValidation(t *testing.T) {
	validStates := []TaskState{
		TaskStateCreated,
		TaskStateAnalyzing,
		TaskStateExecuting,
		TaskStateVerifying,
		TaskStateCompleted,
		TaskStateFailed,
	}

	for _, st := range validStates {
		if err := st.Validate(); err != nil {
			t.Errorf("expected valid state for %s, got error: %v", st, err)
		}
	}

	invalid := TaskState("INVALID")
	if err := invalid.Validate(); err == nil {
		t.Errorf("expected error for invalid state, got nil")
	}

	if TaskStateCreated.IsTerminal() {
		t.Errorf("expected CREATED not to be terminal")
	}
	if !TaskStateCompleted.IsTerminal() || !TaskStateFailed.IsTerminal() {
		t.Errorf("expected COMPLETED and FAILED to be terminal")
	}
}

type mockProvider struct{}

func (m *mockProvider) Name() string { return "mock" }
func (m *mockProvider) Generate(ctx context.Context, messages []ChatMessage, opts *GenerateOptions) (*GenerateResponse, error) {
	return &GenerateResponse{
		Message: ChatMessage{Role: RoleAssistant, Content: "pong"},
	}, nil
}
func (m *mockProvider) StreamGenerate(ctx context.Context, messages []ChatMessage, opts *GenerateOptions, handler func(chunk *GenerateResponse) error) error {
	return handler(&GenerateResponse{
		Message: ChatMessage{Role: RoleAssistant, Content: "pong stream"},
	})
}

func TestModelProviderInterface(t *testing.T) {
	var provider ModelProvider = &mockProvider{}
	if provider.Name() != "mock" {
		t.Errorf("unexpected provider name: %s", provider.Name())
	}

	resp, err := provider.Generate(context.Background(), []ChatMessage{{Role: RoleUser, Content: "ping"}}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Message.Content != "pong" {
		t.Errorf("unexpected content: %s", resp.Message.Content)
	}
}
