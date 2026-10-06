// Package checkpoint records the state of a session at the moment each turn
// begins, so /rewind can put the conversation — and the files the agent wrote
// during the turns after it — back the way they were.
//
// Two things are captured per checkpoint, and they are captured at different
// times on purpose:
//
//   - The conversation position (an event index) is read when the turn starts.
//     Everything after that index belongs to the turn and is dropped on restore.
//
//   - File contents are read *before* each write/edit tool call, from a
//     BeforeToolCallback. Snapshotting afterwards would save the bytes the
//     agent just produced, and restoring them would change nothing.
//
// What is deliberately not captured is the rest of what can change files. Bash
// commands, and edits made by background subagents, never pass through this
// tool seam, so their effects survive a code restore. That limit is the same
// one the feature documents, and it falls out of the design rather than being
// enforced by inspection: there is no observation point that sees them.
package checkpoint

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Defaults for the retention policy. The count is the number of checkpoints
// kept per session; the period is how old a checkpoint must be before the
// sweep removes it.
const (
	DefaultKeep            = 100
	DefaultCleanupPeriod   = 30 * 24 * time.Hour
	maxSnapshotBytes int64 = 1 << 20 // 1 MiB per file
)

// maxLabelRunes bounds a checkpoint's label. The label is the user's prompt,
// rendered into a picker row and (via List) back to a caller that may print it;
// a multi-kilobyte prompt must not become an unreadable row.
const maxLabelRunes = 120

// Snapshot is one file's contents as they were before the turn changed them.
//
// Existed distinguishes "the file was empty" from "the file was not there", and
// the difference decides what a restore does: rewrite the bytes, or delete the
// file the turn created.
type Snapshot struct {
	Path string `json:"path"`
	// Existed reports whether the file was present at capture time.
	Existed bool `json:"existed"`
	// Mode is the permission bits to restore, masked to what a file can carry.
	Mode uint32 `json:"mode,omitempty"`
	// Content is the pre-turn bytes. Nil when the file did not exist, and also
	// when TooLarge is set.
	Content []byte `json:"content,omitempty"`
	// TooLarge marks a file that was over maxSnapshotBytes at capture time. Its
	// content was not kept, so a restore leaves it alone rather than truncating
	// a source file to nothing.
	TooLarge bool `json:"tooLarge,omitempty"`
	// Symlink marks a path that was a symlink when captured. The link target is
	// never followed and never rewritten; a restore skips it and reports the
	// count, because writing through a link would edit the file it points at.
	Symlink bool `json:"symlink,omitempty"`
}

// Checkpoint is one turn boundary: where the conversation stood, and what the
// files it went on to touch looked like beforehand.
type Checkpoint struct {
	// ID sorts by time and is unique per session, so List can order by
	// filename alone.
	ID         string     `json:"id"`
	EventIndex int        `json:"eventIndex"`
	Label      string     `json:"label,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	Files      []Snapshot `json:"files,omitempty"`
	// SkippedSymlinks counts symlinks seen during the turn that were not
	// snapshotted. Carried on the checkpoint so /rewind can name the number
	// rather than silently restoring fewer files than it appears to.
	SkippedSymlinks int `json:"skippedSymlinks,omitempty"`
	// SkippedTooLarge counts files over the per-file size cap.
	SkippedTooLarge int `json:"skippedTooLarge,omitempty"`
}

// dirName is the per-session checkpoint directory, under the session's own
// directory so a session's history travels with it through archive and export.
const dirName = "checkpoints"

// Manager owns the checkpoints of every session in the process and the set of
// checkpoints currently accumulating file snapshots.
//
// One Manager is shared by the TUI and the tool-callback chain, which run on
// different goroutines, so all of its state is behind a mutex.
type Manager struct {
	mu     sync.Mutex
	keep   int
	period time.Duration
	now    func() time.Time

	// open maps session ID to the checkpoint currently collecting file
	// snapshots. A turn with no checkpoint open records nothing: captures
	// outside a turn are writes with no boundary to rewind to, and dropping
	// them is the only safe reading.
	open map[string]*Checkpoint
}

// Option configures a Manager. The zero Manager is not usable; build one with
// New.
type Option func(*Manager)

// WithKeep overrides how many checkpoints are kept per session. A value below 1
// is ignored, so a misconfigured zero cannot silently disable the feature.
func WithKeep(n int) Option {
	return func(m *Manager) {
		if n > 0 {
			m.keep = n
		}
	}
}

// WithCleanupPeriod overrides how old a checkpoint must be before the sweep
// removes it.
func WithCleanupPeriod(d time.Duration) Option {
	return func(m *Manager) {
		if d > 0 {
			m.period = d
		}
	}
}

// withClock replaces the clock, so retention tests do not sleep.
func withClock(now func() time.Time) Option {
	return func(m *Manager) { m.now = now }
}

// New builds a Manager. With no options it keeps 100 checkpoints per session
// and sweeps anything older than 30 days.
func New(opts ...Option) *Manager {
	m := &Manager{
		keep:   DefaultKeep,
		period: DefaultCleanupPeriod,
		now:    time.Now,
		open:   make(map[string]*Checkpoint),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// dir returns a session's checkpoint directory.
func dir(sessionDir string) string { return filepath.Join(sessionDir, dirName) }

// Begin opens a new checkpoint for a session, standing at eventIndex and
// labelled by the prompt that is about to run.
//
// It closes any checkpoint still open for the session — a turn that was
// interrupted without ending still has its own boundary, and letting two
// accumulate into one file would make the earlier one's index a lie.
//
// The retention sweep runs here rather than on a timer: a turn is the only
// moment the process is reliably doing session work, and a timer would keep a
// goroutine alive for a feature most sessions never use.
func (m *Manager) Begin(sessionDir string, eventIndex int, label string) (*Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := os.MkdirAll(dir(sessionDir), 0o700); err != nil {
		return nil, fmt.Errorf("creating checkpoint directory: %w", err)
	}
	m.sweepLocked(sessionDir)

	cp := &Checkpoint{
		ID:         newID(m.now()),
		EventIndex: eventIndex,
		Label:      truncateLabel(label),
		CreatedAt:  m.now(),
	}
	if err := m.write(sessionDir, cp); err != nil {
		return nil, err
	}

	key := sessionKey(sessionDir)
	m.open[key] = cp
	m.trimLocked(sessionDir)
	return cp, nil
}

// Capture records a file's pre-turn contents into the session's open
// checkpoint.
//
// A path is captured once per checkpoint no matter how many times the turn
// writes it: restoring must undo the whole turn, so the bytes that matter are
// the ones from before the turn started, not the ones before its last edit.
//
// A capture with no open checkpoint is a no-op, and so is a re-capture. Both
// return nil because neither is an error worth surfacing to a turn that is
// otherwise fine.
func (m *Manager) Capture(sessionDir, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := sessionKey(sessionDir)
	cp := m.open[key]
	if cp == nil {
		return nil
	}
	for _, snap := range cp.Files {
		if snap.Path == path {
			return nil
		}
	}

	snap := snapshotFile(path)
	if snap.Symlink {
		cp.SkippedSymlinks++
	}
	if snap.TooLarge {
		cp.SkippedTooLarge++
	}
	cp.Files = append(cp.Files, snap)
	return m.write(sessionDir, cp)
}

// End closes the session's open checkpoint, so a later tool call cannot append
// to it. A turn's boundary is its prompt, not its last write.
func (m *Manager) End(sessionDir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.open, sessionKey(sessionDir))
}

// Open reports whether a checkpoint is currently accumulating for a session.
// The TUI's Esc-Esc guard uses it: rewinding mid-turn would restore a file the
// running turn is about to write again.
func (m *Manager) Open(sessionDir string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.open[sessionKey(sessionDir)] != nil
}

// List returns a session's checkpoints, newest first.
func (m *Manager) List(sessionDir string) ([]Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listLocked(sessionDir)
}

func (m *Manager) listLocked(sessionDir string) ([]Checkpoint, error) {
	entries, err := os.ReadDir(dir(sessionDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading checkpoints: %w", err)
	}
	out := make([]Checkpoint, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		cp, err := readCheckpoint(filepath.Join(dir(sessionDir), entry.Name()))
		if err != nil {
			// One unreadable checkpoint must not hide the rest of the history,
			// and a picker that silently omits a row the user remembers is
			// worse than one that logs the reason.
			continue
		}
		out = append(out, *cp)
	}
	// IDs begin with a sortable timestamp, so the name order is the time
	// order; the sort makes that a stated property rather than an accident of
	// the ID format.
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// write persists a checkpoint atomically. The capture path rewrites the file on
// every write and edit tool call, so a torn read would be a real failure mode
// for a picker running against the same directory.
func (m *Manager) write(sessionDir string, cp *Checkpoint) error {
	data, err := json.Marshal(cp)
	if err != nil {
		return fmt.Errorf("encoding checkpoint: %w", err)
	}
	if err := os.MkdirAll(dir(sessionDir), 0o700); err != nil {
		return fmt.Errorf("creating checkpoint directory: %w", err)
	}
	path := filepath.Join(dir(sessionDir), cp.ID+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing checkpoint: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("committing checkpoint: %w", err)
	}
	return nil
}

// trimLocked enforces the per-session count, dropping the oldest first.
func (m *Manager) trimLocked(sessionDir string) {
	cps, err := m.listLocked(sessionDir)
	if err != nil || len(cps) <= m.keep {
		return
	}
	for _, cp := range cps[m.keep:] {
		os.Remove(filepath.Join(dir(sessionDir), cp.ID+".json"))
	}
}

// sweepLocked removes checkpoints older than the cleanup period. The count
// cap alone would keep a hundred checkpoints forever in a session used once a
// year; the period is what bounds the disk a long-lived install accumulates.
func (m *Manager) sweepLocked(sessionDir string) {
	cps, err := m.listLocked(sessionDir)
	if err != nil {
		return
	}
	cutoff := m.now().Add(-m.period)
	for _, cp := range cps {
		if cp.CreatedAt.Before(cutoff) {
			os.Remove(filepath.Join(dir(sessionDir), cp.ID+".json"))
		}
	}
}

// Get reads one checkpoint by ID.
func Get(sessionDir, id string) (*Checkpoint, error) {
	if strings.ContainsAny(id, `/\`) || id == "" {
		// The ID comes from a picker and, via the command line, from the user.
		// A path separator in it would read a file outside the session.
		return nil, fmt.Errorf("invalid checkpoint id %q", id)
	}
	return readCheckpoint(filepath.Join(dir(sessionDir), id+".json"))
}

func readCheckpoint(path string) (*Checkpoint, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is built from a validated id
	if err != nil {
		return nil, err
	}
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, err
	}
	return &cp, nil
}

// snapshotFile reads a path's current state. Lstat rather than Stat, because
// following a link would snapshot — and a restore would later rewrite — the
// file the link points at, which is not the file the agent edited.
func snapshotFile(path string) Snapshot {
	snap := Snapshot{Path: path}
	info, err := os.Lstat(path)
	if err != nil {
		return snap // Existed stays false: the write will create the file.
	}
	if info.Mode()&os.ModeSymlink != 0 {
		snap.Symlink = true
		return snap
	}
	if info.IsDir() {
		// A tool that wrote a path that is a directory failed; there is no file
		// state to keep.
		return snap
	}
	snap.Existed = true
	snap.Mode = uint32(info.Mode().Perm())
	if info.Size() > maxSnapshotBytes {
		snap.TooLarge = true
		return snap
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is the file the tool is about to write
	if err != nil {
		// Unreadable now: record it as present-but-empty rather than absent, so
		// a restore does not delete a file it merely could not read.
		snap.TooLarge = true
		return snap
	}
	snap.Content = data
	return snap
}

// newID builds a sortable unique ID: the timestamp first, so lexicographic
// order is chronological, then a random suffix for uniqueness within a second.
// Two prompts in the same millisecond are possible — a scripted run — and
// without the suffix the second would overwrite the first.
func newID(now time.Time) string {
	return fmt.Sprintf("%s-%04x", now.UTC().Format("20060102-150405.000"), randomSuffix())
}

func randomSuffix() uint16 {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		// The randomness is only disambiguating two checkpoints written in the
		// same millisecond; the timestamp still orders them. A failure here is
		// not worth failing a turn over.
		return uint16(time.Now().UnixNano())
	}
	return binary.BigEndian.Uint16(b[:])
}

// truncateLabel folds a prompt to one readable picker row. Control characters
// become spaces because the label round-trips through the terminal; the length
// cap is in runes so a multi-byte prompt is cut at a character boundary rather
// than mid-rune.
func truncateLabel(label string) string {
	label = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, label)
	label = strings.TrimSpace(label)
	if utf8.RuneCountInString(label) <= maxLabelRunes {
		return label
	}
	runes := []rune(label)
	return strings.TrimSpace(string(runes[:maxLabelRunes])) + "…"
}

// sessionKey normalizes a session directory so a Manager opened on
// "…/sess" and one opened on "…/sess/" agree on which session is meant.
func sessionKey(sessionDir string) string {
	return filepath.Clean(sessionDir)
}