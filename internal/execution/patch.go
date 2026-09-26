package execution

import (
	"context"
	"fmt"
	"strings"

	"github.com/dimetron/pi-go/internal/repository"
)

// PatchEdit represents a single block edit replacement in a file.
type PatchEdit struct {
	Path    string `json:"path"`
	Search  string `json:"search"`
	Replace string `json:"replace"`
}

// CodeModifier performs patch-based and block replacement modifications on repository files.
type CodeModifier interface {
	ApplyPatch(ctx context.Context, repo repository.Repository, patch PatchEdit) error
	ApplyMultiPatch(ctx context.Context, repo repository.Repository, patches []PatchEdit) error
}

// DefaultCodeModifier implements CodeModifier.
type DefaultCodeModifier struct{}

// NewDefaultCodeModifier creates a new DefaultCodeModifier instance.
func NewDefaultCodeModifier() *DefaultCodeModifier {
	return &DefaultCodeModifier{}
}

// ApplyPatch applies a search-and-replace block patch to a specified file.
func (m *DefaultCodeModifier) ApplyPatch(ctx context.Context, repo repository.Repository, patch PatchEdit) error {
	fs := repo.FileSystem()
	data, err := fs.ReadFile(ctx, patch.Path)
	if err != nil {
		return fmt.Errorf("failed to read file %s for patch: %w", patch.Path, err)
	}

	content := string(data)
	if !strings.Contains(content, patch.Search) {
		return fmt.Errorf("search block not found in file %s", patch.Path)
	}

	// Perform exact replacement
	newContent := strings.Replace(content, patch.Search, patch.Replace, 1)
	if err := fs.WriteFile(ctx, patch.Path, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("failed to write patched file %s: %w", patch.Path, err)
	}

	return nil
}

// ApplyMultiPatch applies a slice of patch edits sequentially across files.
func (m *DefaultCodeModifier) ApplyMultiPatch(ctx context.Context, repo repository.Repository, patches []PatchEdit) error {
	for i, patch := range patches {
		if err := m.ApplyPatch(ctx, repo, patch); err != nil {
			return fmt.Errorf("patch %d failed on %s: %w", i, patch.Path, err)
		}
	}
	return nil
}
