package review

import (
	"context"
)

// ReviewIssue represents a quality or architectural issue flagged during code review.
type ReviewIssue struct {
	File     string `json:"file"`
	Line     int    `json:"line,omitempty"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// ReviewResult summarizes code review findings.
type ReviewResult struct {
	Approved bool          `json:"approved"`
	Issues   []ReviewIssue `json:"issues,omitempty"`
	Summary  string        `json:"summary"`
}

// Reviewer analyzes code changes for SOLID, Clean Architecture, and safety compliance.
type Reviewer interface {
	// ReviewChanges reviews modified repository files.
	ReviewChanges(ctx context.Context, workDir string) (*ReviewResult, error)
}
