// Package importclaude reads a Claude Code installation and produces an
// equivalent pi-go configuration, as a preview the user confirms rather than a
// silent overwrite.
//
// The compatibility the spec leans on is that pi-go already accepts Claude's
// permission-rule syntax and Claude's MCP JSON. So the real work here is not
// translation of formats but reconciliation: deciding what carries over,
// what pi-go has no equivalent for, and what would collide with something the
// user has already configured.
//
// Nothing in this package writes. Producing a plan and applying it are separate
// steps, and the apply step is the caller's, so an import can be inspected
// before it has any effect.
package importclaude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dimetron/pi-go/internal/permission"
)

// Sources is where a Claude Code installation was found.
type Sources struct {
	// Home is ~/.claude, or "" when it does not exist.
	Home string
	// Project is the working directory, checked for .mcp.json and CLAUDE.md.
	Project string
	// ProjectMCP is the .mcp.json found in Project, or "" when there is none.
	ProjectMCP string
	// ProjectMemory is the CLAUDE.md found in Project, or "".
	ProjectMemory string
}

// Find locates the Claude Code files worth importing. A missing directory is
// not an error: a user who has never run Claude Code should get a clear
// "nothing to import", not a stack trace.
func Find(projectDir string) Sources {
	var s Sources
	if home, err := os.UserHomeDir(); err == nil {
		dir := filepath.Join(home, ".claude")
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			s.Home = dir
		}
	}
	if projectDir == "" {
		return s
	}
	s.Project = projectDir
	// Walk up from the project directory, matching how pi-go itself discovers
	// project context, so a monorepo's .mcp.json is found from a subdirectory.
	s.ProjectMCP = findNearestFile(projectDir, ".mcp.json")
	s.ProjectMemory = findNearestFile(projectDir, "CLAUDE.md")
	return s
}

// Found reports whether anything at all was located.
func (s Sources) Found() bool {
	return s.Home != "" || s.ProjectMCP != "" || s.ProjectMemory != ""
}

// Plan is what an import would change, grouped by target. Every field is a
// proposal: applying it is the caller's decision.
type Plan struct {
	// PermissionRules are rules to add to config.json's permissions.rules.
	// Already-present rules are excluded, so re-importing is idempotent.
	PermissionRules []string
	// SkippedRules are rules that were not carried over, each with the reason.
	// Surfacing these is the point of a preview: a user who had four deny
	// rules in Claude and sees one silently dropped needs to know which.
	SkippedRules []Skipped
	// MCPServers are servers to add to mcp.json, keyed by name. Servers whose
	// name already exists are excluded for the same idempotence reason.
	MCPServers []ServerImport
	// Memory is CLAUDE.md content to append to the project's AGENTS.md.
	Memory string
	// MemoryPath is the file the memory would be appended to.
	MemoryPath string
	// Keybindings are Claude keybindings pi-go could not map, listed so the
	// user knows they are not carried over rather than assuming they were.
	UnmappedKeybindings []string
	// Notes are human-readable remarks about the import as a whole.
	Notes []string
}

// Skipped is a rule that was not imported, with the reason.
type Skipped struct {
	Rule   string
	Reason string
}

// ServerImport is one MCP server to add.
type ServerImport struct {
	Name string
	Spec ServerSpec
}

// ServerSpec is a Claude MCP server definition, in Claude's own JSON shape.
type ServerSpec struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	URL     string            `json:"url,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// Type is Claude's transport name: "stdio", "http", "sse". pi-go infers
	// the transport from command-vs-url, so it is read only for a note.
	Type string `json:"type,omitempty"`
}

// claudeSettings mirrors the parts of ~/.claude/settings.json that carry over.
type claudeSettings struct {
	Permissions *struct {
		Allow []string `json:"allow"`
		Ask   []string `json:"ask"`
		Deny  []string `json:"deny"`
	} `json:"permissions"`
}

// claudeKeybindings mirrors ~/.claude/keybindings.json. The shape is a flat
// object of "key chord" → action, or "key chord" → array of them; both appear
// in the wild and neither is documented as stable.
type claudeKeybindings map[string]json.RawMessage

// Build produces the import plan for a located Claude installation. existing
// are the rules and server names pi-go already has, so the plan only proposes
// additions and re-running changes nothing.
func Build(s Sources, existingRules []string, existingServers []string) (Plan, error) {
	var p Plan

	have := make(map[string]bool, len(existingRules))
	for _, r := range existingRules {
		have[strings.TrimSpace(r)] = true
	}
	haveServers := make(map[string]bool, len(existingServers))
	for _, n := range existingServers {
		haveServers[strings.ToLower(strings.TrimSpace(n))] = true
	}

	settings, err := readSettings(s.Home)
	if err != nil {
		p.Notes = append(p.Notes, err.Error())
	}
	addRules(&p, settings, have)

	kbUnmapped, err := readKeybindings(s.Home)
	if err != nil {
		p.Notes = append(p.Notes, err.Error())
	}
	p.UnmappedKeybindings = kbUnmapped

	servers, err := readMCPServers(s)
	if err != nil {
		p.Notes = append(p.Notes, err.Error())
	}
	for _, srv := range servers {
		if haveServers[strings.ToLower(srv.Name)] {
			p.SkippedRules = append(p.SkippedRules, Skipped{
				Rule:   "mcp server " + srv.Name,
				Reason: "pi-go already has a server with this name",
			})
			continue
		}
		p.MCPServers = append(p.MCPServers, srv)
	}

	p.Memory, p.MemoryPath = readMemory(s)
	if p.Memory != "" {
		p.Notes = append(p.Notes,
			"CLAUDE.md is project instructions, not pi-go config. Import appends it to "+
				p.MemoryPath+" — check it does not duplicate a file already there.")
	}
	if s.Home == "" {
		p.Notes = append(p.Notes, "~/.claude not found; only project files were considered.")
	}
	return p, nil
}

// addRules converts Claude's three decision lists into pi-go's single ordered
// list. The ordering is not cosmetic: pi-go evaluates deny → ask → allow
// regardless of position, so emitting them in that order makes the config
// read the way it behaves.
func addRules(p *Plan, settings claudeSettings, have map[string]bool) {
	if settings.Permissions == nil {
		return
	}
	add := func(rules []string, prefix string) {
		for _, r := range rules {
			r = strings.TrimSpace(r)
			if r == "" {
				continue
			}
			// A rule that does not parse would look like a working rule in
			// /doctor while matching no tool, so it is reported rather than
			// imported. This is a check on the syntax alone, deliberately not a
			// check against the tool registry: pi-go has no static list of tool
			// names (CoreTools, the MCP toolsets and the subagent tools are
			// assembled separately), and rejecting a rule here would silently
			// drop real policy for any tool the caller happens to have wired up.
			rule, err := permission.ParseRule(prefix + " " + r)
			if err != nil {
				p.SkippedRules = append(p.SkippedRules, Skipped{Rule: prefix + " " + r, Reason: err.Error()})
				continue
			}
			if reason := ruleToolProblem(rule); reason != "" {
				p.SkippedRules = append(p.SkippedRules, Skipped{Rule: prefix + " " + r, Reason: reason})
				continue
			}
			full := prefix + " " + r
			if have[full] {
				p.SkippedRules = append(p.SkippedRules, Skipped{Rule: full, Reason: "already present"})
				continue
			}
			p.PermissionRules = append(p.PermissionRules, full)
		}
	}
	add(settings.Permissions.Deny, "deny")
	add(settings.Permissions.Ask, "ask")
	add(settings.Permissions.Allow, "allow")
}

// ruleToolProblem reports why a rule that parsed is nonetheless unusable, or
// "" when it is fine.
//
// ParseRule accepts any bare token as a tool name, so the parser alone cannot
// tell a real rule from `deny !!!garbage!!!`. A tool name in pi-go — and in
// Claude Code, whose rules these are — is an identifier: letters, digits, `_`
// and `-`, optionally a `*` or `?` glob or an `mcp__` server prefix. A name
// with other punctuation in it cannot name a tool, and importing it produces a
// rule that silently never matches.
//
// This validates the *shape* of the name, not whether a tool by that name is
// registered: pi-go has no static tool list to check against, and a rule for a
// tool the caller has not enabled is still a valid rule.
func ruleToolProblem(r permission.Rule) string {
	if r.Specifier != nil {
		// A file rule's target is a path pattern, not a tool. Those are matched
		// by path, and anything goes in the parentheses.
		return ""
	}
	name := r.Tool
	if name == "" {
		return "rule names no tool"
	}
	for i, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_', c == '-':
		case c == '*' || c == '?':
			// A glob is a deliberate tool-class wildcard.
		case c >= '0' && c <= '9':
			if i == 0 {
				return fmt.Sprintf("tool name %q starts with a digit", name)
			}
		default:
			return fmt.Sprintf("tool name %q contains %q, which cannot appear in a tool name", name, string(c))
		}
	}
	return ""
}

func readSettings(home string) (claudeSettings, error) {	var s claudeSettings
	if home == "" {
		return s, nil
	}
	path := filepath.Join(home, "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, fmt.Errorf("reading %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &s); err != nil {
		// A malformed settings file is the user's, and overwriting or guessing
		// would be worse than reporting it.
		return s, fmt.Errorf("parsing %s: %w", path, err)
	}
	return s, nil
}

// readKeybindings reports Claude keybindings pi-go has no mapping for.
//
// pi-go's own keybindings land with the keybinding workstream; until then an
// import reports what it could not carry over rather than writing a file that
// nothing reads.
func readKeybindings(home string) ([]string, error) {
	if home == "" {
		return nil, nil
	}
	path := filepath.Join(home, "keybindings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var kb claudeKeybindings
	if err := json.Unmarshal(data, &kb); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	chords := make([]string, 0, len(kb))
	for chord := range kb {
		chords = append(chords, chord)
	}
	// Map order is random; a stable order makes the preview diffable.
	sort.Strings(chords)
	return chords, nil
}

func readMCPServers(s Sources) ([]ServerImport, error) {
	if s.ProjectMCP == "" {
		return nil, nil
	}
	data, err := os.ReadFile(s.ProjectMCP)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.ProjectMCP, err)
	}
	var doc struct {
		MCPServers map[string]ServerSpec `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", s.ProjectMCP, err)
	}
	names := make([]string, 0, len(doc.MCPServers))
	for name := range doc.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]ServerImport, 0, len(names))
	for _, name := range names {
		out = append(out, ServerImport{Name: name, Spec: doc.MCPServers[name]})
	}
	return out, nil
}

func readMemory(s Sources) (content, path string) {
	if s.ProjectMemory == "" {
		return "", ""
	}
	data, err := os.ReadFile(s.ProjectMemory)
	if err != nil {
		return "", ""
	}
	return string(data), s.ProjectMemory
}

func findNearestFile(root, name string) string {
	dir := root
	for {
		candidate := filepath.Join(dir, name)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
