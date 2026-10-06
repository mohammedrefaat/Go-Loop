package extension

import (
	"path"
	"path/filepath"
	"strings"
)

// MatchesPaths reports whether the skill is relevant to the given set of
// session-touched files.
//
// A skill with no `paths:` frontmatter is always relevant — gating is opt-in,
// so adding it to an existing skill is a deliberate act, and a typo in a
// pattern must not silently remove a skill from every session.
//
// A skill with `paths:` is relevant when *any* of the touched files matches
// *any* of the patterns. One match is enough: the skill is offered and the
// model decides whether to invoke it.
func (s Skill) MatchesPaths(touched []string) bool {
	if len(s.Paths) == 0 {
		return true
	}
	for _, file := range touched {
		if MatchAnyPath(file, s.Paths) {
			return true
		}
	}
	return false
}

// FilterByPaths returns the skills relevant to the given session-touched
// files, preserving order. Skills with no `paths:` frontmatter always pass.
func FilterByPaths(skills []Skill, touched []string) []Skill {
	if len(touched) == 0 {
		// Nothing touched yet — a gated skill has no evidence to be relevant,
		// and offering every skill on turn one is the cost this feature exists
		// to avoid.
		return FilterUngated(skills)
	}
	out := make([]Skill, 0, len(skills))
	for _, s := range skills {
		if s.MatchesPaths(touched) {
			out = append(out, s)
		}
	}
	return out
}

// FilterUngated returns the skills that carry no `paths:` gating, preserving
// order.
func FilterUngated(skills []Skill) []Skill {
	out := make([]Skill, 0, len(skills))
	for _, s := range skills {
		if len(s.Paths) == 0 {
			out = append(out, s)
		}
	}
	return out
}

// MatchAnyPath reports whether path matches any of the patterns. It is
// exported so gating decisions elsewhere (e.g. /doctor) can be tested and
// reused without going through a Skill value.
func MatchAnyPath(p string, patterns []string) bool {
	for _, pat := range patterns {
		if MatchPath(p, pat) {
			return true
		}
	}
	return false
}

// MatchPath matches one repo-relative file path against one pattern.
//
// Patterns are gitignore-flavoured, which is what users already write in
// `.gitignore` and what the spec asks for:
//
//	"internal/tui/**"   every file under internal/tui
//	"internal/tui/*"    direct children only
//	"**/*.go"           every Go file anywhere
//	"*.md"              top-level Markdown only
//	"docs/**"           the docs tree
//
// A pattern with no `/` and no `*` is an exact path match, not a glob — so
// "CLAUDE.md" does not also match "docs/CLAUDE.md".
func MatchPath(p, pattern string) bool {
	pattern = normalizePathPattern(pattern)
	if pattern == "" {
		return false
	}
	// A bare name anchors to the repo root. `.gitignore` would treat "*.md" as
	// matching at any depth; here the patterns describe project files, and an
	// unanchored "*.md" that silently matched every nested file would make the
	// gate far looser than the author intended.
	if !strings.ContainsAny(pattern, "/") {
		return !strings.Contains(p, "/") && matchSegments(p, pattern)
	}
	return matchPathSegments(p, pattern)
}

func matchSegments(name, pattern string) bool {
	ok, err := path.Match(pattern, name)
	return err == nil && ok
}

func matchPathSegments(p, pattern string) bool {
	pParts := strings.Split(p, "/")
	patParts := strings.Split(pattern, "/")
	return matchFrom(pParts, patParts, 0, 0)
}

// matchFrom matches pattern segments starting at patIdx against path segments
// starting at pathIdx. `**` consumes zero or more segments, which is what makes
// "a/**/b" match "a/b" as well as "a/x/y/b".
func matchFrom(pathParts, patParts []string, pathIdx, patIdx int) bool {
	if patIdx == len(patParts) {
		return pathIdx == len(pathParts)
	}
	if patParts[patIdx] == "**" {
		// "**" matches zero segments...
		if matchFrom(pathParts, patParts, pathIdx, patIdx+1) {
			return true
		}
		// ...or one more, as long as segments remain to consume.
		return pathIdx < len(pathParts) && matchFrom(pathParts, patParts, pathIdx+1, patIdx)
	}
	if pathIdx >= len(pathParts) {
		return false
	}
	if !matchSegments(pathParts[pathIdx], patParts[patIdx]) {
		return false
	}
	return matchFrom(pathParts, patParts, pathIdx+1, patIdx+1)
}

func normalizePathPattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	// Backslashes because a Windows-authored keybindings.json or SKILL.md may
	// carry them, and `**\*.go` would otherwise match nothing on a repo whose
	// paths are recorded with forward slashes.
	pattern = strings.ReplaceAll(pattern, "\\", "/")
	pattern = strings.TrimPrefix(pattern, "./")
	// A leading "/" is the same anchoring "." already gives us; without this
	// "/docs/**" would never match "docs/a.md".
	pattern = strings.TrimPrefix(pattern, "/")
	return strings.TrimSuffix(pattern, "/")
}

// relPathForMatch normalizes an absolute or relative file path to the
// repo-relative, forward-slash form the patterns are written against.
func relPathForMatch(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = filepath.ToSlash(p)
	// An absolute path outside the repo cannot be described by a repo-relative
	// pattern, so anchoring it is not a guess.
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
		return p
	}
	return normalizePathPattern(p)
}
