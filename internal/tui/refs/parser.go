package refs

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// RefType represents the type of context reference.
type RefType string

const (
	RefFile   RefType = "file"
	RefFolder RefType = "folder"
	RefDiff   RefType = "diff"
	RefStaged RefType = "staged"
	RefGit    RefType = "git"
	RefURL    RefType = "url"
)

// ParsedRef represents a parsed @ref reference.
type ParsedRef struct {
	Type      RefType
	RawValue  string     // Original @ref string including the type prefix
	Value     string     // Parsed value (path, URL, etc.)
	LineRange *LineRange // For @file:path:start-end
}

// LineRange represents a line range for file references.
type LineRange struct {
	Start int
	End   int
}

// refPattern matches @type optionally followed by :value.
// Type is always captured (group 1). Value is captured only if colon is present (group 2).
var refPattern = regexp.MustCompile(`@([a-z]+)(?::([^\s]*))?`)

// parseRefs extracts all @ref patterns from input.
//
// Two forms are recognised. The typed form names what to read — @file:path,
// @diff, @staged, @git:N, @url:... — and the bare form is a path directly,
// @internal/tui/tui.go, which is the form the TUI advertises and the one users
// type. The typed form is tried first so @file:x stays a file reference rather
// than a path named "file:x"; a typed-looking token that names no known type
// falls through to the bare form, so @diffusion/model.go is a path and not a
// mistyped @diff.
func parseRefs(input string) []ParsedRef {
	var refs []ParsedRef

	// Find all matches of @type[:value] pattern.
	matches := refPattern.FindAllStringSubmatchIndex(input, -1)

	for _, match := range matches {
		// match[0], match[1] = full match start/end
		// match[2], match[3] = type start/end
		// match[4], match[5] = value start/end (may be -1 if no colon)

		refTypeStr := input[match[2]:match[3]]
		rawValue := ""
		if match[4] != -1 && match[5] != -1 {
			rawValue = input[match[4]:match[5]]
		}

		// Strip trailing punctuation from raw value.
		rawValue = strings.TrimRight(rawValue, ",.;!?")

		// Determine the ref type.
		refType := RefType(refTypeStr)

		// Validate known ref types.
		switch refType {
		case RefDiff, RefStaged:
			// These have no additional value.
			rawValue = ""
		case RefFile, RefFolder, RefURL:
			// These need a path/URL value.
			if rawValue == "" {
				continue
			}
		case RefGit:
			// @git:N should have N as value.
			if rawValue == "" {
				continue
			}
		default:
			// Not a known type. Rather than dropping it, try the whole match
			// as a bare path: "@diffusion/model.go" is a file, not a
			// misspelled "@diff", and silently expanding nothing would leave
			// the user staring at an @ that did nothing.
			bare := strings.TrimRight(input[match[0]:match[1]], ",.;!?")
			if ref, ok := ParseBareRef(bare); ok {
				refs = append(refs, ref)
			}
			continue
		}

		ref := ParsedRef{
			Type:     refType,
			RawValue: rawValue,
			Value:    rawValue,
		}

		// Parse line range for @file: references.
		if refType == RefFile {
			ref = parseFileRef(ref)
		}

		refs = append(refs, ref)
	}

	return refs
}

// ParseBareRef resolves a single untyped "@token" into a file reference with
// any :N-M line range split off, reporting whether it was a path at all.
//
// It is exported because the TUI has to find mention tokens itself: expansion
// happens on submit, when the tokens have to be lifted out of the prompt text
// first, and that scan has to agree with what Expand would have accepted.
func ParseBareRef(token string) (ParsedRef, bool) {
	ref := strings.TrimPrefix(token, "@")
	if ref == "" {
		return ParsedRef{}, false
	}
	// A trailing colon with nothing after it is "@path:" — a path being typed,
	// not a reference. Expanding it would attach a file the user has not
	// finished naming.
	if strings.HasSuffix(ref, ":") {
		return ParsedRef{}, false
	}
	parsed := parseFileRef(ParsedRef{Type: RefFile, RawValue: ref, Value: ref})

	// The test is deliberately narrow. "@model" is far more often an @-handle
	// in prose than a file called "model", so a bare token must carry a path
	// signal — a separator, a dot, or a leading drive/root — to count.
	// Anything else is left in the text untouched.
	base := filepath.Base(parsed.Value)
	if !strings.ContainsAny(parsed.Value, `/\`) &&
		!strings.Contains(base, ".") &&
		!filepath.IsAbs(parsed.Value) &&
		!strings.HasPrefix(parsed.Value, "~") {
		return ParsedRef{}, false
	}
	return parsed, true
}

// parseFileRef parses line range from @file:path:start-end format.
func parseFileRef(ref ParsedRef) ParsedRef {
	// Find the line range pattern at the end of the value.
	// We look for :N or :N-M where N and M are numbers.

	value := ref.Value

	// Find the last colon that could be a line range separator.
	colonIdx := -1
	for i := len(value) - 1; i >= 0; i-- {
		if value[i] == ':' {
			colonIdx = i
			break
		}
		// Stop if we hit a path separator (only allow colon for line ranges)
		if value[i] == '/' || value[i] == '\\' {
			break
		}
	}

	if colonIdx < 0 {
		return ref
	}

	// Check if what follows the colon is a line range.
	afterColon := value[colonIdx+1:]

	// Parse start number.
	start, consumed, ok := parseNumber(afterColon)
	if !ok || consumed == 0 {
		return ref
	}

	// Check for range (dash followed by another number).
	end := start
	rest := afterColon[consumed:]
	if len(rest) > 0 && rest[0] == '-' {
		rest = rest[1:]
		endNum, endConsumed, endOk := parseNumber(rest)
		if endOk && endConsumed > 0 {
			end = endNum
			rest = rest[endConsumed:]
		}
	}

	// If we consumed all remaining characters after the number(s),
	// or if the next char is not alphanumeric (path separator), it's a line range.
	if len(rest) == 0 || !isAlphanumericByte(rest[0]) {
		// This is a line range - extract path.
		path := value[:colonIdx]
		if path == "" {
			return ref
		}

		ref.Value = path
		ref.LineRange = &LineRange{Start: start, End: end}
	}

	return ref
}

// parseNumber parses a number from the start of a string.
// Returns the number, how many characters were consumed, and whether parsing succeeded.
func parseNumber(s string) (int, int, bool) {
	if len(s) == 0 {
		return 0, 0, false
	}

	// Collect digits.
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}

	if end == 0 {
		return 0, 0, false
	}

	num, err := strconv.Atoi(s[:end])
	if err != nil {
		return 0, 0, false
	}

	return num, end, true
}

// isAlphanumericByte returns true if the byte is alphanumeric.
func isAlphanumericByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// trailingPunctRe matches trailing punctuation that should be stripped.
var trailingPunctRe = regexp.MustCompile(`[,.;!?]+$`)

// stripTrailingPunctuation removes common trailing punctuation.
func stripTrailingPunctuation(s string) string {
	return trailingPunctRe.ReplaceAllString(s, "")
}
