package permission

import (
	"path"
	"path/filepath"
	"strings"
)

// Match reports whether the rule applies to a call of toolName with the
// given argument.
//
// The argument is interpreted per tool, which is why it is a plain string
// here rather than `any`: the tool layer already knows which field of the
// args struct is the interesting one and passes just that.
func (r Rule) Match(toolName, arg string) bool {
	if !r.matchTool(toolName) {
		return false
	}
	if r.Specifier == nil {
		// A bare-name rule matches every call to the tool.
		return true
	}
	return matchSpecifier(*r.Specifier, arg)
}

// matchTool compares the rule's tool against the called tool, honouring
// globs.
//
// The comparison is case-insensitive. pi-go registers lowercase tool names
// (`bash`, `git-hunk`) while permission rules are written the way Claude Code
// spells them (`Bash(ls *)`), so a case-sensitive compare would leave every
// realistic rule silently inert. Only the tool name is case-folded — a
// specifier is a bash command or a file path, where case is meaningful.
func (r Rule) matchTool(toolName string) bool {
	if strings.EqualFold(r.Tool, toolName) {
		return true
	}
	if isToolGlob(r.Tool) {
		return globMatch(strings.ToLower(r.Tool), strings.ToLower(toolName))
	}
	return false
}

// matchSpecifier matches the in-parentheses pattern against a concrete
// argument. `:*` is normalized to a trailing `*` so both spellings behave the
// same, and a pattern with no wildcard at all is compared for equality.
//
// The comparison is anchored at the start: `npm run build` must not match
// `sudo npm run build`. A trailing `*` is the only way to say "and anything
// after", which is what makes `Bash(ls *)` mean the ls family rather than
// every command that happens to end in those letters.
func matchSpecifier(pattern, arg string) bool {
	pattern = strings.TrimSpace(pattern)
	arg = strings.TrimSpace(arg)
	if pattern == "" {
		return true
	}
	if pattern == ":*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		// A pattern of only "*" is the match-everything rule.
		if prefix == "" {
			return true
		}
		// A leading wildcard makes this a free-form glob rather than a
		// "family" prefix rule: "*marker*" must match "echo > marker.txt",
		// not just strings that happen to start with "marker".
		if strings.HasPrefix(pattern, "*") {
			return globMatch(pattern, arg)
		}
		return strings.HasPrefix(arg, prefix)
	}
	if strings.ContainsAny(pattern, "*?") {
		return globMatch(pattern, arg)
	}
	return pattern == arg
}

// globMatch matches a shell-style glob containing `*` and `?`. Implemented
// directly rather than via path/filepath.Match because that function treats
// `/` as a separator, which would stop `mcp__*` from spanning a tool name and
// would make `Bash(go test ./...)` behave unexpectedly.
func globMatch(pattern, s string) bool {
	// Iterative backtracking matcher: linear in the common case, and it
	// handles multiple `*` without recursion, so a pathological pattern
	// cannot blow the stack.
	var (
		p, i      int
		star      = -1
		starMatch int
	)
	for i < len(s) {
		switch {
		case p < len(pattern) && (pattern[p] == '?' || pattern[p] == s[i]):
			p++
			i++
		case p < len(pattern) && pattern[p] == '*':
			star = p
			starMatch = i
			p++
		case star >= 0:
			// Backtrack: let the last `*` absorb one more character.
			p = star + 1
			starMatch++
			i = starMatch
		default:
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}

// MatchPath reports whether the rule's path pattern matches the given file
// path. It is used for file rules, where the specifier is a gitignore-style
// pattern rather than a bash command.
//
// Both separators are normalized so a rule written on Windows matches the
// forward-slash paths the model emits, and vice versa. A bare `secret` or
// `*.env` matches at any depth, which is what a gitignore pattern means; a
// pattern with a slash is anchored to the working directory.
func (r Rule) MatchPath(p string) bool {
	if r.PathPattern == "" {
		return false
	}
	pattern := normalizePath(r.PathPattern)
	clean := normalizePath(p)
	if pattern == "" {
		return false
	}

	// A leading ./ is documentation, not a constraint.
	pattern = strings.TrimPrefix(pattern, "./")
	clean = strings.TrimPrefix(clean, "./")

	if strings.ContainsAny(pattern, "*?[") {
		if globMatch(pattern, clean) {
			return true
		}
		// An unanchored pattern matches at any depth: `*.env` covers
		// `config/.env` the way gitignore does.
		if !strings.Contains(pattern, "/") {
			if ok, _ := path.Match(pattern, path.Base(clean)); ok {
				return true
			}
			// Also try every path suffix, so `internal/tui` matches
			// `~/proj/internal/tui`.
			for rest := clean; ; {
				i := strings.IndexByte(rest, '/')
				if i < 0 {
					break
				}
				rest = rest[i+1:]
				if globMatch(pattern, rest) {
					return true
				}
			}
		}
		return false
	}

	if pattern == clean {
		return true
	}
	// A path pattern matches the path itself or anything under it. A bare
	// name is treated as a directory or a file wherever it appears, so
	// `deny Read(secrets)` covers `secrets/key.pem` as well as `./secrets`.
	if !strings.Contains(pattern, "/") {
		if containsSegment(clean, pattern) {
			return true
		}
		return strings.HasPrefix(clean, pattern+"/")
	}
	return strings.HasPrefix(clean, strings.TrimSuffix(pattern, "/")+"/")
}

// containsSegment reports whether want appears in slash-separated p as a
// whole segment, so `env` matches `a/.env` but not `a/environment`.
func containsSegment(p, want string) bool {
	for rest := p; ; {
		if rest == want {
			return true
		}
		i := strings.IndexByte(rest, '/')
		if i < 0 {
			return false
		}
		rest = rest[i+1:]
	}
}

// normalizePath converts separators to slashes and strips the drive letter so
// that Windows and POSIX paths compare equal.
func normalizePath(p string) string {
	p = filepath.ToSlash(p)
	if len(p) >= 2 && p[1] == ':' {
		p = p[2:]
	}
	return p
}
