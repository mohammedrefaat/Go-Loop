package verification

import (
	"context"
	"fmt"

	"github.com/dimetron/pi-go/internal/execution"
)

// DefaultVerifier runs build, test, lint, and formatting verification using command execution.
type DefaultVerifier struct {
	cmdExec execution.CommandExecutor
}

// NewDefaultVerifier creates a new DefaultVerifier instance.
func NewDefaultVerifier(cmdExec execution.CommandExecutor) *DefaultVerifier {
	if cmdExec == nil {
		cmdExec = execution.NewOSCommandExecutor()
	}
	return &DefaultVerifier{cmdExec: cmdExec}
}

// Format runs code formatting checks.
func (v *DefaultVerifier) Format(ctx context.Context, opts *VerificationOptions) (*VerificationCheck, error) {
	res, err := v.cmdExec.ExecuteCommand(ctx, "go", []string{"fmt", "./..."}, nil)
	check := &VerificationCheck{Type: CheckTypeFormat, Passed: true}
	if err != nil || res.ExitCode != 0 {
		check.Passed = false
		if res != nil {
			check.Output = res.Stdout
			check.Error = res.Stderr
		}
	}
	return check, nil
}

// Compile runs build compilation checks.
func (v *DefaultVerifier) Compile(ctx context.Context, opts *VerificationOptions) (*VerificationCheck, error) {
	res, err := v.cmdExec.ExecuteCommand(ctx, "go", []string{"build", "./..."}, nil)
	check := &VerificationCheck{Type: CheckTypeCompile, Passed: true}
	if err != nil || res.ExitCode != 0 {
		check.Passed = false
		if res != nil {
			check.Output = res.Stdout
			check.Error = res.Stderr
		}
	}
	return check, nil
}

// Test runs test suite checks.
func (v *DefaultVerifier) Test(ctx context.Context, opts *VerificationOptions) (*VerificationCheck, error) {
	res, err := v.cmdExec.ExecuteCommand(ctx, "go", []string{"test", "./..."}, nil)
	check := &VerificationCheck{Type: CheckTypeTest, Passed: true}
	if err != nil || res.ExitCode != 0 {
		check.Passed = false
		if res != nil {
			check.Output = res.Stdout
			check.Error = res.Stderr
		}
	}
	return check, nil
}

// Lint runs static analysis checks.
func (v *DefaultVerifier) Lint(ctx context.Context, opts *VerificationOptions) (*VerificationCheck, error) {
	res, err := v.cmdExec.ExecuteCommand(ctx, "go", []string{"vet", "./..."}, nil)
	check := &VerificationCheck{Type: CheckTypeLint, Passed: true}
	if err != nil || res.ExitCode != 0 {
		check.Passed = false
		if res != nil {
			check.Output = res.Stdout
			check.Error = res.Stderr
		}
	}
	return check, nil
}

// Verify runs all checks sequentially.
func (v *DefaultVerifier) Verify(ctx context.Context, opts *VerificationOptions) (*VerificationResult, error) {
	result := &VerificationResult{Passed: true}

	fmtCheck, _ := v.Format(ctx, opts)
	result.Checks = append(result.Checks, *fmtCheck)

	compileCheck, _ := v.Compile(ctx, opts)
	result.Checks = append(result.Checks, *compileCheck)

	testCheck, _ := v.Test(ctx, opts)
	result.Checks = append(result.Checks, *testCheck)

	lintCheck, _ := v.Lint(ctx, opts)
	result.Checks = append(result.Checks, *lintCheck)

	for _, check := range result.Checks {
		if !check.Passed {
			result.Passed = false
			break
		}
	}

	return result, nil
}

// SelfHealingLoop encapsulates a feedback loop for automatically fixing verification errors.
type SelfHealingLoop struct {
	verifier    Verifier
	maxAttempts int
}

// NewSelfHealingLoop creates a new SelfHealingLoop instance.
func NewSelfHealingLoop(verifier Verifier, maxAttempts int) *SelfHealingLoop {
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	return &SelfHealingLoop{
		verifier:    verifier,
		maxAttempts: maxAttempts,
	}
}

// Run executes verification and invokes attemptFix when verification fails until clean or maxAttempts reached.
func (sh *SelfHealingLoop) Run(ctx context.Context, attemptFix func(ctx context.Context, failResult *VerificationResult) error) (*VerificationResult, error) {
	var lastResult *VerificationResult

	for i := 0; i < sh.maxAttempts; i++ {
		res, err := sh.verifier.Verify(ctx, nil)
		if err != nil {
			return nil, err
		}
		lastResult = res

		if res.Passed {
			return res, nil
		}

		if attemptFix != nil {
			if err := attemptFix(ctx, res); err != nil {
				return res, fmt.Errorf("self-healing attempt %d fix failed: %w", i+1, err)
			}
		}
	}

	return lastResult, fmt.Errorf("verification failed after %d self-healing attempts", sh.maxAttempts)
}
