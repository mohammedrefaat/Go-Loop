package verification

import (
	"context"
	"fmt"
	"time"
)

// VerificationCheckType identifies the specific type of verification check.
type VerificationCheckType string

const (
	CheckTypeFormat  VerificationCheckType = "FORMAT"
	CheckTypeCompile VerificationCheckType = "COMPILE"
	CheckTypeTest    VerificationCheckType = "TEST"
	CheckTypeLint    VerificationCheckType = "LINT"
)

// VerificationCheck represents an individual verification step result.
type VerificationCheck struct {
	Type     VerificationCheckType `json:"type"`
	Passed   bool                  `json:"passed"`
	Output   string                `json:"output,omitempty"`
	Error    string                `json:"error,omitempty"`
	Duration time.Duration         `json:"duration"`
}

// VerificationResult summarizes the complete verification outcome.
type VerificationResult struct {
	Passed    bool                `json:"passed"`
	Checks    []VerificationCheck `json:"checks"`
	Duration  time.Duration       `json:"duration"`
	Timestamp time.Time           `json:"timestamp"`
}

// VerificationOptions controls options for verification runs.
type VerificationOptions struct {
	Files []string `json:"files,omitempty"`
	Fix   bool     `json:"fix,omitempty"`
}

// Verifier defines domain abstraction for code formatting, compilation, testing, and linting.
type Verifier interface {
	// Format checks or applies formatting on codebase files.
	Format(ctx context.Context, opts *VerificationOptions) (*VerificationCheck, error)

	// Compile checks if the codebase compiles successfully.
	Compile(ctx context.Context, opts *VerificationOptions) (*VerificationCheck, error)

	// Test executes test suites and reports test pass/fail results.
	Test(ctx context.Context, opts *VerificationOptions) (*VerificationCheck, error)

	// Lint runs static analysis / linters on the codebase.
	Lint(ctx context.Context, opts *VerificationOptions) (*VerificationCheck, error)

	// Verify executes format, compile, test, and lint in sequence.
	Verify(ctx context.Context, opts *VerificationOptions) (*VerificationResult, error)
}

// ValidateCheckType verifies if check type string is known.
func (c VerificationCheckType) Validate() error {
	switch c {
	case CheckTypeFormat, CheckTypeCompile, CheckTypeTest, CheckTypeLint:
		return nil
	default:
		return fmt.Errorf("invalid verification check type: %q", c)
	}
}
