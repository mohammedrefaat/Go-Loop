package tui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/dimetron/pi-go/internal/provider"
)

// MenuItem represents an item in an interactive selectable list,
// satisfying list.DefaultItem for charm.land/bubbles/v2/list.
type MenuItem struct {
	title       string
	description string
	filterVal   string
	value       string
}

// Title returns the item title displayed in the list.
func (i MenuItem) Title() string { return i.title }

// Description returns the secondary item description displayed in the list.
func (i MenuItem) Description() string { return i.description }

// FilterValue returns the search string used for live fuzzy filtering.
func (i MenuItem) FilterValue() string {
	if i.filterVal != "" {
		return i.filterVal
	}
	if i.description != "" {
		return i.title + " " + i.description
	}
	return i.title
}

// Value returns the raw payload or identifier (e.g. command name or model ID).
func (i MenuItem) Value() string {
	if i.value != "" {
		return i.value
	}
	return i.title
}

// modelPickerState manages the interactive /model selection screen.
type modelPickerState struct {
	list   list.Model
	width  int
	height int
}

// newInteractiveList creates a new list.Model configured with NewDefaultDelegate(),
// fuzzy filtering enabled, and styled with dark mode defaults.
func newInteractiveList(items []list.Item, width, height int, title string) list.Model {
	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = true
	delegate.SetHeight(2)
	delegate.SetSpacing(0)

	l := list.New(items, delegate, width, height)
	l.Title = title
	l.SetFilteringEnabled(true)
	l.SetShowStatusBar(true)
	l.SetShowPagination(true)
	l.SetShowHelp(true)
	l.DisableQuitKeybindings()
	return l
}

// openModelMenu gathers configured roles and known provider models,
// initializes an interactive selectable menu with charm.land/bubbles/v2/list,
// and activates m.modelPicker.
func (m *model) openModelMenu() {
	var items []list.Item
	seen := make(map[string]bool)

	// 1. Configured roles from m.cfg.Roles (default first, then alphabetical).
	if len(m.cfg.Roles) > 0 {
		roleNames := make([]string, 0, len(m.cfg.Roles))
		for name := range m.cfg.Roles {
			roleNames = append(roleNames, name)
		}
		sort.Slice(roleNames, func(i, j int) bool {
			if roleNames[i] == "default" {
				return true
			}
			if roleNames[j] == "default" {
				return false
			}
			return roleNames[i] < roleNames[j]
		})

		for _, name := range roleNames {
			rc := m.cfg.Roles[name]
			title := name
			desc := fmt.Sprintf("Role -> %s", rc.Model)
			if rc.Provider != "" {
				desc += fmt.Sprintf(" [%s]", rc.Provider)
			}
			if name == m.cfg.ActiveRole || (m.cfg.ActiveRole == "" && name == "default") {
				desc += " * (active)"
			}
			items = append(items, MenuItem{
				title:       title,
				description: desc,
				filterVal:   fmt.Sprintf("%s role %s %s", name, rc.Model, rc.Provider),
				value:       name,
			})
			seen[name] = true
		}
	}

	// 2. Current active model (if not already added as a role).
	if m.cfg.ModelName != "" && !seen[m.cfg.ModelName] {
		prov := m.cfg.ProviderName
		if prov == "" {
			prov = "active"
		}
		items = append(items, MenuItem{
			title:       m.cfg.ModelName,
			description: fmt.Sprintf("Current Model [%s] * (active)", prov),
			filterVal:   fmt.Sprintf("%s current active %s", m.cfg.ModelName, prov),
			value:       m.cfg.ModelName,
		})
		seen[m.cfg.ModelName] = true
	}

	// 3. Known models per provider from provider.KnownModels.
	// Sort providers putting current provider first, then alphabetical.
	provs := make([]string, 0, len(provider.KnownModels))
	for p := range provider.KnownModels {
		provs = append(provs, p)
	}
	sort.Slice(provs, func(i, j int) bool {
		if provs[i] == m.cfg.ProviderName {
			return true
		}
		if provs[j] == m.cfg.ProviderName {
			return false
		}
		return provs[i] < provs[j]
	})

	for _, p := range provs {
		models := provider.KnownModels[p]
		for _, mod := range models {
			if seen[mod] {
				continue
			}
			seen[mod] = true
			items = append(items, MenuItem{
				title:       mod,
				description: fmt.Sprintf("Provider: %s", p),
				filterVal:   fmt.Sprintf("%s %s", mod, p),
				value:       mod,
			})
		}
	}

	width := m.chatWidth()
	if width <= 0 || width > 80 {
		width = 80
	}
	if width < 30 {
		width = 30
	}

	height := m.messageViewportHeight()
	if height <= 0 || height > 18 {
		height = 18
	}
	if height < 8 {
		height = 8
	}

	l := newInteractiveList(items, width-4, height-2, "Select Model or Role")
	m.modelPicker = &modelPickerState{
		list:   l,
		width:  width,
		height: height,
	}
}

// handleModelPickerKey handles keyboard input when the model picker menu is open:
// - Enter selects the highlighted model/role and switches to it.
// - Esc cancels and closes the picker.
// - All other keys (arrows, typing for fuzzy filter, pagination) are forwarded to list.Model.Update().
func (m *model) handleModelPickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	if m.modelPicker == nil {
		return nil, nil, false
	}

	key := msg.Key()

	// If currently typing into the filter input:
	if m.modelPicker.list.SettingFilter() {
		switch key.Code {
		case tea.KeyEsc:
			// Let the filter input cancel itself first
			var cmd tea.Cmd
			m.modelPicker.list, cmd = m.modelPicker.list.Update(msg)
			return m, cmd, true
		case tea.KeyEnter:
			// Accepting filter query
			var cmd tea.Cmd
			m.modelPicker.list, cmd = m.modelPicker.list.Update(msg)
			return m, cmd, true
		}
	}

	switch key.Code {
	case tea.KeyEsc:
		// Close model picker without changing model
		m.modelPicker = nil
		return m, nil, true

	case tea.KeyEnter:
		// Confirm selection
		selected := m.modelPicker.list.SelectedItem()
		if selected != nil {
			if mi, ok := selected.(MenuItem); ok {
				m.modelPicker = nil
				model, cmd := m.handleModelCommand([]string{mi.Value()})
				return model, cmd, true
			}
		}
		m.modelPicker = nil
		return m, nil, true
	}

	// Forward arrow navigation, typing to fuzzy filter, pagination to list.Model
	var cmd tea.Cmd
	m.modelPicker.list, cmd = m.modelPicker.list.Update(msg)
	return m, cmd, true
}

// renderModelPicker renders the interactive model picker menu in a styled container.
func (m *model) renderModelPicker(width int) string {
	if m.modelPicker == nil {
		return ""
	}
	if width < 30 {
		width = 30
	}

	innerW := max(20, width-4)
	innerH := max(6, m.modelPicker.height-2)
	m.modelPicker.list.SetSize(innerW, innerH)

	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.palette.Cyan).
		Background(m.palette.Surface0).
		Padding(0, 1).
		Width(width)

	return style.Render(m.modelPicker.list.View())
}

// overlayModelPicker renders the model picker centered over messages.
func (m *model) overlayModelPicker(messages string, mainWidth int) string {
	if m.modelPicker == nil {
		return messages
	}

	pickerWidth := min(mainWidth-4, 80)
	if pickerWidth < 30 {
		pickerWidth = 30
	}
	picker := m.renderModelPicker(pickerWidth)
	if picker == "" {
		return messages
	}
	return overlayCenteredBox(messages, picker, mainWidth)
}

// overlayCenteredBox paints box over messages, centered horizontally and
// vertically. Both interactive pickers use it, so a session list and a model
// list sit in the same place and look the same; only the box differs.
func overlayCenteredBox(messages, box string, mainWidth int) string {
	if box == "" {
		return messages
	}

	lines := strings.Split(messages, "\n")
	viewportHeight := len(lines)

	pickerLines := strings.Split(box, "\n")
	if viewportHeight > 0 && len(pickerLines) > viewportHeight {
		pickerLines = pickerLines[:viewportHeight]
	}
	if len(pickerLines) == 0 {
		return strings.Join(lines, "\n")
	}

	boxWidth := maxLineWidth(pickerLines)
	left := 0
	if mainWidth > boxWidth {
		left = (mainWidth - boxWidth) / 2
	}
	start := 0
	if viewportHeight > len(pickerLines) {
		start = (viewportHeight - len(pickerLines)) / 2
	}
	if start < 0 {
		start = 0
	}

	for i, line := range pickerLines {
		idx := start + i
		if idx >= len(lines) {
			break
		}
		lines[idx] = overlayPopupLine(line, left, mainWidth)
	}
	return strings.Join(lines, "\n")
}
