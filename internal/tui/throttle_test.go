package tui

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestThrottleAgentChannel_BuffersAndFlushesTokens(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rawCh := make(chan agentMsg, 64)
	outCh := make(chan agentMsg, 64)

	// Use 30ms throttle interval
	go throttleAgentChannel(ctx, rawCh, outCh, 30*time.Millisecond)

	// Send 5 rapid tokens within a few ms
	rawCh <- agentTextMsg{text: "Hello"}
	rawCh <- agentTextMsg{text: " "}
	rawCh <- agentTextMsg{text: "world"}
	rawCh <- agentTextMsg{text: ","}
	rawCh <- agentTextMsg{text: " welcome!"}

	// Should not deliver immediately on token 1
	select {
	case msg := <-outCh:
		// If received, it must be the flushed batch, not single token
		if txt, ok := msg.(agentTextMsg); ok {
			if !strings.Contains(txt.text, "welcome") {
				t.Logf("partial flush received: %q", txt.text)
			}
		}
	case <-time.After(10 * time.Millisecond):
		// Expected: buffering tokens during the tick window
	}

	// Wait for tick to flush
	select {
	case msg := <-outCh:
		txt, ok := msg.(agentTextMsg)
		if !ok {
			t.Fatalf("expected agentTextMsg, got %T", msg)
		}
		if !strings.Contains(txt.text, "Hello") || !strings.Contains(txt.text, "welcome!") {
			t.Errorf("expected buffered concatenated text, got %q", txt.text)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for throttled flush")
	}
}

func TestThrottleAgentChannel_FlushesBeforeToolCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rawCh := make(chan agentMsg, 64)
	outCh := make(chan agentMsg, 64)

	go throttleAgentChannel(ctx, rawCh, outCh, 100*time.Millisecond)

	// Send text token then immediately a tool call
	rawCh <- agentTextMsg{text: "I will read the file now."}
	rawCh <- agentToolCallMsg{name: "read", args: map[string]any{"path": "main.go"}}

	// First received message MUST be the flushed text
	select {
	case msg := <-outCh:
		txt, ok := msg.(agentTextMsg)
		if !ok || txt.text != "I will read the file now." {
			t.Fatalf("expected flushed text before tool call, got %#v", msg)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("timed out waiting for flushed text before tool call")
	}

	// Second received message MUST be the tool call
	select {
	case msg := <-outCh:
		tc, ok := msg.(agentToolCallMsg)
		if !ok || tc.name != "read" {
			t.Fatalf("expected tool call after flushed text, got %#v", msg)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("timed out waiting for tool call")
	}
}

func TestThrottleAgentChannel_FlushesOnClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rawCh := make(chan agentMsg, 64)
	outCh := make(chan agentMsg, 64)

	go throttleAgentChannel(ctx, rawCh, outCh, 500*time.Millisecond)

	rawCh <- agentTextMsg{text: "Final message before done"}
	close(rawCh)

	// Should flush the text immediately when rawCh closes
	select {
	case msg, ok := <-outCh:
		if !ok {
			t.Fatal("outCh closed before flushed text was delivered")
		}
		txt, ok := msg.(agentTextMsg)
		if !ok || txt.text != "Final message before done" {
			t.Fatalf("expected final text delivered on close, got %#v", msg)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for flush on close")
	}

	// Then outCh should be closed
	select {
	case _, ok := <-outCh:
		if ok {
			t.Fatal("expected outCh to be closed after final flush")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for outCh to close")
	}
}

func TestMarkdownRenderCache_ReusesAndInvalidates(t *testing.T) {
	c := NewChatModel(nil)
	c.Width = 80

	md := "**Bold Title**\n\nSome paragraph text."

	// First render: populates markdownCache
	r1 := c.RenderMarkdown(md)
	if r1 == "" {
		t.Fatal("expected non-empty rendered markdown")
	}
	if len(c.markdownCache) != 1 {
		t.Fatalf("expected markdownCache to have 1 entry, got %d", len(c.markdownCache))
	}

	// Manually inject sentinel to verify cache hit without re-rendering
	c.markdownCache[md] = "CACHED_GLAMOUR_SENTINEL"
	r2 := c.RenderMarkdown(md)
	if r2 != "CACHED_GLAMOUR_SENTINEL" {
		t.Fatalf("expected cache hit returning sentinel, got %q", r2)
	}

	// Invalidate on resize/theme change
	c.invalidateRenderCaches()
	if len(c.markdownCache) != 0 {
		t.Errorf("expected markdownCache cleared after invalidation, got %d entries", len(c.markdownCache))
	}

	// Subsequent render re-renders
	r3 := c.RenderMarkdown(md)
	if r3 == "CACHED_GLAMOUR_SENTINEL" {
		t.Fatal("expected fresh render after cache invalidation")
	}
}
