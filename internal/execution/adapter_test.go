package execution

import (
	"context"
	"testing"
	"time"
)

func TestOSCommandExecutor(t *testing.T) {
	exec := NewOSCommandExecutor()

	ctx := context.Background()
	res, err := exec.ExecuteCommand(ctx, "go", []string{"version"}, &CommandOptions{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("unexpected error running command: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", res.ExitCode)
	}
	if res.Stdout == "" {
		t.Errorf("expected non-empty output from go version")
	}
}
