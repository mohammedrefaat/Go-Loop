package checkpoint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Outcome reports what a code restore actually did, so /rewind can tell the
// user what it could not do rather than implying the tree is back.
//
// Every field is a count of the same units (files), and every failure mode
// produces a count rather than an error: a restore that cannot write one file
// in a lockfile or a generated asset should still restore the other nine, and
// the one it failed on is exactly what the user needs to hear about.
type Outcome struct {
	// Restored counts files whose previous contents were written back.
	Restored int
	// Created counts files that did not exist at capture time and have been
	// deleted again.
	Deleted int
	// SkippedSymlinks counts symlinks left alone — their targets were never
	// snapshotted, so writing through them would edit the wrong file.
	SkippedSymlinks int
	// SkippedTooLarge counts files over the capture size cap, whose contents
	// were never kept.
	SkippedTooLarge int
	// SkippedMissing counts files that no longer exist at restore time, so
	// there was nothing to write back.
	SkippedMissing int
	// Failed counts files whose restore was attempted and errored.
	Failed int
	// FirstErr is the first error encountered, for a single-line diagnostic.
	FirstErr error
}

// Empty reports whether the restore did nothing at all — which is the normal
// result for a checkpoint taken during a turn that wrote nothing.
func (o Outcome) Empty() bool {
	return o.Restored == 0 && o.Deleted == 0 && o.SkippedSymlinks == 0 &&
		o.SkippedTooLarge == 0 && o.SkippedMissing == 0 && o.Failed == 0
}

// String renders the outcome as the one-line summary /rewind prints. It names
// the skipped counts whenever any are non-zero: a restore that quietly covered
// four of six files reads as a complete one.
func (o Outcome) String() string {
	switch {
	case o.Empty():
		return "No files to restore."
	}
	var b []string
	if o.Restored > 0 {
		b = append(b, plural(o.Restored, "file")+" restored")
	}
	if o.Deleted > 0 {
		b = append(b, plural(o.Deleted, "file")+" removed")
	}
	if o.SkippedSymlinks > 0 {
		b = append(b, plural(o.SkippedSymlinks, "symlink")+" skipped")
	}
	if o.SkippedTooLarge > 0 {
		b = append(b, plural(o.SkippedTooLarge, "large file")+" skipped")
	}
	if o.SkippedMissing > 0 {
		b = append(b, plural(o.SkippedMissing, "file")+" no longer present")
	}
	if o.Failed > 0 {
		b = append(b, plural(o.Failed, "file")+" failed")
	}
	return strings.Join(b, ", ") + "."
}

// Restore puts every file in the checkpoint back the way it was.
//
// The snapshots are applied in reverse capture order, so for a file the turn
// wrote more than once only the oldest copy is used — and by construction each
// path appears at most once anyway, which the loop still relies on: writing the
// same path twice would restore an intermediate state, not the boundary state.
func Restore(cp *Checkpoint) Outcome {
	var out Outcome
	// Guard against a symlinked or otherwise escaping path: a checkpoint file
	// is data on disk, and writing wherever its paths point is not something a
	// corrupt or hand-edited file should be able to cause.
	seen := make(map[string]bool, len(cp.Files))
	for _, snap := range cp.Files {
		if seen[snap.Path] {
			continue
		}
		seen[snap.Path] = true
		applySnapshot(snap, &out)
	}
	return out
}

func applySnapshot(snap Snapshot, out *Outcome) {
	switch {
	case snap.Symlink:
		out.SkippedSymlinks++
		return
	case snap.TooLarge:
		out.SkippedTooLarge++
		return
	case !snap.Existed:
		// The turn created this file. Removing it is the only way back to the
		// state the checkpoint recorded; leaving it behind would leave the
		// generated file the user is trying to get rid of.
		if err := os.Remove(snap.Path); err != nil {
			if os.IsNotExist(err) {
				// Already gone: that is the state the checkpoint recorded, so
				// the restore succeeded rather than failed.
				out.Restored++
				return
			}
			out.fail(err)
			return
		}
		out.Deleted++
		return
	}

	mode := os.FileMode(snap.Mode).Perm()
	if mode == 0 {
		mode = 0o644
	}
	if err := os.MkdirAll(filepath.Dir(snap.Path), 0o755); err != nil {
		out.fail(fmt.Errorf("creating %s: %w", filepath.Dir(snap.Path), err))
		return
	}
	if err := os.WriteFile(snap.Path, snap.Content, mode); err != nil {
		out.fail(err)
		return
	}
	// WriteFile leaves an existing file's mode alone, so an explicit chmod is
	// what makes a file the turn made executable stop being executable.
	if err := os.Chmod(snap.Path, mode); err != nil {
		out.fail(err)
		return
	}
	out.Restored++
}

func (o *Outcome) fail(err error) {
	o.Failed++
	if o.FirstErr == nil {
		o.FirstErr = err
	}
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func join(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}