package verification

import (
	"context"
	"testing"
	"time"
)

type mockVerifier struct{}

func (m *mockVerifier) Format(ctx context.Context, opts *VerificationOptions) (*VerificationCheck, error) {
	return &VerificationCheck{Type: CheckTypeFormat, Passed: true}, nil
}
func (m *mockVerifier) Compile(ctx context.Context, opts *VerificationOptions) (*VerificationCheck, error) {
	return &VerificationCheck{Type: CheckTypeCompile, Passed: true}, nil
}
func (m *mockVerifier) Test(ctx context.Context, opts *VerificationOptions) (*VerificationCheck, error) {
	return &VerificationCheck{Type: CheckTypeTest, Passed: true}, nil
}
func (m *mockVerifier) Lint(ctx context.Context, opts *VerificationOptions) (*VerificationCheck, error) {
	return &VerificationCheck{Type: CheckTypeLint, Passed: true}, nil
}
func (m *mockVerifier) Verify(ctx context.Context, opts *VerificationOptions) (*VerificationResult, error) {
	return &VerificationResult{
		Passed:    true,
		Timestamp: time.Now(),
		Checks: []VerificationCheck{
			{Type: CheckTypeFormat, Passed: true},
			{Type: CheckTypeCompile, Passed: true},
			{Type: CheckTypeTest, Passed: true},
			{Type: CheckTypeLint, Passed: true},
		},
	}, nil
}

func TestVerifierInterface(t *testing.T) {
	var v Verifier = &mockVerifier{}

	res, err := v.Verify(context.Background(), &VerificationOptions{})
	if err != nil || !res.Passed {
		t.Fatalf("unexpected result: %+v, %v", res, err)
	}

	if len(res.Checks) != 4 {
		t.Errorf("expected 4 checks, got %d", len(res.Checks))
	}
}
