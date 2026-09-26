package execution_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/dimetron/pi-go/internal/execution"
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
	if data, ok := m.files[cleanPath]; ok {
		return &repository.FileInfo{Path: cleanPath, Name: filepath.Base(cleanPath), Size: int64(len(data)), IsDir: false}, nil
	}
	return nil, os.ErrNotExist
}

func (m *memoryFS) ReadDir(ctx context.Context, path string) ([]*repository.FileInfo, error) { return nil, nil }
func (m *memoryFS) MkdirAll(ctx context.Context, path string, perm uint32) error          { return nil }
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

func TestDefaultCodeModifier_ApplyPatch(t *testing.T) {
	ctx := context.Background()
	mFS := newMemoryFS()
	mFS.files["main.go"] = []byte("package main\n\nfunc Hello() string {\n\treturn \"hello\"\n}\n")

	repo := &memoryRepo{fs: mFS, root: "/test"}
	modifier := execution.NewDefaultCodeModifier()

	patch := execution.PatchEdit{
		Path:    "main.go",
		Search:  "return \"hello\"",
		Replace: "return \"hello world\"",
	}

	err := modifier.ApplyPatch(ctx, repo, patch)
	if err != nil {
		t.Fatalf("ApplyPatch failed: %v", err)
	}

	updated, err := mFS.ReadFile(ctx, "main.go")
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	if string(updated) != "package main\n\nfunc Hello() string {\n\treturn \"hello world\"\n}\n" {
		t.Errorf("unexpected updated content: %s", string(updated))
	}
}
