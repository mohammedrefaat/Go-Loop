package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestCompletedTurnScrollbackEmission(t *testing.T) {
	m := newTestModelFull(t)
	m.running = true
	m.chatModel.Messages = []message{
		{role: "user", content: "What is 2+2?"},
		{role: "assistant", content: "4"},
	}

	// When agentDoneMsg is received, the completed turn should be emitted via tea.Printf
	newM, cmd := m.Update(agentDoneMsg{})
	mm := newM.(*model)
	var _ tea.Cmd = cmd

	if mm.running {
		t.Error("expected running to be false after agentDoneMsg")
	}

	// Verify a tea.Cmd was returned for tea.Printf
	if cmd == nil {
		t.Fatal("expected non-nil tea.Cmd emitting tea.Printf for the completed turn")
	}

	// Executing the Cmd should yield a message representing the printed line
	msg := cmd()
	if msg == nil {
		t.Fatal("expected Cmd execution to produce printLineMessage")
	}

	// The completed messages should now be removed from Messages (unmanaged by View)
	if len(mm.chatModel.Messages) != 0 {
		t.Errorf("expected 0 active messages in chatModel, got %d", len(mm.chatModel.Messages))
	}

	// The completed messages should be preserved in CommittedMessages
	if len(mm.chatModel.CommittedMessages) != 2 {
		t.Fatalf("expected 2 committed messages, got %d", len(mm.chatModel.CommittedMessages))
	}
	if mm.chatModel.CommittedMessages[0].content != "What is 2+2?" {
		t.Errorf("expected user message in committed messages, got %q", mm.chatModel.CommittedMessages[0].content)
	}
	if mm.chatModel.CommittedMessages[1].content != "4" {
		t.Errorf("expected assistant message in committed messages, got %q", mm.chatModel.CommittedMessages[1].content)
	}
	if !mm.chatModel.HasCommitted {
		t.Error("expected HasCommitted to be true")
	}

	// View() should NOT re-render the committed history
	mm.width = 80
	mm.height = 24
	viewOutput := mm.View().Content
	if strings.Contains(viewOutput, "What is 2+2?") {
		t.Errorf("View() should not re-render committed user turn, found in view:\n%s", viewOutput)
	}
	if strings.Contains(viewOutput, "4") && strings.Contains(viewOutput, "What is 2+2?") {
		t.Errorf("View() should not re-render committed assistant turn, found in view:\n%s", viewOutput)
	}

	// PlainTranscript and LastAssistantMessage should still access the committed history
	transcript := mm.chatModel.PlainTranscript()
	if !strings.Contains(transcript, "What is 2+2?") || !strings.Contains(transcript, "4") {
		t.Errorf("PlainTranscript() should retain committed history, got %q", transcript)
	}
	if last := mm.chatModel.LastAssistantMessage(); last != "4" {
		t.Errorf("LastAssistantMessage() = %q, want %q", last, "4")
	}
}

func TestViewRendersOnlyInProgressTurn(t *testing.T) {
	m := newTestModelFull(t)
	m.width = 80
	m.height = 24

	// Complete first turn
	m.running = true
	m.chatModel.Messages = append(m.chatModel.Messages,
		message{role: "user", content: "First turn"},
		message{role: "assistant", content: "First response"},
	)
	newM, _ := m.Update(agentDoneMsg{})
	m = newM.(*model)

	// Start second in-progress turn
	m.running = true
	m.chatModel.Messages = append(m.chatModel.Messages,
		message{role: "user", content: "Second turn in progress"},
		message{role: "assistant", content: "Streaming partial..."},
	)

	viewOutput := m.View().Content

	// Should contain the in-progress turn
	if !strings.Contains(viewOutput, "Second turn in progress") {
		t.Errorf("View() should render in-progress turn, got:\n%s", viewOutput)
	}
	// Should NOT contain the first, already-committed turn
	if strings.Contains(viewOutput, "First turn") {
		t.Errorf("View() should NOT re-render committed first turn, got:\n%s", viewOutput)
	}
}
