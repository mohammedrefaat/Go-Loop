package execution

import (
	"context"
	"time"
)

// CommandOptions configures command execution parameters.
type CommandOptions struct {
	WorkDir string            `json:"workDir,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Timeout time.Duration     `json:"timeout,omitempty"`
	Stdin   string            `json:"stdin,omitempty"`
}

// CommandResult represents the outcome of executing a shell command.
type CommandResult struct {
	ExitCode int           `json:"exitCode"`
	Stdout   string        `json:"stdout"`
	Stderr   string        `json:"stderr"`
	Duration time.Duration `json:"duration"`
}

// CommandExecutor abstracts OS shell/binary execution.
type CommandExecutor interface {
	// ExecuteCommand runs a command with the given name, arguments, and execution options.
	ExecuteCommand(ctx context.Context, command string, args []string, opts *CommandOptions) (*CommandResult, error)
}
