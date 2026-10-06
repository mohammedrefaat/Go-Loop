package extension

import (
	"path/filepath"
	"strings"
	"sync"
)

// touchedFiles records which repository files a session has actually worked
// on, so `paths:`-gated skills can be offered only when one of them is
// relevant.
//
// It is a bounded, insertion-ordered set: the point is not to be complete but
// to be good enough to open the right gate without tracking every file in a
// long session.
type TouchedFiles struct {
	mu    sync.Mutex
	files []string
	seen  map[string]bool
	limit int
	// root is the working directory, used to reduce an absolute tool argument
	// to the repo-relative form patterns are written against. Empty means the
	// caller has no root, in which case an absolute path stays absolute and
	// simply never matches — the safe direction, since inventing a relative
	// form could open a gate the author never wrote.
	root string
}

// DefaultTouchedLimit bounds how many distinct files are remembered. A session
// that touches more than this is already editing broadly enough that gating
// saves little, and an unbounded set would grow the prompt for the rest of the
// session.
const DefaultTouchedLimit = 200

// NewTouchedFiles returns an empty tracker with the default bound and no root.
func NewTouchedFiles() *TouchedFiles {
	return &TouchedFiles{seen: make(map[string]bool), limit: DefaultTouchedLimit}
}

// NewTouchedFilesIn returns an empty tracker rooted at dir, so absolute tool
// arguments are recorded as paths relative to it.
func NewTouchedFilesIn(dir string) *TouchedFiles {
	t := NewTouchedFiles()
	if dir != "" {
		t.root = filepath.ToSlash(filepath.Clean(dir))
	}
	return t
}

// Note records that the session touched p. Paths are normalized to the
// repo-relative, forward-slash form patterns are written against, so a caller
// may pass an absolute path from a tool argument.
//
// Repeats are ignored, so calling Note for every tool call is cheap. Once the
// bound is reached further paths are dropped rather than evicting earlier
// ones: the first files touched are the ones that describe what the session is
// about, and a late arrival displacing them would make the gate flap.
func (t *TouchedFiles) Note(p string) {
	if t == nil {
		return
	}
	p = t.normalize(p)
	if p == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.seen == nil {
		t.seen = make(map[string]bool)
	}
	if t.seen[p] || len(t.files) >= t.limit {
		return
	}
	t.seen[p] = true
	t.files = append(t.files, p)
}

// NoteAll records several paths.
func (t *TouchedFiles) NoteAll(paths []string) {
	if t == nil {
		return
	}
	for _, p := range paths {
		t.Note(p)
	}
}

// Files returns a copy of the recorded paths, in the order first touched.
func (t *TouchedFiles) Files() []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.files...)
}

// Len returns how many distinct files are recorded.
func (t *TouchedFiles) Len() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.files)
}

// Reset clears the tracker, for reuse across turns in a long-lived session.
func (t *TouchedFiles) Reset() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.files = nil
	t.seen = make(map[string]bool)
}

// Filter returns the skills relevant to what has been touched so far. A nil
// tracker means nothing is known, which is the same as an empty session.
func (t *TouchedFiles) Filter(skills []Skill) []Skill {
	if t == nil {
		return FilterByPaths(skills, nil)
	}
	return FilterByPaths(skills, t.Files())
}

// normalize reduces a tool-supplied path to the comparable form, relative to
// the tracker's root.
//
// An absolute path under the root becomes repo-relative. One that is not —
// and any absolute path at all when the root is unknown — is kept as-is,
// because no repo-relative pattern can describe it and guessing a relative
// form would match patterns the author never wrote.
func (t *TouchedFiles) normalize(p string) string {
	if p == "" {
		return ""
	}
	p = filepath.ToSlash(p)
	// A leading slash counts as absolute even on Windows, where
	// filepath.IsAbs rejects it for want of a volume. A tool argument carrying
	// a POSIX path is ordinary — a bash run under WSL, a path from a hook
	// payload — and treating it as repo-relative would make it match patterns
	// by a leading path segment the author never wrote.
	if !filepath.IsAbs(filepath.FromSlash(p)) && !strings.HasPrefix(p, "/") {
		return relPathForMatch(p)
	}
	if t.root == "" {
		return p
	}
	// Compare volumes separately: a Windows tool argument arrives as
	// "C:/repo/internal/x.go" and the drive letter would otherwise make a
	// path under the root look like it belongs on some other drive. Both
	// sides lose their volume for the comparison, because filepath.Rel will
	// not relate a volume-less path to a volume-ful one.
	vol := filepath.VolumeName(filepath.FromSlash(p))
	rest := strings.TrimPrefix(p, vol)
	cleanRoot := strings.TrimSuffix(strings.TrimPrefix(t.root, filepath.VolumeName(filepath.FromSlash(t.root))), "/")
	if cleanRoot == "" {
		return p
	}
	// Require the root's own segment boundary, not just a string prefix, so
	// "C:/repository" is not treated as sitting inside "C:/repo".
	if !strings.HasPrefix(rest, cleanRoot+"/") {
		return p
	}
	if rel, err := filepath.Rel(filepath.FromSlash(cleanRoot), filepath.FromSlash(rest)); err == nil {
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return relPathForMatch(filepath.ToSlash(rel))
		}
	}
	return p
}
