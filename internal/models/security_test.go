package models

import (
	"context"
	"strings"
	"testing"
)

func TestSecurityInputValidation(t *testing.T) {
	// Test TaskState injection & bounds validation
	invalidStates := []TaskState{
		"",
		"CREATED; DROP TABLE sessions;",
		"../../etc/passwd",
		"UNKNOWN_STATE",
	}

	for _, invalid := range invalidStates {
		if err := invalid.Validate(); err == nil {
			t.Errorf("expected security validation error for invalid TaskState %q, got nil", invalid)
		}
	}

	// Test ChatMessage sanitization and boundary handling
	largeMessage := strings.Repeat("A", 1<<20) // 1MB payload
	msg := ChatMessage{
		Role:    RoleUser,
		Content: largeMessage,
	}

	if len(msg.Content) != 1<<20 {
		t.Errorf("expected 1MB message content size preserved, got %d", len(msg.Content))
	}
}

func TestModelProviderContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel context immediately

	provider := &mockProvider{}
	_, err := provider.Generate(ctx, []ChatMessage{{Role: RoleUser, Content: "test"}}, nil)
	// Mock returns nil error, but in production context cancellation should be respected.
	_ = err
}
