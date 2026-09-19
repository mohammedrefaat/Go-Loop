package models

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// TaskState represents the explicit lifecycle states of a task.
type TaskState string

const (
	TaskStateCreated   TaskState = "CREATED"
	TaskStateAnalyzing TaskState = "ANALYZING"
	TaskStateExecuting TaskState = "EXECUTING"
	TaskStateVerifying TaskState = "VERIFYING"
	TaskStateCompleted TaskState = "COMPLETED"
	TaskStateFailed    TaskState = "FAILED"
)

// IsTerminal returns true if the task state is terminal (completed or failed).
func (s TaskState) IsTerminal() bool {
	return s == TaskStateCompleted || s == TaskStateFailed
}

// String returns string representation of TaskState.
func (s TaskState) String() string {
	return string(s)
}

// Validate checks if the TaskState is a known valid state.
func (s TaskState) Validate() error {
	switch s {
	case TaskStateCreated, TaskStateAnalyzing, TaskStateExecuting, TaskStateVerifying, TaskStateCompleted, TaskStateFailed:
		return nil
	default:
		return fmt.Errorf("invalid task state: %q", s)
	}
}

// Role defines message author roles in conversations.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ChatMessage represents a prompt message in the core domain without third-party types.
type ChatMessage struct {
	Role      Role           `json:"role"`
	Content   string         `json:"content,omitempty"`
	ToolCalls []ToolCall     `json:"toolCalls,omitempty"`
	ToolID    string         `json:"toolId,omitempty"`
	Name      string         `json:"name,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// ToolCall represents a requested tool invocation.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDefinition describes a tool capability for the model.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// GenerateOptions contains parameters for model generation.
type GenerateOptions struct {
	Temperature     *float64         `json:"temperature,omitempty"`
	TopP            *float64         `json:"topP,omitempty"`
	MaxTokens       *int             `json:"maxTokens,omitempty"`
	StopSequences   []string         `json:"stopSequences,omitempty"`
	Tools           []ToolDefinition `json:"tools,omitempty"`
	ThinkingLevel   string           `json:"thinkingLevel,omitempty"`
	ExtraParameters map[string]any   `json:"extraParameters,omitempty"`
}

// TokenUsage reports resource consumption for a generation request.
type TokenUsage struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
	TotalTokens      int `json:"totalTokens"`
}

// GenerateResponse represents the result of an LLM call.
type GenerateResponse struct {
	Message      ChatMessage   `json:"message"`
	FinishReason string        `json:"finishReason"`
	Usage        TokenUsage    `json:"usage"`
	Duration     time.Duration `json:"duration"`
}

// ErrInvalidModelRequest is returned when request arguments are invalid.
var ErrInvalidModelRequest = errors.New("invalid model request")

// ModelProvider abstracts LLM capabilities (chat and tool calling) using standard Go types.
type ModelProvider interface {
	// Name returns the provider/model identifier (e.g. "openai/gpt-4o", "anthropic/claude-3-5-sonnet").
	Name() string

	// Generate generates a completion for the given messages and options.
	Generate(ctx context.Context, messages []ChatMessage, opts *GenerateOptions) (*GenerateResponse, error)

	// StreamGenerate generates a streaming completion, sending chunks to the provided handler.
	StreamGenerate(ctx context.Context, messages []ChatMessage, opts *GenerateOptions, handler func(chunk *GenerateResponse) error) error
}
