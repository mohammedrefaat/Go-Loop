package extension

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestTouchedFilesNoteNormalizesAndDedupes(t *testing.T) {
	tr := NewTouchedFiles()
	tr.Note("internal/tui/tui.go")
	tr.Note("internal/tui/tui.go")
	tr.Note("./internal/tools/a.go")
	tr.Note("")
	got := tr.Files()
	if len(got) != 2 {
		t.Fatalf("Files() = %v, want 2 distinct entries", got)
	}
	if got[0] != "internal/tui/tui.go" || got[1] != "internal/tools/a.go" {
		t.Errorf("Files() = %v, want [internal/tui/tui.go internal/tools/a.go]", got)
	}
}

func TestTouchedFilesNoteKeepsInsertionOrder(t *testing.T) {
	tr := NewTouchedFiles()
	tr.Note("a.go")
	tr.Note("b.go")
	tr.Note("a.go") // repeat must not reorder
	files := tr.Files()
	if files[0] != "a.go" || files[1] != "b.go" {
		t.Errorf("Files() = %v, want insertion order [a.go b.go]", files)
	}
}

func TestTouchedFilesNoteIsBounded(t *testing.T) {
	tr := &TouchedFiles{seen: make(map[string]bool), limit: 3}
	tr.Note("a")
	tr.Note("b")
	tr.Note("c")
	tr.Note("d") // dropped, not a replacement for "a"
	if tr.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", tr.Len())
	}
	files := tr.Files()
	if files[0] != "a" {
		t.Errorf("Files()[0] = %q, want the first touched file to survive", files[0])
	}
}

func TestTouchedFilesFilesReturnsACopy(t *testing.T) {
	tr := NewTouchedFiles()
	tr.Note("a.go")
	files := tr.Files()
	files[0] = "mutated"
	if tr.Files()[0] != "a.go" {
		t.Error("Files() must return a copy; a caller mutating it must not corrupt the tracker")
	}
}

func TestTouchedFilesNilIsUsable(t *testing.T) {
	var tr *TouchedFiles
	tr.Note("a.go") // must not panic
	tr.NoteAll(nil) // must not panic
	tr.Reset()      // must not panic
	if tr.Len() != 0 || tr.Files() != nil {
		t.Error("a nil tracker is empty")
	}
	skills := []Skill{{Name: "always"}, {Name: "tui", Paths: []string{"internal/tui/**"}}}
	got := tr.Filter(skills)
	if len(got) != 1 || got[0].Name != "always" {
		t.Errorf("Filter() = %v, want just the ungated skill", got)
	}
}

func TestTouchedFilesReset(t *testing.T) {
	tr := NewTouchedFiles()
	tr.Note("a.go")
	tr.Reset()
	if tr.Len() != 0 {
		t.Errorf("Len() after Reset = %d, want 0", tr.Len())
	}
	tr.Note("a.go")
	if tr.Len() != 1 {
		t.Errorf("Len() = %d after re-noting post-Reset, want 1", tr.Len())
	}
}

func TestTouchedFilesNoteAll(t *testing.T) {
	tr := NewTouchedFiles()
	tr.NoteAll([]string{"a.go", "b.go", "a.go"})
	if tr.Len() != 2 {
		t.Errorf("Len() = %d, want 2", tr.Len())
	}
}

func TestTouchedFilesConcurrentNote(t *testing.T) {
	tr := NewTouchedFiles()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.Note("internal/tui/tui.go")
			_ = tr.Files()
		}()
	}
	wg.Wait()
	if tr.Len() != 1 {
		t.Errorf("Len() = %d, want 1 under concurrent Note", tr.Len())
	}
}

func TestTouchedFilesFilterOpensGate(t *testing.T) {
	tr := NewTouchedFiles()
	skills := []Skill{{Name: "always"}, {Name: "tui", Paths: []string{"internal/tui/**"}}}
	if got := tr.Filter(skills); len(got) != 1 {
		t.Fatalf("before any Note, Filter() = %v, want the ungated skill only", got)
	}
	tr.Note("internal/tui/tui.go")
	if got := tr.Filter(skills); len(got) != 2 {
		t.Errorf("after touching a matching file, Filter() = %v, want both skills", got)
	}
}

func TestTouchedFilesAbsolutePathUnderRootBecomesRelative(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo")
	tr := NewTouchedFilesIn(root)
	tr.Note(filepath.ToSlash(filepath.Join(root, "internal", "tui", "tui.go")))
	files := tr.Files()
	if len(files) != 1 || files[0] != "internal/tui/tui.go" {
		t.Errorf("Files() = %v, want [internal/tui/tui.go]", files)
	}
}

func TestTouchedFilesAbsolutePathOutsideRootStaysAbsolute(t *testing.T) {
	// /etc/passwd has no repo-relative form. Rewriting it as "etc/passwd"
	// would let a pattern like "etc/**" gate on it, which the author never
	// wrote and cannot reason about.
	tr := NewTouchedFilesIn(filepath.Join(string(filepath.Separator), "repo"))
	tr.Note("/etc/passwd")
	files := tr.Files()
	if len(files) != 1 || files[0] != "/etc/passwd" {
		t.Errorf("Files() = %v, want [/etc/passwd]", files)
	}
}

func TestTouchedFilesAbsolutePathWithNoRootStaysAbsolute(t *testing.T) {
	// Without a root there is nothing to be relative to, so the path is kept
	// and simply never matches a repo-relative pattern.
	tr := NewTouchedFiles()
	tr.Note("/repo/internal/tui/tui.go")
	files := tr.Files()
	if len(files) != 1 || files[0] != "/repo/internal/tui/tui.go" {
		t.Errorf("Files() = %v, want the path kept absolute", files)
	}
}

func TestTouchedFilesSiblingWithSharedPrefixIsNotRelativized(t *testing.T) {
	// "C:/repository" must not be treated as sitting inside "C:/repo" just
	// because the string starts with the root's characters.
	root := filepath.Join(string(filepath.Separator), "repo")
	tr := NewTouchedFilesIn(root)
	outside := filepath.ToSlash(filepath.Join(string(filepath.Separator), "repository", "a.go"))
	tr.Note(outside)
	files := tr.Files()
	if len(files) != 1 || files[0] != outside {
		t.Errorf("Files() = %v, want %v kept absolute", files, outside)
	}
}

func TestTouchedFilesNoteWindowsStylePath(t *testing.T) {
	// A Windows tool argument arrives with a drive letter; the gate patterns
	// are written repo-relative, so the volume must come off.
	tr := NewTouchedFilesIn(`C:\repo`)
	tr.Note(`C:\repo\internal\tui\tui.go`)
	files := tr.Files()
	if len(files) != 1 {
		t.Fatalf("Files() = %v, want 1 entry", files)
	}
	if !MatchPath(files[0], "internal/tui/**") {
		t.Errorf("Files()[0] = %q, which does not match internal/tui/**", files[0])
	}
}
