package repository

import (
	"context"
	"io"
	"time"
)

// FileInfo represents metadata for a file in the file system.
type FileInfo struct {
	Path    string    `json:"path"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	IsDir   bool      `json:"isDir"`
	Mode    uint32    `json:"mode"`
	ModTime time.Time `json:"modTime"`
}

// FileSystem abstracts file system read/write operations without OS dependencies.
type FileSystem interface {
	ReadFile(ctx context.Context, path string) ([]byte, error)
	WriteFile(ctx context.Context, path string, data []byte, perm uint32) error
	DeleteFile(ctx context.Context, path string) error
	Stat(ctx context.Context, path string) (*FileInfo, error)
	ReadDir(ctx context.Context, path string) ([]*FileInfo, error)
	MkdirAll(ctx context.Context, path string, perm uint32) error
	Open(ctx context.Context, path string) (io.ReadCloser, error)
}

// GitCommit represents a commit in git repository.
type GitCommit struct {
	Hash      string    `json:"hash"`
	Author    string    `json:"author"`
	Message   string    `json:"message"`
	Committed time.Time `json:"committed"`
}

// GitDiffOptions configures diff operations.
type GitDiffOptions struct {
	Path      string `json:"path,omitempty"`
	Cached    bool   `json:"cached,omitempty"`
	BaseRef   string `json:"baseRef,omitempty"`
	TargetRef string `json:"targetRef,omitempty"`
}

// GitProvider abstracts Git version control operations without direct binary coupling.
type GitProvider interface {
	CurrentBranch(ctx context.Context) (string, error)
	Status(ctx context.Context) (string, error)
	Diff(ctx context.Context, opts *GitDiffOptions) (string, error)
	Commit(ctx context.Context, message string) (*GitCommit, error)
	CreateBranch(ctx context.Context, name string) error
	Checkout(ctx context.Context, branchOrRef string) error
	RootPath(ctx context.Context) (string, error)
}

// Repository composes FileSystem and GitProvider into a unified repository domain abstraction.
type Repository interface {
	RootPath() string
	FileSystem() FileSystem
	Git() GitProvider
}
