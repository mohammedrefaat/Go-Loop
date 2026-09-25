package review

import (
	"context"
	"testing"
)

type mockReviewer struct{}

func (m *mockReviewer) ReviewChanges(ctx context.Context, workDir string) (*ReviewResult, error) {
	return &ReviewResult{Approved: true, Summary: "looks good"}, nil
}

func TestReviewerInterface(t *testing.T) {
	var r Reviewer = &mockReviewer{}
	res, err := r.ReviewChanges(context.Background(), ".")
	if err != nil || !res.Approved {
		t.Fatalf("unexpected review result: %+v, %v", res, err)
	}
}
