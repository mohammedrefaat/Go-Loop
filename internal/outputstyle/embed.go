package outputstyle

import (
	"embed"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed builtin/*.md
var builtinFS embed.FS

// LoadBuiltin reads one of the styles shipped in the binary. A user style of
// the same name shadows nothing: the two live in different directories and the
// caller decides which to load, so /output-style can offer both.
//
// Lookup is case-insensitive, matching the user directory's Load. macOS and
// Windows filesystems are case-insensitive anyway, so a case-sensitive lookup
// here would make a style reachable on Linux and unreachable everywhere else.
func LoadBuiltin(name string) (Style, error) {
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
	data, err := readBuiltin(name)
	if err != nil {
		return Style{}, errNotBuiltin(name)
	}
	style, err := parse(data, strings.TrimSuffix(name, ".md"))
	if err != nil {
		return Style{}, err
	}
	style.Path = "builtin:" + name
	return style, nil
}

// readBuiltin finds a style file by exact name, then case-insensitively.
//
// The exact attempt first, and the case-insensitive scan only as a fallback:
// a user who writes "Terse" and a style whose frontmatter name is "Terse" both
// resolve, without the scan overriding an exact file that happens to differ in
// case from the frontmatter.
func readBuiltin(name string) ([]byte, error) {
	data, err := builtinFS.ReadFile("builtin/" + name)
	if err == nil {
		return data, nil
	}
	entries, dirErr := fs.ReadDir(builtinFS, "builtin")
	if dirErr == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if strings.EqualFold(e.Name(), name) {
				return builtinFS.ReadFile("builtin/" + e.Name())
			}
		}
	}
	return nil, err
}

// ListBuiltin returns the built-in styles, sorted by name.
func ListBuiltin() []Style {
	entries, err := fs.ReadDir(builtinFS, "builtin")
	if err != nil {
		return nil
	}
	var styles []Style
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		if s, err := LoadBuiltin(entry.Name()); err == nil {
			styles = append(styles, s)
		}
	}
	sort.Slice(styles, func(i, j int) bool { return styles[i].Name < styles[j].Name })
	return styles
}
