package context

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/dimetron/pi-go/internal/repository"
)

// ContextItemType represents the classification of context content.
type ContextItemType string

const (
	ContextTypeFile    ContextItemType = "file"
	ContextTypeSymbol  ContextItemType = "symbol"
	ContextTypeGit     ContextItemType = "git"
	ContextTypeDoc     ContextItemType = "doc"
	ContextTypeRuntime ContextItemType = "runtime"
)

// ContextItem represents a single unit of relevant context gathered for a task.
type ContextItem struct {
	ID       string          `json:"id"`
	Type     ContextItemType `json:"type"`
	Path     string          `json:"path,omitempty"`
	Content  string          `json:"content"`
	Priority int             `json:"priority"` // Higher number = higher importance
	Tokens   int             `json:"tokens"`
}

// ContextBudget defines context token budget constraints.
type ContextBudget struct {
	MaxTokens       int `json:"maxTokens"`
	ReservedForSystem int `json:"reservedForSystem"`
	ReservedForOutput int `json:"reservedForOutput"`
}

// AvailableTokens returns total remaining tokens for relevant context items.
func (b *ContextBudget) AvailableTokens() int {
	avail := b.MaxTokens - b.ReservedForSystem - b.ReservedForOutput
	if avail < 0 {
		return 0
	}
	return avail
}

// ContextPackage contains assembled context items constrained by budget.
type ContextPackage struct {
	Items       []ContextItem `json:"items"`
	TotalTokens int           `json:"totalTokens"`
	Budget      ContextBudget `json:"budget"`
}

// ContextEngine gathers, ranks, and fits context within a token budget.
type ContextEngine interface {
	GatherContext(ctx context.Context, repo repository.Repository, query string, hints []string) ([]ContextItem, error)
	BuildPackage(items []ContextItem, budget ContextBudget) *ContextPackage
}

// DefaultContextEngine implements ContextEngine.
type DefaultContextEngine struct {
	searcher repository.Searcher
}

// NewDefaultContextEngine creates a new ContextEngine instance.
func NewDefaultContextEngine(searcher repository.Searcher) *DefaultContextEngine {
	if searcher == nil {
		searcher = repository.NewLocalSearcher()
	}
	return &DefaultContextEngine{searcher: searcher}
}

// EstimateTokens provides a lightweight token estimation (~4 chars per token).
func EstimateTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	tokens := len(text) / 4
	if tokens == 0 {
		return 1
	}
	return tokens
}

// GatherContext retrieves relevant files, documentation, and search matches.
func (e *DefaultContextEngine) GatherContext(ctx context.Context, repo repository.Repository, query string, hints []string) ([]ContextItem, error) {
	var items []ContextItem
	fs := repo.FileSystem()

	// 1. Check for documentation (README, etc.)
	if data, err := fs.ReadFile(ctx, "README.md"); err == nil {
		content := string(data)
		items = append(items, ContextItem{
			ID:       "doc:README.md",
			Type:     ContextTypeDoc,
			Path:     "README.md",
			Content:  content,
			Priority: 10,
			Tokens:   EstimateTokens(content),
		})
	}

	// 2. Gather hint files explicitly specified
	for _, hint := range hints {
		if data, err := fs.ReadFile(ctx, hint); err == nil {
			content := string(data)
			items = append(items, ContextItem{
				ID:       fmt.Sprintf("file:%s", hint),
				Type:     ContextTypeFile,
				Path:     hint,
				Content:  content,
				Priority: 20,
				Tokens:   EstimateTokens(content),
			})
		}
	}

	// 3. Search query matches across repository
	if query != "" {
		res, err := e.searcher.ExactSearch(ctx, repo, query)
		if err == nil && res != nil {
			fileMatches := make(map[string][]string)
			for _, m := range res.Matches {
				fileMatches[m.Path] = append(fileMatches[m.Path], m.LineText)
			}
			for path, lines := range fileMatches {
				snippet := strings.Join(lines, "\n")
				items = append(items, ContextItem{
					ID:       fmt.Sprintf("search:%s", path),
					Type:     ContextTypeSymbol,
					Path:     path,
					Content:  snippet,
					Priority: 15,
					Tokens:   EstimateTokens(snippet),
				})
			}
		}
	}

	return items, nil
}

// BuildPackage filters and ranks items to fit within ContextBudget available tokens.
func (e *DefaultContextEngine) BuildPackage(items []ContextItem, budget ContextBudget) *ContextPackage {
	// Sort by priority descending
	sorted := make([]ContextItem, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Priority > sorted[j].Priority
	})

	avail := budget.AvailableTokens()
	var selected []ContextItem
	usedTokens := 0

	for _, item := range sorted {
		if usedTokens+item.Tokens <= avail {
			selected = append(selected, item)
			usedTokens += item.Tokens
		}
	}

	return &ContextPackage{
		Items:       selected,
		TotalTokens: usedTokens,
		Budget:      budget,
	}
}
