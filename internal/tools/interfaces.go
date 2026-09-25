package tools

import (
	"context"
	"fmt"
)

// RiskLevel categorizes tools by safety/permission level.
type RiskLevel string

const (
	RiskLevelLow      RiskLevel = "LOW"
	RiskLevelMedium   RiskLevel = "MEDIUM"
	RiskLevelHigh     RiskLevel = "HIGH"
	RiskLevelCritical RiskLevel = "CRITICAL"
)

// Validate checks whether the RiskLevel is a recognized value.
func (r RiskLevel) Validate() error {
	switch r {
	case RiskLevelLow, RiskLevelMedium, RiskLevelHigh, RiskLevelCritical:
		return nil
	default:
		return fmt.Errorf("invalid risk level: %q", r)
	}
}

// Tool defines the core domain abstraction for a tool capability.
type Tool interface {
	// Name returns the unique tool identifier.
	Name() string

	// Description returns a human/LLM readable summary of the tool.
	Description() string

	// ParameterSchema returns JSON schema of expected arguments.
	ParameterSchema() map[string]any

	// RiskLevel returns the risk classification for execution policies.
	RiskLevel() RiskLevel
}

// ToolExecutionResult represents the output of running a tool.
type ToolExecutionResult struct {
	ToolName string         `json:"toolName"`
	Success  bool           `json:"success"`
	Output   string         `json:"output,omitempty"`
	Error    string         `json:"error,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// ToolExecutor executes tools given a tool name and input arguments.
type ToolExecutor interface {
	// Execute executes a named tool with context and input arguments.
	Execute(ctx context.Context, name string, args map[string]any) (*ToolExecutionResult, error)

	// ListTools returns all registered tools.
	ListTools(ctx context.Context) ([]Tool, error)
}
