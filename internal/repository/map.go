package repository

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// MapNode represents a file or folder node in the repository structure.
type MapNode struct {
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	IsDir    bool       `json:"isDir"`
	Children []*MapNode `json:"children,omitempty"`
}

// RepositoryMap represents the structural architecture map of the repository.
type RepositoryMap struct {
	Root           *MapNode            `json:"root"`
	CategoryGroups map[string][]string `json:"categoryGroups,omitempty"`
}

// MapBuilder constructs a structural map of the repository.
type MapBuilder interface {
	BuildMap(ctx context.Context, repo Repository, maxDepth int) (*RepositoryMap, error)
}

// LocalMapBuilder builds a repository map by traversing the filesystem.
type LocalMapBuilder struct{}

// NewLocalMapBuilder creates a new LocalMapBuilder instance.
func NewLocalMapBuilder() *LocalMapBuilder {
	return &LocalMapBuilder{}
}

// BuildMap builds a RepositoryMap traversing up to maxDepth levels.
func (b *LocalMapBuilder) BuildMap(ctx context.Context, repo Repository, maxDepth int) (*RepositoryMap, error) {
	fs := repo.FileSystem()
	categories := make(map[string][]string)

	var buildNode func(path string, depth int) (*MapNode, error)
	buildNode = func(path string, depth int) (*MapNode, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		info, err := fs.Stat(ctx, path)
		name := filepath.Base(path)
		if path == "." || path == "" {
			name = repo.RootPath()
		}

		node := &MapNode{
			Name:  name,
			Path:  path,
			IsDir: info == nil || info.IsDir,
		}

		if !node.IsDir || (maxDepth > 0 && depth >= maxDepth) {
			return node, nil
		}

		// Categorization mapping
		lower := strings.ToLower(node.Path)
		if strings.Contains(lower, "cmd") || strings.Contains(lower, "app") || strings.Contains(lower, "api") {
			categories["Applications"] = append(categories["Applications"], node.Path)
		} else if strings.Contains(lower, "domain") || strings.Contains(lower, "models") || strings.Contains(lower, "core") {
			categories["Domain"] = append(categories["Domain"], node.Path)
		} else if strings.Contains(lower, "infra") || strings.Contains(lower, "db") || strings.Contains(lower, "adapter") {
			categories["Infrastructure"] = append(categories["Infrastructure"], node.Path)
		} else if strings.Contains(lower, "test") || strings.Contains(lower, "spec") {
			categories["Tests"] = append(categories["Tests"], node.Path)
		}

		entries, err := fs.ReadDir(ctx, path)
		if err != nil {
			return node, nil
		}

		for _, entry := range entries {
			entryName := entry.Name
			if strings.HasPrefix(entryName, ".") || entryName == "node_modules" || entryName == "vendor" || entryName == "dist" || entryName == "build" {
				continue
			}
			childPath := entry.Path
			if childPath == "" {
				childPath = filepath.Join(path, entryName)
			}
			childNode, err := buildNode(childPath, depth+1)
			if err != nil {
				return nil, err
			}
			node.Children = append(node.Children, childNode)
		}

		return node, nil
	}

	rootNode, err := buildNode(".", 0)
	if err != nil {
		return nil, fmt.Errorf("failed to build repo map: %w", err)
	}

	return &RepositoryMap{
		Root:           rootNode,
		CategoryGroups: categories,
	}, nil
}
