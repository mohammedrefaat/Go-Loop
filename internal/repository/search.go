package repository

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// SearchMatch represents a matching file location or snippet from a search query.
type SearchMatch struct {
	Path       string `json:"path"`
	LineNumber int    `json:"lineNumber,omitempty"`
	LineText   string `json:"lineText,omitempty"`
}

// SearchResult contains all matches for a given search query.
type SearchResult struct {
	Query   string        `json:"query"`
	Matches []SearchMatch `json:"matches"`
}

// Searcher defines code searching capabilities over a Repository.
type Searcher interface {
	ExactSearch(ctx context.Context, repo Repository, query string) (*SearchResult, error)
	PatternSearch(ctx context.Context, repo Repository, pattern string) (*SearchResult, error)
}

// LocalSearcher performs code search on the local filesystem of a repository.
type LocalSearcher struct{}

// NewLocalSearcher creates a new LocalSearcher instance.
func NewLocalSearcher() *LocalSearcher {
	return &LocalSearcher{}
}

// ExactSearch searches for literal text across non-hidden files in the repository.
func (s *LocalSearcher) ExactSearch(ctx context.Context, repo Repository, query string) (*SearchResult, error) {
	if query == "" {
		return nil, fmt.Errorf("empty query")
	}

	fs := repo.FileSystem()
	result := &SearchResult{Query: query}

	var walk func(path string) error
	walk = func(path string) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		files, err := fs.ReadDir(ctx, path)
		if err != nil {
			return nil
		}

		for _, f := range files {
			name := f.Name
			fullPath := f.Path
			if fullPath == "" {
				fullPath = filepath.Join(path, name)
			}

			if f.IsDir {
				if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "dist" || name == "build" {
					continue
				}
				if err := walk(fullPath); err != nil {
					return err
				}
				continue
			}

			// Read file content and perform exact search
			data, err := fs.ReadFile(ctx, fullPath)
			if err != nil {
				continue
			}

			content := string(data)
			if strings.Contains(content, query) {
				lines := strings.Split(content, "\n")
				for idx, line := range lines {
					if strings.Contains(line, query) {
						result.Matches = append(result.Matches, SearchMatch{
							Path:       fullPath,
							LineNumber: idx + 1,
							LineText:   strings.TrimSpace(line),
						})
					}
				}
			}
		}
		return nil
	}

	if err := walk("."); err != nil {
		return nil, err
	}

	return result, nil
}

// PatternSearch searches for files matching a glob pattern or path fragment.
func (s *LocalSearcher) PatternSearch(ctx context.Context, repo Repository, pattern string) (*SearchResult, error) {
	if pattern == "" {
		return nil, fmt.Errorf("empty pattern")
	}

	fs := repo.FileSystem()
	result := &SearchResult{Query: pattern}
	lowerPattern := strings.ToLower(pattern)

	var walk func(path string) error
	walk = func(path string) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		files, err := fs.ReadDir(ctx, path)
		if err != nil {
			return nil
		}

		for _, f := range files {
			name := f.Name
			fullPath := f.Path
			if fullPath == "" {
				fullPath = filepath.Join(path, name)
			}

			if f.IsDir {
				if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "dist" || name == "build" {
					continue
				}
				if err := walk(fullPath); err != nil {
					return err
				}
				continue
			}

			matched, err := filepath.Match(pattern, name)
			if (err == nil && matched) || strings.Contains(strings.ToLower(fullPath), lowerPattern) {
				result.Matches = append(result.Matches, SearchMatch{
					Path: fullPath,
				})
			}
		}
		return nil
	}

	if err := walk("."); err != nil {
		return nil, err
	}

	return result, nil
}
