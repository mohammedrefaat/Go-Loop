// Package outputstyle loads user-authored instruction sets that reshape how
// the agent responds — a terse reviewer's style, a documentation voice, a
// commit-message-only register.
//
// An output style is *not* a role. A role picks a model (internal/config
// RoleConfig: model, provider, advisor settings); an output style picks
// instructions. They compose: --output-style sets the voice, --slow picks the
// model, and neither displaces the other. When both are set, the explicit
// --output-style wins over a style implied by a role, because a role that
// merely implies a voice is the weaker statement.
package outputstyle

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Style is one loaded output style.
type Style struct {
	// Name is the style's identifier, derived from the file name.
	Name string
	// Description is a one-line summary from frontmatter, shown in the picker.
	Description string
	// Instruction is the markdown body — the text prepended to the built-in
	// system prompt.
	Instruction string
	// Path is the file it came from, for diagnostics.
	Path string
}

// Dir returns the user-level output style directory, ~/.pi-go/output-styles.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pi-go", "output-styles"), nil
}

// errEmptyName and errPath are shared with the built-in loader so both report
// the same way for the same mistake.
var (
	errEmptyName = errors.New("output style name is empty")
)

// errPath rejects a name carrying a separator. The name selects a file inside a
// known directory; letting it walk out would read an arbitrary markdown file
// and inject it as system instructions.
func errPath(name string) error {
	return fmt.Errorf("output style %q must be a name, not a path", name)
}

// errNotBuiltin is returned when a name names no shipped style.
func errNotBuiltin(name string) error {
	return fmt.Errorf("no built-in output style named %q", name)
}

// Load reads one style by name from a directory. A name may be given with or
// without the .md extension, because a user reading a filename in a terminal
// will type it either way and the difference is not worth an error.
func Load(dir, name string) (Style, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Style{}, errEmptyName
	}
	if strings.ContainsAny(name, `/\`) {
		return Style{}, errPath(name)
	}
	// EqualFold, not HasSuffix: "TERSE.MD" already carries the extension, and a
	// case-sensitive check would append a second one.
	if !strings.EqualFold(filepath.Ext(name), ".md") {
		name += ".md"
	}
	path := filepath.Join(dir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		return Style{}, fmt.Errorf("reading output style %q: %w", name, err)
	}
	style, err := parse(data, strings.TrimSuffix(filepath.Base(path), ".md"))
	if err != nil {
		return Style{}, fmt.Errorf("parsing output style %q: %w", name, err)
	}
	style.Path = path
	return style, nil
}

// List returns every style in dir, sorted by name, skipping files that do not
// parse — one broken style should not hide the rest of the directory.
func List(dir string) ([]Style, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			// A user who has never written a style has no directory, and that
			// is not an error worth surfacing at startup.
			return nil, nil
		}
		return nil, err
	}
	var styles []Style
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		s, err := Load(dir, entry.Name())
		if err != nil {
			continue
		}
		styles = append(styles, s)
	}
	sort.Slice(styles, func(i, j int) bool { return styles[i].Name < styles[j].Name })
	return styles, nil
}

// parse splits optional frontmatter from the markdown body. The frontmatter
// reader is deliberately the same line-oriented shape the skill parser uses, so
// a user who has written one file format has learned the other.
func parse(data []byte, fallbackName string) (Style, error) {
	style := Style{Name: fallbackName}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	inFrontmatter := false
	frontmatterDone := false
	var body strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if !frontmatterDone && strings.TrimSpace(line) == "---" {
			inFrontmatter = !inFrontmatter
			frontmatterDone = !inFrontmatter
			continue
		}
		if !inFrontmatter {
			body.WriteString(line)
			body.WriteString("\n")
			continue
		}
		if key, value, ok := splitFrontmatter(line); ok {
			switch key {
			case "name":
				style.Name = value
			case "description":
				style.Description = value
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Style{}, err
	}
	style.Instruction = strings.TrimSpace(body.String())
	if style.Name == "" {
		style.Name = fallbackName
	}
	return style, nil
}

func splitFrontmatter(line string) (key, value string, ok bool) {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}
