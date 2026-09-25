package execution

import (
	"context"
	"testing"
	"time"
)

type mockExec struct{}

func (m *mockExec) ExecuteCommand(ctx context.Context, command string, args []string, opts *CommandOptions) (*CommandResult, error) {
	return &CommandResult{
		ExitCode: 0,
		Stdout:   "ok",
		Stderr:   "",
		Duration: 10 * time.Millisecond,
	}, nil
}

func TestCommandExecutorInterface(t *testing.T) {
	var exec CommandExecutor = &mockExec{}

	res, err := exec.ExecuteCommand(context.Background(), "echo", []string{"hello"}, &CommandOptions{WorkDir: "/tmp"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ExitCode != 0 || res.Stdout != "ok" {
		t.Errorf("unexpected command result: %+v", res)
	}
}
