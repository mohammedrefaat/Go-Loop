package importclaude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dimetron/pi-go/internal/config"
)

// Preview renders the plan as the text a user reads before deciding. It names
// what will be added, what was skipped and why, and what is not carried over —
// a preview that only lists additions leaves the user unable to notice a loss.
func (p Plan) Preview() string {
	var b strings.Builder
	b.WriteString("Import preview — nothing has been written yet.\n")

	if len(p.PermissionRules) > 0 {
		fmt.Fprintf(&b, "\nPermission rules to add (%d):\n", len(p.PermissionRules))
		for _, r := range p.PermissionRules {
			fmt.Fprintf(&b, "  + %s\n", r)
		}
	}

	if len(p.MCPServers) > 0 {
		fmt.Fprintf(&b, "\nMCP servers to add (%d):\n", len(p.MCPServers))
		for _, s := range p.MCPServers {
			fmt.Fprintf(&b, "  + %s: %s\n", s.Name, describeServer(s.Spec))
		}
	}

	if p.Memory != "" {
		fmt.Fprintf(&b, "\nProject instructions:\n  + %s (%d bytes) will be appended to %s\n",
			filepath.Base(p.MemoryPath), len(p.Memory), p.MemoryPath)
	}

	if len(p.SkippedRules) > 0 {
		fmt.Fprintf(&b, "\nNot imported (%d):\n", len(p.SkippedRules))
		for _, s := range p.SkippedRules {
			fmt.Fprintf(&b, "  - %s (%s)\n", s.Rule, s.Reason)
		}
	}

	if len(p.UnmappedKeybindings) > 0 {
		fmt.Fprintf(&b, "\nKeybindings not carried over (%d):\n", len(p.UnmappedKeybindings))
		for _, k := range p.UnmappedKeybindings {
			fmt.Fprintf(&b, "  - %s\n", k)
		}
		b.WriteString("  pi-go has no import mapping for these yet; bind them by hand when the\n")
		b.WriteString("  keybindings workstream lands.\n")
	}

	if len(p.Notes) > 0 {
		b.WriteString("\nNotes:\n")
		for _, n := range p.Notes {
			fmt.Fprintf(&b, "  ! %s\n", n)
		}
	}

	if len(p.PermissionRules) == 0 && len(p.MCPServers) == 0 && p.Memory == "" {
		b.WriteString("\nNothing to import.\n")
	}
	return b.String()
}

func describeServer(s ServerSpec) string {
	if s.URL != "" {
		return s.URL
	}
	if s.Command == "" {
		return "(no command or url)"
	}
	if len(s.Args) == 0 {
		return s.Command
	}
	return s.Command + " " + strings.Join(s.Args, " ")
}

// Apply writes the plan. It is separate from Build so the plan can be shown
// and confirmed first, and so applying twice is detectable rather than
// silently duplicating rules.
//
// Existing content is preserved: rules are appended, not replaced, because an
// import that overwrote a user's hand-written rules would be far worse than
// one that adds a duplicate they can see and delete.
func (p Plan) Apply(cfg config.Config) (config.Config, error) {
	if len(p.PermissionRules) > 0 {
		if cfg.Permissions == nil {
			cfg.Permissions = &config.PermissionConfig{}
		}
		cfg.Permissions.Rules = append(cfg.Permissions.Rules, p.PermissionRules...)
	}
	return cfg, nil
}

// WriteMCPServers writes the planned servers to path, merging with whatever is
// already there. Servers already present under the same name are left alone,
// so applying to a file that has since gained a server does not clobber it.
func (p Plan) WriteMCPServers(path string) error {
	if len(p.MCPServers) == 0 {
		return nil
	}
	var doc struct {
		MCPServers map[string]ServerSpec `json:"mcpServers"`
	}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if doc.MCPServers == nil {
		doc.MCPServers = make(map[string]ServerSpec, len(p.MCPServers))
	}
	for _, s := range p.MCPServers {
		if _, exists := doc.MCPServers[s.Name]; exists {
			continue
		}
		doc.MCPServers[s.Name] = s.Spec
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// AppendMemory adds the imported CLAUDE.md content to the project's AGENTS.md
// under a marked heading, so the user can see it came from an import and
// remove it.
func (p Plan) AppendMemory(agentsPath string) error {
	if p.Memory == "" {
		return nil
	}
	existing, err := os.ReadFile(agentsPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	const marker = "<!-- imported from CLAUDE.md by /import claude -->"
	if strings.Contains(string(existing), marker) {
		// Applying twice would stack copies of the same instructions, and the
		// marker is the only way to tell.
		return nil
	}
	var b strings.Builder
	b.Write(existing)
	if len(existing) > 0 {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\n%s\n\n%s", marker, strings.TrimSpace(p.Memory))
	if err := os.MkdirAll(filepath.Dir(agentsPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(agentsPath, []byte(b.String()), 0o644)
}
