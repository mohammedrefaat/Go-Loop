package repository_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/repository"
)

type memoryFS struct {
	files map[string][]byte
}

func newMemoryFS() *memoryFS {
	return &memoryFS{files: make(map[string][]byte)}
}

func (m *memoryFS) ReadFile(ctx context.Context, path string) ([]byte, error) {
	cleanPath := filepath.Clean(path)
	data, ok := m.files[cleanPath]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func (m *memoryFS) WriteFile(ctx context.Context, path string, data []byte, perm uint32) error {
	m.files[filepath.Clean(path)] = data
	return nil
}

func (m *memoryFS) DeleteFile(ctx context.Context, path string) error {
	delete(m.files, filepath.Clean(path))
	return nil
}

func (m *memoryFS) Stat(ctx context.Context, path string) (*repository.FileInfo, error) {
	cleanPath := filepath.Clean(path)
	if cleanPath == "." || cleanPath == "" {
		return &repository.FileInfo{Path: ".", Name: ".", IsDir: true}, nil
	}
	// Check if directory
	for fPath := range m.files {
		if strings.HasPrefix(fPath, cleanPath+"/") || strings.HasPrefix(fPath, cleanPath+"\\") {
			return &repository.FileInfo{Path: cleanPath, Name: filepath.Base(cleanPath), IsDir: true}, nil
		}
	}
	if data, ok := m.files[cleanPath]; ok {
		return &repository.FileInfo{
			Path:  cleanPath,
			Name:  filepath.Base(cleanPath),
			Size:  int64(len(data)),
			IsDir: false,
		}, nil
	}
	return nil, os.ErrNotExist
}

func (m *memoryFS) ReadDir(ctx context.Context, path string) ([]*repository.FileInfo, error) {
	cleanPath := filepath.Clean(path)
	seen := make(map[string]*repository.FileInfo)

	prefix := ""
	if cleanPath != "." && cleanPath != "" {
		prefix = cleanPath + string(filepath.Separator)
	}

	for fPath := range m.files {
		if prefix != "" && !strings.HasPrefix(fPath, prefix) {
			continue
		}
		rel := strings.TrimPrefix(fPath, prefix)
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) == 0 {
			continue
		}
		entryName := parts[0]
		entryPath := entryName
		if prefix != "" {
			entryPath = filepath.Join(cleanPath, entryName)
		}

		if len(parts) > 1 {
			seen[entryName] = &repository.FileInfo{
				Path:  entryPath,
				Name:  entryName,
				IsDir: true,
			}
		} else {
			seen[entryName] = &repository.FileInfo{
				Path:  entryPath,
				Name:  entryName,
				IsDir: false,
				Size:  int64(len(m.files[fPath])),
			}
		}
	}

	var results []*repository.FileInfo
	for _, fi := range seen {
		results = append(results, fi)
	}
	return results, nil
}

func (m *memoryFS) MkdirAll(ctx context.Context, path string, perm uint32) error {
	return nil
}

func (m *memoryFS) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	data, err := m.ReadFile(ctx, path)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

type memoryRepo struct {
	fs   repository.FileSystem
	root string
}

func (r *memoryRepo) RootPath() string                  { return r.root }
func (r *memoryRepo) FileSystem() repository.FileSystem { return r.fs }
func (r *memoryRepo) Git() repository.GitProvider       { return nil }

func TestLocalDiscoverer(t *testing.T) {
	ctx := context.Background()
	mFS := newMemoryFS()
	mFS.files["go.mod"] = []byte("module testrepo\n\nrequire github.com/gin-gonic/gin v1.9.1\n")
	mFS.files[filepath.Join("cmd", "api", "main.go")] = []byte("package main\nfunc main() {}\n")
	mFS.files[filepath.Join("cmd", "api", "main_test.go")] = []byte("package main\nimport \"testing\"\nfunc TestMain(t *testing.T) {}\n")
	mFS.files["Dockerfile"] = []byte("FROM golang:1.21\n")
	mFS.files[filepath.Join(".github", "workflows", "ci.yml")] = []byte("name: CI\n")
	mFS.files["README.md"] = []byte("# Test Repo\n")

	repo := &memoryRepo{fs: mFS, root: "/test/repo"}
	disc := repository.NewLocalDiscoverer()

	info, err := disc.Discover(ctx, repo)
	if err != nil {
		t.Fatalf("Discover failed: %v", err)
	}

	if len(info.Languages) == 0 || info.Languages[0] != "Go" {
		t.Errorf("expected Go language, got %v", info.Languages)
	}

	if len(info.Frameworks) == 0 || info.Frameworks[0] != "Gin" {
		t.Errorf("expected Gin framework, got %v", info.Frameworks)
	}

	if !info.HasDocker {
		t.Errorf("expected HasDocker to be true")
	}

	if !info.HasCICD {
		t.Errorf("expected HasCICD to be true")
	}

	if !info.HasDocs {
		t.Errorf("expected HasDocs to be true")
	}
}

func TestLocalMapBuilder(t *testing.T) {
	ctx := context.Background()
	mFS := newMemoryFS()
	mFS.files[filepath.Join("cmd", "api", "main.go")] = []byte("package main")
	mFS.files[filepath.Join("internal", "models", "user.go")] = []byte("package models")
	mFS.files[filepath.Join("internal", "infra", "db.go")] = []byte("package db")

	repo := &memoryRepo{fs: mFS, root: "/test/repo"}
	builder := repository.NewLocalMapBuilder()

	repoMap, err := builder.BuildMap(ctx, repo, 5)
	if err != nil {
		t.Fatalf("BuildMap failed: %v", err)
	}

	if repoMap.Root == nil {
		t.Fatalf("expected root node in map")
	}

	if len(repoMap.CategoryGroups["Applications"]) == 0 {
		t.Errorf("expected Applications category group")
	}
	if len(repoMap.CategoryGroups["Domain"]) == 0 {
		t.Errorf("expected Domain category group")
	}
	if len(repoMap.CategoryGroups["Infrastructure"]) == 0 {
		t.Errorf("expected Infrastructure category group")
	}
}

func TestLocalSearcher(t *testing.T) {
	ctx := context.Background()
	mFS := newMemoryFS()
	mFS.files[filepath.Join("internal", "domain", "payment.go")] = []byte("package domain\n\nfunc ProcessPayment() {}\n")
	mFS.files[filepath.Join("internal", "infra", "db.go")] = []byte("package db\n\nfunc SavePayment() {}\n")

	repo := &memoryRepo{fs: mFS, root: "/test/repo"}
	searcher := repository.NewLocalSearcher()

	// Test ExactSearch
	exactRes, err := searcher.ExactSearch(ctx, repo, "ProcessPayment")
	if err != nil {
		t.Fatalf("ExactSearch failed: %v", err)
	}
	if len(exactRes.Matches) != 1 {
		t.Fatalf("expected 1 match for ProcessPayment, got %d", len(exactRes.Matches))
	}
	expectedPath := filepath.Join("internal", "domain", "payment.go")
	if exactRes.Matches[0].Path != expectedPath {
		t.Errorf("unexpected path %s, expected %s", exactRes.Matches[0].Path, expectedPath)
	}

	// Test PatternSearch
	patternRes, err := searcher.PatternSearch(ctx, repo, "payment.go")
	if err != nil {
		t.Fatalf("PatternSearch failed: %v", err)
	}
	if len(patternRes.Matches) != 1 {
		t.Fatalf("expected 1 match for pattern payment.go, got %d", len(patternRes.Matches))
	}
}
