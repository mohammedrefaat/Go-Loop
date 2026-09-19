package repository

import (
	"context"
	"io"
	"testing"
)

type mockFS struct{}

func (m *mockFS) ReadFile(ctx context.Context, path string) ([]byte, error) {
	return []byte("hello"), nil
}
func (m *mockFS) WriteFile(ctx context.Context, path string, data []byte, perm uint32) error {
	return nil
}
func (m *mockFS) DeleteFile(ctx context.Context, path string) error { return nil }
func (m *mockFS) Stat(ctx context.Context, path string) (*FileInfo, error) {
	return &FileInfo{Path: path, Name: "foo"}, nil
}
func (m *mockFS) ReadDir(ctx context.Context, path string) ([]*FileInfo, error) {
	return []*FileInfo{{Path: "foo"}}, nil
}
func (m *mockFS) MkdirAll(ctx context.Context, path string, perm uint32) error { return nil }
func (m *mockFS) Open(ctx context.Context, path string) (io.ReadCloser, error)         { return nil, nil }

type mockGit struct{}

func (m *mockGit) CurrentBranch(ctx context.Context) (string, error) { return "main", nil }
func (m *mockGit) Status(ctx context.Context) (string, error)        { return "clean", nil }
func (m *mockGit) Diff(ctx context.Context, opts *GitDiffOptions) (string, error) {
	return "", nil
}
func (m *mockGit) Commit(ctx context.Context, message string) (*GitCommit, error) {
	return &GitCommit{Hash: "abc", Message: message}, nil
}
func (m *mockGit) CreateBranch(ctx context.Context, name string) error { return nil }
func (m *mockGit) Checkout(ctx context.Context, branchOrRef string) error {
	return nil
}
func (m *mockGit) RootPath(ctx context.Context) (string, error) { return "/app", nil }

type mockRepo struct {
	fs  FileSystem
	git GitProvider
}

func (m *mockRepo) RootPath() string        { return "/app" }
func (m *mockRepo) FileSystem() FileSystem { return m.fs }
func (m *mockRepo) Git() GitProvider       { return m.git }

func TestRepositoryInterfaces(t *testing.T) {
	repo := &mockRepo{fs: &mockFS{}, git: &mockGit{}}

	if repo.RootPath() != "/app" {
		t.Errorf("unexpected root path: %s", repo.RootPath())
	}

	data, err := repo.FileSystem().ReadFile(context.Background(), "test.txt")
	if err != nil || string(data) != "hello" {
		t.Errorf("unexpected read file: %s, %v", string(data), err)
	}

	branch, err := repo.Git().CurrentBranch(context.Background())
	if err != nil || branch != "main" {
		t.Errorf("unexpected branch: %s, %v", branch, err)
	}
}
