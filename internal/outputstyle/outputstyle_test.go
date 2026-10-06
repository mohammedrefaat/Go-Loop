package outputstyle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeStyle(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadParsesFrontmatterAndBody(t *testing.T) {
	dir := t.TempDir()
	writeStyle(t, dir, "terse.md", "---\nname: Terse\ndescription: Short answers\n---\nAnswer in one line.\n")

	s, err := Load(dir, "terse")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Name != "Terse" {
		t.Errorf("Name = %q, want %q", s.Name, "Terse")
	}
	if s.Description != "Short answers" {
		t.Errorf("Description = %q, want %q", s.Description, "Short answers")
	}
	if s.Instruction != "Answer in one line." {
		t.Errorf("Instruction = %q, want %q", s.Instruction, "Answer in one line.")
	}
}

func TestLoadAcceptsNameWithOrWithoutExtension(t *testing.T) {
	dir := t.TempDir()
	writeStyle(t, dir, "terse.md", "body")

	for _, name := range []string{"terse", "terse.md"} {
		if _, err := Load(dir, name); err != nil {
			t.Errorf("Load(%q): %v", name, err)
		}
	}
}

func TestLoadFallsBackToFileName(t *testing.T) {
	dir := t.TempDir()
	writeStyle(t, dir, "terse.md", "no frontmatter here")
	s, err := Load(dir, "terse")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Name != "terse" {
		t.Errorf("Name = %q, want the file name %q", s.Name, "terse")
	}
}

func TestLoadEmptyFrontmatterNameFallsBackToFileName(t *testing.T) {
	// "name:" parses to an empty value, and a style with no name would render
	// a prompt with a blank label and be unselectable in a picker.
	dir := t.TempDir()
	writeStyle(t, dir, "terse.md", "---\nname:\ndescription: d\n---\nbody")
	s, err := Load(dir, "terse")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Name != "terse" {
		t.Errorf("Name = %q, want %q", s.Name, "terse")
	}
}

func TestLoadNoFrontmatterKeepsWholeFileAsBody(t *testing.T) {
	// A file that opens with "---" as a horizontal rule rather than a
	// frontmatter fence would otherwise swallow the first line. The parser
	// treats the first "---" as opening, so the body starts at the second.
	dir := t.TempDir()
	writeStyle(t, dir, "plain.md", "Just instructions, no frontmatter.")
	s, err := Load(dir, "plain")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(s.Instruction, "Just instructions") {
		t.Errorf("Instruction = %q, want the whole file as body", s.Instruction)
	}
}

func TestLoadRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "..", "secret.md")
	writeStyle(t, filepath.Dir(outside), "secret.md", "arbitrary file")

	for _, name := range []string{"../secret", "..\\secret", "sub/terse"} {
		if _, err := Load(dir, name); err == nil {
			t.Errorf("Load(%q) succeeded; a style name must not escape its directory", name)
		}
	}
}

func TestLoadErrorsOnMissingAndEmpty(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir, "nope"); err == nil {
		t.Error("Load of a missing style should error")
	}
	if _, err := Load(dir, "  "); err == nil {
		t.Error("Load of an empty name should error")
	}
}

func TestLoadTrimsBodyWhitespace(t *testing.T) {
	dir := t.TempDir()
	writeStyle(t, dir, "terse.md", "---\nname: t\n---\n\n\nbody\n\n\n")
	s, err := Load(dir, "terse")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Instruction != "body" {
		t.Errorf("Instruction = %q, want %q", s.Instruction, "body")
	}
}

func TestListSortsAndSkipsNonMarkdown(t *testing.T) {
	dir := t.TempDir()
	writeStyle(t, dir, "zulu.md", "z")
	writeStyle(t, dir, "alpha.md", "a")
	writeStyle(t, dir, "notes.txt", "ignored")
	if err := os.MkdirAll(filepath.Join(dir, "subdir.md"), 0o755); err != nil {
		t.Fatal(err)
	}

	styles, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(styles) != 2 {
		t.Fatalf("got %d styles, want 2", len(styles))
	}
	if styles[0].Name != "alpha" || styles[1].Name != "zulu" {
		t.Errorf("got order %q,%q want alpha,zulu", styles[0].Name, styles[1].Name)
	}
}

func TestListOnMissingDirectoryIsNotAnError(t *testing.T) {
	// A user who has never written a style has no directory; failing at startup
	// for that would make output styles a tax on everyone else.
	styles, err := List(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(styles) != 0 {
		t.Errorf("got %d styles, want 0", len(styles))
	}
}

func TestPromptWrapsTheInstruction(t *testing.T) {
	s := Style{Name: "Terse", Instruction: "One line per answer."}
	got := s.Prompt()
	if !strings.Contains(got, "One line per answer.") {
		t.Errorf("Prompt() = %q, want the instruction", got)
	}
	if !strings.Contains(got, "Terse") {
		t.Errorf("Prompt() = %q, want the style name", got)
	}
	if !strings.HasPrefix(got, "\n\n") {
		t.Errorf("Prompt() = %q, want a leading blank-line separator so it joins a prompt cleanly", got)
	}
}

func TestPromptOnEmptyInstructionIsEmpty(t *testing.T) {
	// A style file holding only frontmatter contributes nothing; emitting a
	// bare heading would put "# Output Style" in the prompt with no content.
	for _, s := range []Style{
		{Name: "empty"},
		{Name: "blank", Instruction: "   \n\t "},
	} {
		if got := s.Prompt(); got != "" {
			t.Errorf("Prompt() = %q, want empty for %q", got, s.Name)
		}
	}
}
