package models

import (
	"context"
	"fmt"
	"testing"
)

func BenchmarkTaskStateValidate(b *testing.B) {
	state := TaskStateExecuting
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := state.Validate(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChatMessageAllocation(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ChatMessage{
			Role:    RoleUser,
			Content: "Execute task with clean domain interface and SOLID architecture",
			ToolCalls: []ToolCall{
				{
					ID:        fmt.Sprintf("call_%d", i),
					Name:      "read_file",
					Arguments: `{"path": "main.go"}`,
				},
			},
		}
	}
}

func BenchmarkModelProviderGenerateMock(b *testing.B) {
	provider := &mockProvider{}
	ctx := context.Background()
	messages := []ChatMessage{
		{Role: RoleUser, Content: "Hello world"},
	}
	opts := &GenerateOptions{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := provider.Generate(ctx, messages, opts)
		if err != nil {
			b.Fatal(err)
		}
	}
}
