package context_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	picontext "github.com/dimetron/pi-go/internal/context"
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
	if data, ok := m.files[cleanPath]; ok {
		return &repository.FileInfo{Path: cleanPath, Name: filepath.Base(cleanPath), Size: int64(len(data)), IsDir: false}, nil
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
			seen[entryName] = &repository.FileInfo{Path: entryPath, Name: entryName, IsDir: true}
		} else {
			seen[entryName] = &repository.FileInfo{Path: entryPath, Name: entryName, IsDir: false, Size: int64(len(m.files[fPath]))}
		}
	}

	var results []*repository.FileInfo
	for _, fi := range seen {
		results = append(results, fi)
	}
	return results, nil
}

func (m *memoryFS) MkdirAll(ctx context.Context, path string, perm uint32) error { return nil }
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

func TestContextEngineGatherAndPackage(t *testing.T) {
	ctx := context.Background()
	mFS := newMemoryFS()
	mFS.files["README.md"] = []byte("# System Architecture\nThis is a sample readme.")
	mFS.files[filepath.Join("internal", "order.go")] = []byte("package order\nfunc ProcessOrder() error { return nil }")

	repo := &memoryRepo{fs: mFS, root: "/test"}
	searcher := repository.NewLocalSearcher()
	engine := picontext.NewDefaultContextEngine(searcher)

	items, err := engine.GatherContext(ctx, repo, "ProcessOrder", []string{filepath.Join("internal", "order.go")})
	if err != nil {
		t.Fatalf("GatherContext failed: %v", err)
	}

	if len(items) == 0 {
		t.Fatalf("expected items gathered, got 0")
	}

	budget := picontext.ContextBudget{
		MaxTokens:         1000,
		ReservedForSystem: 100,
		ReservedForOutput: 200,
	}

	pkg := engine.BuildPackage(items, budget)
	if pkg == nil {
		t.Fatalf("expected non-nil ContextPackage")
	}

	if pkg.TotalTokens > budget.AvailableTokens() {
		t.Errorf("TotalTokens %d exceeds budget %d", pkg.TotalTokens, budget.AvailableTokens())
	}
}
