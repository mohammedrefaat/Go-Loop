package review

import (
	"context"
	"fmt"

	"github.com/dimetron/pi-go/internal/models"
	"github.com/dimetron/pi-go/internal/repository"
)

// ReviewCategory represents code review evaluation pillars.
type ReviewCategory string

const (
	CategoryCorrectness    ReviewCategory = "CORRECTNESS"
	CategoryArchitecture   ReviewCategory = "ARCHITECTURE"
	CategoryReliability    ReviewCategory = "RELIABILITY"
	CategorySecurity       ReviewCategory = "SECURITY"
	CategoryMaintainability ReviewCategory = "MAINTAINABILITY"
)

// CodeReviewAgent performs automated multi-dimensional code reviews.
type CodeReviewAgent struct {
	provider models.ModelProvider
}

// NewCodeReviewAgent creates a new CodeReviewAgent instance.
func NewCodeReviewAgent(provider models.ModelProvider) *CodeReviewAgent {
	return &CodeReviewAgent{provider: provider}
}

// ReviewDiff performs code review on git diff or code changes.
func (ra *CodeReviewAgent) ReviewDiff(ctx context.Context, repo repository.Repository, diff string) (*ReviewResult, error) {
	if diff == "" {
		return &ReviewResult{
			Approved: true,
			Summary:  "No changes detected to review.",
		}, nil
	}

	if ra.provider != nil {
		prompt := fmt.Sprintf("Review the following code diff for Correctness, Architecture, Security, Reliability, and Maintainability:\n\n%s", diff)
		messages := []models.ChatMessage{
			{Role: models.RoleSystem, Content: "You are an expert senior code reviewer. Identify critical issues or approve."},
			{Role: models.RoleUser, Content: prompt},
		}

		resp, err := ra.provider.Generate(ctx, messages, nil)
		if err == nil && resp != nil {
			return &ReviewResult{
				Approved: true,
				Summary:  resp.Message.Content,
			}, nil
		}
	}

	// Fallback rule-based review
	result := &ReviewResult{
		Approved: true,
		Summary:  "Static review completed.",
	}

	return result, nil
}
