package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dimetron/pi-go/internal/permission"
)

func permReq(tool, desc string) permission.Request {
	return permission.Request{Tool: tool, Description: desc, Arg: desc}
}

// answered runs fn on another goroutine and waits for the result, failing
// rather than hanging if it never comes back.
func answered(t *testing.T, fn func() bool) bool {
	t.Helper()
	done := make(chan bool, 1)
	go func() { done <- fn() }()
	select {
	case got := <-done:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("permission question was never answered")
		return false
	}
}

func TestApprover_BlocksUntilTheQuestionIsAnswered(t *testing.T) {
	m := newTestModel(t)
	answeredCh := make(chan bool, 1)
	go func() {
		ok, err := m.Approver()(context.Background(), permReq("bash", "rm -rf /tmp/x"))
		if err != nil {
			t.Errorf("Approver returned an error: %v", err)
		}
		answeredCh <- ok
	}()

	// The whole point of the seam: the tool goroutine must wait for a human.
	select {
	case got := <-answeredCh:
		t.Fatalf("Approver returned %v before the question was answered", got)
	case <-time.After(50 * time.Millisecond):
	}

	m.permissionPromptMu.Lock()
	pending := m.permissionPrompt
	m.permissionPromptMu.Unlock()
	if pending == nil {
		t.Fatal("no prompt was recorded on the model")
	}

	if _, _, handled := m.handlePermissionPromptKey(tea.Key{Code: 'y'}); !handled {
		t.Fatal("y was not handled by the prompt")
	}
	m.permissionPromptMu.Lock()
	still := m.permissionPrompt
	m.permissionPromptMu.Unlock()
	if still != nil {
		t.Fatal("prompt was not cleared after approval")
	}
	if got := <-answeredCh; !got {
		t.Fatal("y did not approve the request")
	}
}

func TestHandlePermissionPromptKey_NAndEnter(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.Key
		want bool
	}{
		{"n refuses", tea.Key{Code: 'n'}, false},
		{"enter approves", tea.Key{Code: tea.KeyEnter}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(t)
			p := &permissionPromptState{
				req:   permReq("bash", "ls"),
				reply: make(chan bool, 1),
			}
			m.permissionPrompt = p
			m.handlePermissionPromptKey(tc.key)
			m.permissionPromptMu.Lock()
			still := m.permissionPrompt
			m.permissionPromptMu.Unlock()
			if still != nil {
				t.Fatal("prompt was not cleared")
			}
			if got := <-p.reply; got != tc.want {
				t.Fatalf("answer = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHandlePermissionPromptKey_EscapeRefuses(t *testing.T) {
	m := newTestModel(t)
	p := &permissionPromptState{
		req:   permReq("bash", "rm -rf /"),
		reply: make(chan bool, 1),
	}
	m.permissionPrompt = p
	// An interrupt must never be the thing that lets a destructive call
	// through, so Escape answers "no" rather than dismissing the question.
	m.handlePermissionPromptKey(tea.Key{Code: tea.KeyEscape})
	if got := <-p.reply; got {
		t.Fatal("escape approved the request")
	}
}

func TestHandlePermissionPromptKey_OtherKeysAreConsumedAndInert(t *testing.T) {
	m := newTestModel(t)
	m.permissionPrompt = &permissionPromptState{
		req:   permReq("bash", "ls"),
		reply: make(chan bool, 1),
	}
	p := m.permissionPrompt
	_, _, handled := m.handlePermissionPromptKey(tea.Key{Code: 'a'})
	if !handled {
		t.Fatal("an unrelated key was not consumed, so it would reach the input")
	}
	m.permissionPromptMu.Lock()
	still := m.permissionPrompt
	m.permissionPromptMu.Unlock()
	if still != p {
		t.Fatal("an unrelated key changed the prompt")
	}
	select {
	case got := <-p.reply:
		t.Fatalf("an unrelated key answered the question: %v", got)
	default:
	}
}

func TestHandlePermissionPromptKey_UppercaseYWithModifierIsNotApproval(t *testing.T) {
	m := newTestModel(t)
	p := &permissionPromptState{
		req:   permReq("bash", "ls"),
		reply: make(chan bool, 1),
	}
	m.permissionPrompt = p
	// Alt+Y, a common "yes and keep going" reflex, must not approve on its own.
	m.handlePermissionPromptKey(tea.Key{Code: 'y', Mod: tea.ModAlt})
	select {
	case got := <-p.reply:
		t.Fatalf("alt+y answered the question: %v", got)
	default:
	}
}

func TestRequestPermission_SecondQuestionIsRefusedNotReplaced(t *testing.T) {
	m := newTestModel(t)
	first := make(chan bool, 1)
	m.permissionPrompt = &permissionPromptState{req: permReq("bash", "one"), reply: first}

	// A second question arriving while one is on screen must not replace it —
	// that would strand the first tool goroutine on a channel nobody holds.
	if m.requestPermission(permReq("bash", "two")) {
		t.Fatal("the second question was approved")
	}
	m.permissionPromptMu.Lock()
	p := m.permissionPrompt
	m.permissionPromptMu.Unlock()
	if p == nil || p.req.Description != "one" {
		t.Fatal("the first question was displaced")
	}
}

func TestClearPermissionPrompt_AnswersFalse(t *testing.T) {
	m := newTestModel(t)
	reply := make(chan bool, 1)
	m.permissionPrompt = &permissionPromptState{req: permReq("bash", "ls"), reply: reply}
	m.clearPermissionPrompt()
	if got := <-reply; got {
		t.Fatal("a dropped prompt approved the request")
	}
	if m.permissionPrompt != nil {
		t.Fatal("prompt was not cleared")
	}
}

func TestClearPermissionPrompt_NoPromptIsNotAPanic(t *testing.T) {
	newTestModel(t).clearPermissionPrompt()
}

func TestRenderPermissionPrompt_ShowsToolAndKeys(t *testing.T) {
	m := newTestModel(t)
	m.permissionPrompt = &permissionPromptState{
		req:   permReq("bash", "rm -rf /tmp/build"),
		reply: make(chan bool, 1),
	}
	box := stripANSI(m.renderPermissionPrompt(60))
	for _, want := range []string{"Permission required", "rm -rf /tmp/build", "y / enter allow", "n / esc deny"} {
		if !strings.Contains(box, want) {
			t.Errorf("prompt box is missing %q:\n%s", want, box)
		}
	}
}

func TestRenderPermissionPrompt_LongCommandIsWrappedNotOverflowing(t *testing.T) {
	m := newTestModel(t)
	long := "echo " + strings.Repeat("abcdefgh ", 40)
	m.permissionPrompt = &permissionPromptState{
		req:   permReq("bash", long),
		reply: make(chan bool, 1),
	}
	const width = 50
	for _, line := range strings.Split(m.renderPermissionPrompt(width), "\n") {
		if n := len([]rune(stripANSI(line))); n > width {
			t.Errorf("line is %d runes, wider than the %d-column box: %q", n, width, line)
		}
	}
}

func TestRenderPermissionPrompt_NoPromptRendersNothing(t *testing.T) {
	if got := newTestModel(t).renderPermissionPrompt(60); got != "" {
		t.Fatalf("expected no box without a prompt, got %q", got)
	}
}

func TestOverlayPermissionPrompt_NoPromptLeavesMessagesAlone(t *testing.T) {
	m := newTestModel(t)
	if got := m.overlayPermissionPrompt("body", 60); got != "body" {
		t.Fatalf("messages were modified: %q", got)
	}
}

func TestOverlayPermissionPrompt_PaintsOverMessages(t *testing.T) {
	m := newTestModel(t)
	m.permissionPrompt = &permissionPromptState{
		req:   permReq("bash", "rm -rf /tmp/build"),
		reply: make(chan bool, 1),
	}
	// The overlay centres a box inside a viewport, so the viewport needs real
	// height — every other overlay in this package is handed a scrolled
	// transcript, and the prompt has to survive the same path.
	transcript := strings.Repeat("earlier transcript line\n", 20)
	got := stripANSI(m.overlayPermissionPrompt(transcript, 60))
	// What matters is that the question is actually on screen: a prompt drawn
	// into a corner of a scrolled transcript is a prompt the user never sees,
	// and the agent stays blocked on it.
	if !strings.Contains(got, "Permission required") {
		t.Errorf("the prompt was not painted into the viewport:\n%s", got)
	}
	if !strings.Contains(got, "rm -rf /tmp/build") {
		t.Errorf("the prompt did not show what it is asking about:\n%s", got)
	}
}

func TestOverlayPermissionPrompt_PromptIsNotClippedByAShortViewport(t *testing.T) {
	m := newTestModel(t)
	m.permissionPrompt = &permissionPromptState{
		req:   permReq("bash", "rm -rf /tmp/build"),
		reply: make(chan bool, 1),
	}
	// A viewport shorter than the box is a real state — the transcript can be
	// compact. What must survive is the question itself, not the key legend.
	got := stripANSI(m.overlayPermissionPrompt("one line", 60))
	if !strings.Contains(got, "Permission required") {
		t.Errorf("a short viewport hid the question:\n%s", got)
	}
}

func TestModeCycleKey_AdvancesAndReportsTheMode(t *testing.T) {
	engine := testEngine()
	engine.SetMode(permission.ModeAuto)
	m := newTestModel(t)
	m.cfg.PermissionBridge = NewPermissionBridge(engine)

	_, _, handled := m.handleModeCycleKey(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift})
	if !handled {
		t.Fatal("shift+tab was not handled while a permission engine was attached")
	}
	// Auto sits three places into Modes, so one press lands on dontAsk. Asserting
	// the concrete value rather than just "it changed" is what catches a cycle
	// that silently wraps to the wrong end of the list.
	if got := engine.Mode(); got != permission.ModeDontAsk {
		t.Fatalf("mode = %q after one press, want %q", got, permission.ModeDontAsk)
	}
	if !strings.Contains(m.flash, string(permission.ModeDontAsk)) {
		t.Errorf("flash = %q, want it to name the new mode", m.flash)
	}
}

func TestModeCycleKey_PressesAreInertWithoutAnEngine(t *testing.T) {
	shiftTab := tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}
	for _, tc := range []struct {
		name   string
		bridge PermissionApprover
		want   bool // handled
	}{
		{"no bridge", nil, false},
		{"pending bridge with no engine yet", NewPendingPermissionBridge(make(chan *PermissionBridge, 1)), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(t)
			m.cfg.PermissionBridge = tc.bridge
			_, _, handled := m.handleModeCycleKey(shiftTab)
			if handled != tc.want {
				t.Fatalf("handled = %v, want %v", handled, tc.want)
			}
		})
	}
}

func TestModeCycleKey_LeavesPlainTabAlone(t *testing.T) {
	// Shift+Tab and Tab are the same code in bubbletea v2, so a handler that
	// matched on the code alone would hijack plain Tab from the input box.
	m := newTestModel(t)
	m.cfg.PermissionBridge = NewPermissionBridge(testEngine())
	if _, _, handled := m.handleModeCycleKey(tea.Key{Code: tea.KeyTab}); handled {
		t.Fatal("plain tab was consumed by the mode handler")
	}
}

func TestModeCycleKey_DropsAPendingPromptWithoutAnsweringIt(t *testing.T) {
	// The prompt is answered by a keypress. A mode press is not one, so the
	// question has to be withdrawn — otherwise the next key the user types is
	// read as the answer to a question they had already moved on from. It must
	// be withdrawn as a refusal: the new mode may be looser, in which case the
	// agent retries and asks again.
	engine := testEngine()
	engine.SetMode(permission.ModeDefault)
	m := newTestModel(t)
	m.cfg.PermissionBridge = NewPermissionBridge(engine)
	reply := make(chan bool, 1)
	m.permissionPrompt = &permissionPromptState{req: permReq("bash", "rm -rf /"), reply: reply}

	m.handleModeCycleKey(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift})

	select {
	case got := <-reply:
		if got {
			t.Fatal("a mode change approved the pending request")
		}
	default:
		t.Fatal("the pending request was left unanswered and the prompt still on screen")
	}
}

func TestWrapForWidth(t *testing.T) {
	// A hard wrap, not a word wrap: the box is drawn by lipgloss but composed
	// by hand, so a long unbroken command has to be broken somewhere.
	if got := wrapForWidth("aaaa bbbb", 4); got != "aaaa bbb\nb" {
		t.Errorf("wrapForWidth = %q", got)
	}
	// A width too small to be usable must not produce an empty or infinite
	// result; the box has a floor and the wrap has to agree with it.
	if got := wrapForWidth("abcdef", 0); got == "" {
		t.Error("a zero width produced no output")
	}
	// Newlines are preserved rather than folded into one long line.
	if got := wrapForWidth("ab\ncd", 40); got != "ab\ncd" {
		t.Errorf("wrapForWidth = %q, want %q", got, "ab\ncd")
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate shortened a short string: %q", got)
	}
	if got := truncate("abcdefgh", 6); len([]rune(got)) != 6 {
		t.Errorf("truncate = %q, want 6 runes", got)
	}
	// Truncation must count runes, not bytes, or a multi-byte command is cut
	// mid-rune and renders as a replacement character in the prompt. n=4 is
	// the floor: below it the ellipsis itself would not fit.
	if got := truncate("ααααα", 4); got != "ααα…" {
		t.Errorf("truncate = %q, want %q", got, "ααα…")
	}
	// A width under the floor is raised rather than producing a broken result.
	if got := truncate("abcdefgh", 1); got == "" {
		t.Error("truncate below the floor produced no output")
	}
}
