package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	adkmodel "google.golang.org/adk/v2/model"

	"github.com/dimetron/pi-go/internal/config"
)

func TestMenuItem_Interface(t *testing.T) {
	item := MenuItem{
		title:       "/model",
		description: "Show or switch model",
		filterVal:   "/model switch",
		value:       "/model",
	}

	if item.Title() != "/model" {
		t.Errorf("Title() = %q, want /model", item.Title())
	}
	if item.Description() != "Show or switch model" {
		t.Errorf("Description() = %q, want Show or switch model", item.Description())
	}
	if item.FilterValue() != "/model switch" {
		t.Errorf("FilterValue() = %q, want /model switch", item.FilterValue())
	}
	if item.Value() != "/model" {
		t.Errorf("Value() = %q, want /model", item.Value())
	}

	// Default fallback for filterVal and value
	fallback := MenuItem{
		title:       "/clear",
		description: "Clear screen",
	}
	if fallback.FilterValue() != "/clear Clear screen" {
		t.Errorf("FilterValue() fallback = %q", fallback.FilterValue())
	}
	if fallback.Value() != "/clear" {
		t.Errorf("Value() fallback = %q", fallback.Value())
	}
}

func TestSearchPopup_ListModelIntegration(t *testing.T) {
	m := &model{
		inputModel: InputModel{Text: "/"},
		chatModel:  ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			ModelName: "test-model",
		},
	}

	m.newSearchPopup(searchModeCommands)
	if m.searchPopup == nil {
		t.Fatal("expected searchPopup to be initialized")
	}

	// Verify list.Model was created
	items := m.searchPopup.list.Items()
	if len(items) == 0 {
		t.Fatal("expected searchPopup.list to contain command items")
	}

	// Verify delegate item type
	firstItem, ok := items[0].(MenuItem)
	if !ok {
		t.Fatalf("expected items to be of type MenuItem, got %T", items[0])
	}
	if !strings.HasPrefix(firstItem.Title(), "/") {
		t.Errorf("expected command title starting with '/', got %q", firstItem.Title())
	}

	// Verify initial selection
	if m.searchPopup.selected != 0 {
		t.Errorf("expected initial selected = 0, got %d", m.searchPopup.selected)
	}
	if m.searchPopup.list.Index() != 0 {
		t.Errorf("expected list.Index() = 0, got %d", m.searchPopup.list.Index())
	}

	// Navigation: selectNext moves selection and syncs with list
	m.searchPopup.selectNext()
	if m.searchPopup.selected != 1 {
		t.Errorf("expected selected = 1 after selectNext, got %d", m.searchPopup.selected)
	}
	if m.searchPopup.list.Index() != 1 {
		t.Errorf("expected list.Index() = 1 after selectNext, got %d", m.searchPopup.list.Index())
	}

	// Navigation: selectPrev moves back to 0
	m.searchPopup.selectPrev()
	if m.searchPopup.selected != 0 {
		t.Errorf("expected selected = 0 after selectPrev, got %d", m.searchPopup.selected)
	}
	if m.searchPopup.list.Index() != 0 {
		t.Errorf("expected list.Index() = 0 after selectPrev, got %d", m.searchPopup.list.Index())
	}

	// Live filtering: typing to search updates filtered items and list.Model
	m.searchPopup.search = "model"
	m.searchPopup.filterSearch()
	if len(m.searchPopup.filtered) == 0 {
		t.Fatal("expected filtered results for 'model'")
	}
	if len(m.searchPopup.list.Items()) != len(m.searchPopup.filtered) {
		t.Errorf("expected list.Items() len (%d) == filtered len (%d)",
			len(m.searchPopup.list.Items()), len(m.searchPopup.filtered))
	}
}

func TestModelMenu_OpenAndKeyNavigation(t *testing.T) {
	var switchedTo string
	mockSwitcher := func(_ context.Context, modelName string) (adkmodel.LLM, string, string, error) {
		switchedTo = modelName
		return nil, modelName, "test-prov", nil
	}

	m := &model{
		inputModel: InputModel{Text: ""},
		chatModel:  ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			ModelName:     "claude-sonnet-4-6",
			ProviderName:  "anthropic",
			ActiveRole:    "default",
			ModelSwitcher: mockSwitcher,
			Roles: map[string]config.RoleConfig{
				"default": {Model: "claude-sonnet-4-6", Provider: "anthropic"},
				"fast":    {Model: "gemini-2.5-flash", Provider: "google"},
			},
		},
	}

	// Run /model command with no args -> should open interactive menu
	newM, _ := m.handleSlashCommand("/model")
	mm := newM.(*model)

	if mm.modelPicker == nil {
		t.Fatal("expected modelPicker to be open after /model")
	}

	items := mm.modelPicker.list.Items()
	if len(items) == 0 {
		t.Fatal("expected modelPicker to have items")
	}

	// Check that roles and known models are in the list
	foundRole := false
	for _, it := range items {
		mi := it.(MenuItem)
		if mi.Title() == "fast" {
			foundRole = true
			if !strings.Contains(mi.Description(), "gemini-2.5-flash") {
				t.Errorf("expected fast role description to mention gemini-2.5-flash, got %q", mi.Description())
			}
		}
	}
	if !foundRole {
		t.Error("expected 'fast' role in model picker items")
	}

	// Navigate down with KeyDown
	downKeyMsg := tea.KeyPressMsg(tea.Key{Code: tea.KeyDown})
	newM, _ = mm.handleKey(downKeyMsg)
	mm = newM.(*model)

	if mm.modelPicker.list.Index() != 1 {
		t.Errorf("expected list index to advance to 1 after KeyDown, got %d", mm.modelPicker.list.Index())
	}

	// Esc dismisses the picker without switching model
	escKeyMsg := tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc})
	newM, _ = mm.handleKey(escKeyMsg)
	mm = newM.(*model)

	if mm.modelPicker != nil {
		t.Error("expected modelPicker to be nil after Esc")
	}
	if switchedTo != "" {
		t.Errorf("model should not have switched on Esc, but got %q", switchedTo)
	}
}

func TestModelMenu_EnterConfirmation(t *testing.T) {
	var switchedTo string
	mockSwitcher := func(_ context.Context, modelName string) (adkmodel.LLM, string, string, error) {
		switchedTo = modelName
		return nil, modelName, "test-prov", nil
	}

	m := &model{
		inputModel: InputModel{Text: ""},
		chatModel:  ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			ModelName:     "claude-sonnet-4-6",
			ProviderName:  "anthropic",
			ActiveRole:    "default",
			ModelSwitcher: mockSwitcher,
			Roles: map[string]config.RoleConfig{
				"fast": {Model: "gemini-2.5-flash", Provider: "google"},
			},
		},
	}

	// Open model menu
	m.openModelMenu()
	if m.modelPicker == nil {
		t.Fatal("expected modelPicker to be open")
	}

	// Find the index of "fast"
	fastIdx := -1
	for idx, it := range m.modelPicker.list.Items() {
		if it.(MenuItem).Value() == "fast" {
			fastIdx = idx
			break
		}
	}
	if fastIdx < 0 {
		t.Fatal("expected to find 'fast' role in model picker items")
	}

	m.modelPicker.list.Select(fastIdx)

	// Press Enter to confirm selection
	enterMsg := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	newM, _ := m.handleKey(enterMsg)
	mm := newM.(*model)

	// Model picker should be closed
	if mm.modelPicker != nil {
		t.Error("expected modelPicker to be dismissed after Enter confirmation")
	}

	// The model switcher should have been called with the selected model (from role fast: gemini-2.5-flash)
	if switchedTo != "gemini-2.5-flash" {
		t.Errorf("expected switchedTo = %q, got %q", "gemini-2.5-flash", switchedTo)
	}

	// State should be updated
	if mm.cfg.ActiveRole != "fast" {
		t.Errorf("expected ActiveRole = fast, got %q", mm.cfg.ActiveRole)
	}
}

func TestModelMenu_RenderAndOverlay(t *testing.T) {
	m := &model{
		width:      80,
		height:     24,
		palette:    darkPalette,
		inputModel: InputModel{Text: ""},
		chatModel:  ChatModel{Messages: make([]message, 0), Width: 80},
		cfg: Config{
			ModelName: "claude-sonnet-4-6",
		},
	}

	m.openModelMenu()
	rendered := m.renderModelPicker(70)
	if rendered == "" {
		t.Fatal("expected renderModelPicker to return non-empty view")
	}
	if !strings.Contains(rendered, "Select Model or Role") {
		t.Errorf("expected rendered picker to contain title, got %q", rendered)
	}

	// Overlay over messages
	messages := "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10"
	overlaid := m.overlayModelPicker(messages, 80)
	if overlaid == "" {
		t.Fatal("expected overlayModelPicker to produce overlaid content")
	}
	if !strings.Contains(overlaid, "Select Model or Role") {
		t.Errorf("expected overlay to contain model picker title")
	}
}
