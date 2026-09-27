package verification_test

import (
	"context"
	"testing"

	"github.com/dimetron/pi-go/internal/execution"
	"github.com/dimetron/pi-go/internal/verification"
)

type mockExec struct {
	failCount int
}

func (m *mockExec) ExecuteCommand(ctx context.Context, command string, args []string, opts *execution.CommandOptions) (*execution.CommandResult, error) {
	if m.failCount > 0 {
		m.failCount--
		return &execution.CommandResult{
			ExitCode: 1,
			Stderr:   "compilation error",
		}, nil
	}
	return &execution.CommandResult{
		ExitCode: 0,
		Stdout:   "ok",
	}, nil
}

func TestDefaultVerifierAndSelfHealing(t *testing.T) {
	ctx := context.Background()
	cmdExec := &mockExec{failCount: 1}
	verifier := verification.NewDefaultVerifier(cmdExec)
	healing := verification.NewSelfHealingLoop(verifier, 3)

	res, err := healing.Run(ctx, func(ctx context.Context, failResult *verification.VerificationResult) error {
		// Mock fixing action
		return nil
	})

	if err != nil {
		t.Fatalf("SelfHealingLoop expected success on second try, got err: %v", err)
	}

	if !res.Passed {
		t.Errorf("expected verification result to pass after self-healing")
	}
}
