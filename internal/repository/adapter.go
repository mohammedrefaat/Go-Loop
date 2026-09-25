package repository

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// SandboxFS implements FileSystem over an os.Root or directory root.
type SandboxFS struct {
	rootDir string
}

// NewSandboxFS creates a new FileSystem adapter rooted at rootDir.
func NewSandboxFS(rootDir string) (*SandboxFS, error) {
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolving root dir: %w", err)
	}
	return &SandboxFS{rootDir: abs}, nil
}

func (s *SandboxFS) resolve(path string) (string, error) {
	var target string
	if filepath.IsAbs(path) {
		target = path
	} else {
		target = filepath.Join(s.rootDir, path)
	}
	cleaned := filepath.Clean(target)
	rel, err := filepath.Rel(s.rootDir, cleaned)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %s escapes root %s", path, s.rootDir)
	}
	return cleaned, nil
}

func (s *SandboxFS) ReadFile(ctx context.Context, path string) ([]byte, error) {
	resolved, err := s.resolve(path)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(resolved)
}

func (s *SandboxFS) WriteFile(ctx context.Context, path string, data []byte, perm uint32) error {
	resolved, err := s.resolve(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return err
	}
	return os.WriteFile(resolved, data, os.FileMode(perm))
}

func (s *SandboxFS) DeleteFile(ctx context.Context, path string) error {
	resolved, err := s.resolve(path)
	if err != nil {
		return err
	}
	return os.Remove(resolved)
}

func (s *SandboxFS) Stat(ctx context.Context, path string) (*FileInfo, error) {
	resolved, err := s.resolve(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	return &FileInfo{
		Path:    path,
		Name:    st.Name(),
		Size:    st.Size(),
		IsDir:   st.IsDir(),
		Mode:    uint32(st.Mode()),
		ModTime: st.ModTime(),
	}, nil
}

func (s *SandboxFS) ReadDir(ctx context.Context, path string) ([]*FileInfo, error) {
	resolved, err := s.resolve(path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return nil, err
	}
	var res []*FileInfo
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		relPath := filepath.Join(path, entry.Name())
		res = append(res, &FileInfo{
			Path:    relPath,
			Name:    entry.Name(),
			Size:    info.Size(),
			IsDir:   entry.IsDir(),
			Mode:    uint32(info.Mode()),
			ModTime: info.ModTime(),
		})
	}
	return res, nil
}

func (s *SandboxFS) MkdirAll(ctx context.Context, path string, perm uint32) error {
	resolved, err := s.resolve(path)
	if err != nil {
		return err
	}
	return os.MkdirAll(resolved, os.FileMode(perm))
}

func (s *SandboxFS) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	resolved, err := s.resolve(path)
	if err != nil {
		return nil, err
	}
	return os.Open(resolved)
}

// ExecGit implements GitProvider using OS git binary execution.
type ExecGit struct {
	workDir string
}

// NewExecGit creates a GitProvider adapter for the given work directory.
func NewExecGit(workDir string) *ExecGit {
	return &ExecGit{workDir: workDir}
}

func (g *ExecGit) exec(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (g *ExecGit) CurrentBranch(ctx context.Context) (string, error) {
	return g.exec(ctx, "rev-parse", "--abbrev-ref", "HEAD")
}

func (g *ExecGit) Status(ctx context.Context) (string, error) {
	return g.exec(ctx, "status", "--porcelain")
}

func (g *ExecGit) Diff(ctx context.Context, opts *GitDiffOptions) (string, error) {
	args := []string{"diff"}
	if opts != nil {
		if opts.Cached {
			args = append(args, "--cached")
		}
		if opts.BaseRef != "" {
			if opts.TargetRef != "" {
				args = append(args, opts.BaseRef+".."+opts.TargetRef)
			} else {
				args = append(args, opts.BaseRef)
			}
		}
		if opts.Path != "" {
			args = append(args, "--", opts.Path)
		}
	}
	return g.exec(ctx, args...)
}

func (g *ExecGit) Commit(ctx context.Context, message string) (*GitCommit, error) {
	if _, err := g.exec(ctx, "commit", "-m", message); err != nil {
		return nil, err
	}
	hash, err := g.exec(ctx, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	author, _ := g.exec(ctx, "log", "-1", "--format=%an <%ae>")
	return &GitCommit{
		Hash:      hash,
		Author:    author,
		Message:   message,
		Committed: time.Now(),
	}, nil
}

func (g *ExecGit) CreateBranch(ctx context.Context, name string) error {
	_, err := g.exec(ctx, "checkout", "-b", name)
	return err
}

func (g *ExecGit) Checkout(ctx context.Context, branchOrRef string) error {
	_, err := g.exec(ctx, "checkout", branchOrRef)
	return err
}

func (g *ExecGit) RootPath(ctx context.Context) (string, error) {
	return g.exec(ctx, "rev-parse", "--show-toplevel")
}

// LocalRepository composes SandboxFS and ExecGit into a Repository adapter.
type LocalRepository struct {
	fs   FileSystem
	git  GitProvider
	root string
}

// NewLocalRepository creates a new LocalRepository.
func NewLocalRepository(rootDir string) (*LocalRepository, error) {
	fs, err := NewSandboxFS(rootDir)
	if err != nil {
		return nil, err
	}
	git := NewExecGit(rootDir)
	return &LocalRepository{
		fs:   fs,
		git:  git,
		root: rootDir,
	}, nil
}

func (r *LocalRepository) RootPath() string        { return r.root }
func (r *LocalRepository) FileSystem() FileSystem { return r.fs }
func (r *LocalRepository) Git() GitProvider       { return r.git }
