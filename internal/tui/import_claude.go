package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/importclaude"
)

// pendingClaudeImport holds a built import plan while it waits for the user to
// confirm. The plan is built up front and applied only on Enter, so a preview
// the user walks away from leaves nothing behind — the whole point of the
// preview.
type pendingClaudeImport struct {
	plan importclaude.Plan
}

// handleImportClaudeCommand previews a Claude Code import and asks before
// writing anything.
//
// The two-step shape is deliberate. An import touches three separate files
// (config.json, mcp.json, AGENTS.md) and a user who cannot see the plan has no
// way to judge whether the merge is safe, so the answer comes first.
func (m *model) handleImportClaudeCommand(args []string) (tea.Model, tea.Cmd) {
	m.inputModel.Clear()

	if len(args) != 1 || !strings.EqualFold(args[0], "claude") {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "Usage: `/import claude`\n\nPreviews what would be carried over from a Claude Code installation, then asks before writing it.",
		})
		return m, nil
	}

	sources := importclaude.Find(m.cwd())

	if !sources.Found() {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "No Claude Code installation found.\n\nLooked for `~/.claude` (settings and keybindings) and `.mcp.json` / `CLAUDE.md` up from this directory. Nothing there to import.",
		})
		return m, nil
	}

	// The plan is built against what pi-go already has, so re-running the
	// command proposes nothing rather than duplicating rules. The rules come
	// from disk rather than m.cfg: the TUI config does not carry them, and the
	// file is what an import will be merged into.
	current, err := config.Load()
	if err != nil {
		current = config.Defaults()
	}
	plan, err := importclaude.Build(sources, existingPermissionRules(&current), existingMCPServerNames(m.cfg.MCPServers))
	if err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Import failed: %v", err),
		})
		return m, nil
	}

	if len(plan.PermissionRules) == 0 && len(plan.MCPServers) == 0 && plan.Memory == "" {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "Nothing to import — pi-go already has everything in this Claude Code configuration.",
		})
		return m, nil
	}

	m.pendingClaudeImport = &pendingClaudeImport{plan: plan}
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role: "assistant",
		content: "```\n" + strings.TrimRight(plan.Preview(), "\n") + "\n```\n\n" +
			"Press **Enter** to import, **Esc** to cancel.",
	})
	return m, nil
}

// handleImportClaudeConfirm applies the previewed plan.
//
// The config is reloaded from disk before the merge rather than reusing a
// cached copy: the loaded config has project-level overrides and merged MCP
// servers folded in, and saving that back would flatten a project config into
// the global one.
func (m *model) handleImportClaudeConfirm() (tea.Model, tea.Cmd) {
	p := m.pendingClaudeImport
	m.pendingClaudeImport = nil
	if p == nil {
		return m, nil
	}

	// Load fresh from disk: the loaded config has project-level overrides and
	// merged MCP servers folded in, and saving that back would flatten a
	// project config into the global one.
	cfg, err := config.Load()

	if err != nil {
		cfg = config.Defaults()
	}
	updated, err := p.plan.Apply(cfg)
	if err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Import failed: %v", err),
		})
		return m, nil
	}
	if len(p.plan.PermissionRules) > 0 {
		if err := updated.Save(); err != nil {
			m.chatModel.Messages = append(m.chatModel.Messages, message{
				role:    "assistant",
				content: fmt.Sprintf("Could not save permission rules: %v", err),
			})
			return m, nil
		}
	}

	var written []string
	if err := p.plan.WriteMCPServers(mcpConfigPath(m.cwd())); err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Rules imported, but MCP servers could not be written: %v", err),
		})
		return m, nil
	}
	if len(p.plan.MCPServers) > 0 {
		written = append(written, fmt.Sprintf("%d MCP server(s) to %s", len(p.plan.MCPServers), mcpConfigPath(m.cwd())))
	}
	if p.plan.Memory != "" {
		if err := p.plan.AppendMemory(p.plan.MemoryPath); err != nil {
			m.chatModel.Messages = append(m.chatModel.Messages, message{
				role:    "assistant",
				content: fmt.Sprintf("Rules imported, but project instructions could not be written: %v", err),
			})
			return m, nil
		}
		written = append(written, "project instructions appended")
	}

	summary := fmt.Sprintf("Imported %d permission rule(s).", len(p.plan.PermissionRules))
	if len(written) > 0 {
		summary += " Also wrote: " + strings.Join(written, ", ") + "."
	}
	if len(p.plan.SkippedRules) > 0 {
		summary += fmt.Sprintf(" %d item(s) were not imported — see the preview above.", len(p.plan.SkippedRules))
	}
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role: "assistant",
		// The permission engine and the MCP toolsets are both built at startup
		// and are not reachable from here, so nothing takes effect until the
		// next run. Saying otherwise would leave the user waiting for a rule to
		// start applying.
		content: summary + "\n\nRestart pi-go for the imported configuration to take effect.",
	})
	return m, nil
}

// handleImportClaudeCancel drops the preview without writing.
func (m *model) handleImportClaudeCancel() (tea.Model, tea.Cmd) {
	m.pendingClaudeImport = nil
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: "Import canceled. Nothing was written.",
	})
	return m, nil
}

// handleImportClaudeKey resolves the import confirmation, swallowing every other
// key so a stray press cannot apply a plan the user never read.
func (m *model) handleImportClaudeKey(key tea.Key) (tea.Model, tea.Cmd, bool) {
	if m.running || m.pendingClaudeImport == nil {
		return nil, nil, false
	}
	switch {
	case key.Code == tea.KeyEnter:
		model, cmd := m.handleImportClaudeConfirm()
		return model, cmd, true
	case isCancelKey(key):
		model, cmd := m.handleImportClaudeCancel()
		return model, cmd, true
	default:
		return m, nil, true
	}
}

// existingPermissionRules collects the rules pi-go already has, so the import
// only proposes additions.
func existingPermissionRules(cfg *config.Config) []string {
	if cfg.Permissions == nil {
		return nil
	}
	return cfg.Permissions.Rules
}

// existingMCPServerNames collects the configured server names for the same
// reason, in their original case — Build lowercases both sides for comparison.
func existingMCPServerNames(servers []extension.MCPServerConfig) []string {
	names := make([]string, 0, len(servers))
	for _, s := range servers {
		names = append(names, s.Name)
	}
	return names
}

// mcpConfigPath is where imported servers are written: the project's
// .pi-go/mcp.json when there is a project, the global one otherwise. Importing
// a project's servers into the global config would leak one repository's
// servers into every other.
func mcpConfigPath(cwd string) string {
	if cwd == "" {
		return ""
	}
	return filepath.Join(cwd, ".pi-go", "mcp.json")
}
