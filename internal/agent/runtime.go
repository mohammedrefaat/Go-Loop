package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dimetron/pi-go/internal/models"
	"github.com/dimetron/pi-go/internal/tools"
)

// AgentLoopConfig configures the execution parameters of the agent runtime loop.
type AgentLoopConfig struct {
	MaxIterations int           `json:"maxIterations"`
	Timeout       time.Duration `json:"timeout"`
}

// AgentLoopState tracks iteration state during runtime loop execution.
type AgentLoopState struct {
	Iteration int                  `json:"iteration"`
	History   []models.ChatMessage `json:"history"`
	Completed bool                 `json:"completed"`
	Error     error                `json:"error,omitempty"`
}

// RuntimeLoop coordinates interaction between LLM model provider and tool execution.
type RuntimeLoop struct {
	provider models.ModelProvider
	executor tools.ToolExecutor
	config   AgentLoopConfig
}

// NewRuntimeLoop creates a new RuntimeLoop instance.
func NewRuntimeLoop(provider models.ModelProvider, executor tools.ToolExecutor, config AgentLoopConfig) *RuntimeLoop {
	if config.MaxIterations <= 0 {
		config.MaxIterations = 10
	}
	if config.Timeout <= 0 {
		config.Timeout = 5 * time.Minute
	}
	return &RuntimeLoop{
		provider: provider,
		executor: executor,
		config:   config,
	}
}

// Run executes the agent loop until task completion or iteration limit.
func (rl *RuntimeLoop) Run(ctx context.Context, initialMessages []models.ChatMessage) (*AgentLoopState, error) {
	ctx, cancel := context.WithTimeout(ctx, rl.config.Timeout)
	defer cancel()

	state := &AgentLoopState{
		Iteration: 0,
		History:   append([]models.ChatMessage{}, initialMessages...),
	}

	for state.Iteration < rl.config.MaxIterations {
		select {
		case <-ctx.Done():
			state.Error = ctx.Err()
			return state, ctx.Err()
		default:
		}

		state.Iteration++

		// Get available tools from executor
		toolDefs, err := rl.executor.ListTools(ctx)
		if err != nil {
			state.Error = fmt.Errorf("failed to list tools: %w", err)
			return state, state.Error
		}

		var modelTools []models.ToolDefinition
		for _, t := range toolDefs {
			modelTools = append(modelTools, models.ToolDefinition{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.ParameterSchema(),
			})
		}

		// Call model provider
		opts := &models.GenerateOptions{Tools: modelTools}
		resp, err := rl.provider.Generate(ctx, state.History, opts)
		if err != nil {
			state.Error = fmt.Errorf("provider generation failed: %w", err)
			return state, state.Error
		}

		// Append model response message to history
		state.History = append(state.History, resp.Message)

		// If no tool calls requested, loop is complete
		if len(resp.Message.ToolCalls) == 0 {
			state.Completed = true
			return state, nil
		}

		// Execute tool calls sequentially
		for _, tc := range resp.Message.ToolCalls {
			var args map[string]any
			if tc.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Arguments), &args)
			}
			result, execErr := rl.executor.Execute(ctx, tc.Name, args)
			output := ""
			if execErr != nil {
				output = fmt.Sprintf("Error executing %s: %v", tc.Name, execErr)
			} else if result != nil {
				if result.Success {
					output = result.Output
				} else {
					output = fmt.Sprintf("Tool failed: %s", result.Error)
				}
			}

			// Add tool result to history
			state.History = append(state.History, models.ChatMessage{
				Role:    models.RoleTool,
				ToolID:  tc.ID,
				Name:    tc.Name,
				Content: output,
			})
		}
	}

	state.Error = fmt.Errorf("exceeded max iterations (%d)", rl.config.MaxIterations)
	return state, state.Error
}
