package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/dimetron/pi-go/internal/permission"
)

// The tests below cover the four ways the @ and ! prefixes can go wrong:
// attaching a file the user did not name, silently dropping one they did,
// letting a mention bypass a deny rule, and blocking the UI on a slow command.

func newMentionFixture(t *testing.T) (*model, string) {
	t.Helper()
	dir := t.TempDir()
	m := newTestModel(t)
	m.cfg.WorkDir = dir
	return m, dir
}

func writeMentionFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding %s: %v", name, err)
	}
	return path
}

func TestMentionAttachesFileContents(t *testing.T) {
	m, dir := newMentionFixture(t)
	writeMentionFile(t, dir, "main.go", "package main\n\nfunc main() {}\n")

	out := m.resolveMentions("look at @main.go please")

	if !strings.Contains(out.Text, "package main") {
		t.Errorf("prompt does not carry the file contents:\n%s", out.Text)
	}
	if len(out.Attached) != 1 || out.Attached[0] != "@main.go" {
		t.Errorf("Attached = %v, want [@main.go]", out.Attached)
	}
	if len(out.Refused) != 0 {
		t.Errorf("Refused = %v, want none", out.Refused)
	}
	if strings.Contains(out.Text, "@main.go please") {
		t.Error("the raw @token was left in the prompt text")
	}
}

func TestMentionKeepsTheSurroundingProse(t *testing.T) {
	m, dir := newMentionFixture(t)
	writeMentionFile(t, dir, "a.go", "AAA")
	writeMentionFile(t, dir, "b.go", "BBB")

	out := m.resolveMentions("compare @a.go with @b.go")

	// The tokens are lifted out, but the sentence they sat in has to survive,
	// or the agent is handed content with no indication what was asked.
	if !strings.Contains(out.Text, "compare") || !strings.Contains(out.Text, "with") {
		t.Errorf("the prose around the mentions was lost:\n%s", out.Text)
	}
	if !strings.Contains(out.Text, "AAA") || !strings.Contains(out.Text, "BBB") {
		t.Errorf("not every mention was attached:\n%s", out.Text)
	}
	if got := len(out.Attached); got != 2 {
		t.Errorf("Attached = %v, want two entries", out.Attached)
	}
}

func TestMentionHonoursALineRange(t *testing.T) {
	m, dir := newMentionFixture(t)
	writeMentionFile(t, dir, "a.go", "one\ntwo\nthree\nfour\n")

	out := m.resolveMentions("read @a.go:2-3")

	if !strings.Contains(out.Text, "two") || !strings.Contains(out.Text, "three") {
		t.Errorf("the selected range is missing:\n%s", out.Text)
	}
	if strings.Contains(out.Text, "four") {
		t.Error("a line outside the range was attached")
	}
	if got := out.Attached; len(got) != 1 || got[0] != "@a.go:2-3" {
		t.Errorf("Attached = %v, want [@a.go:2-3]", got)
	}
}

func TestMentionRefusesAPathOutsideTheRoot(t *testing.T) {
	m, _ := newMentionFixture(t)

	out := m.resolveMentions("read @../../etc/passwd")

	if len(out.Refused) != 1 {
		t.Fatalf("Refused = %v, want the traversal reported", out.Refused)
	}
	if !strings.Contains(out.Refused[0], "not attached") {
		t.Errorf("Refused = %v, want it to say the file was not attached", out.Refused)
	}
	if strings.Contains(out.Text, "root:") {
		t.Error("the file contents reached the prompt despite the refusal")
	}
}

func TestMissingFileIsReportedNotSilent(t *testing.T) {
	m, _ := newMentionFixture(t)

	out := m.resolveMentions("read @nosuchfile.go")

	if len(out.Refused) != 1 {
		t.Fatalf("Refused = %v, want a missing file reported", out.Refused)
	}
	// The token must be gone from the prompt: leaving it would have the agent
	// read the file on its own authority, which is the thing just refused.
	if strings.Contains(out.Text, "@nosuchfile.go") {
		t.Errorf("the refused token was left in the prompt:\n%s", out.Text)
	}
}

func TestMentionGoesThroughThePermissionEngine(t *testing.T) {
	// The point of routing mentions through the engine: a deny rule written for
	// Read has to cover a file the user attached by typing "@", not only one
	// the agent chose to read.
	m, dir := newMentionFixture(t)
	writeMentionFile(t, dir, "secret.go", "TOP SECRET")
	spec := "secret.go"
	engine := permission.New([]permission.Rule{{
		Tool:       "Read",
		Specifier:  &spec,
		Decision:   permission.Deny,
	}})
	m.cfg.PermissionBridge = NewPermissionBridge(engine)

	out := m.resolveMentions("read @secret.go")

	if len(out.Refused) != 1 {
		t.Fatalf("Refused = %v, want the deny rule to apply", out.Refused)
	}
	if !strings.Contains(out.Refused[0], "denied by rule") {
		t.Errorf("Refused = %v, want it to name the rule that refused", out.Refused)
	}
	if strings.Contains(out.Text, "TOP SECRET") {
		t.Error("a denied file's contents reached the prompt")
	}
}

func TestMentionOutsideTheRootIsAllowedUnderAnAllowRule(t *testing.T) {
	// An explicit allow rule is the escape hatch for a deny that is too broad,
	// and it must work here too — otherwise the only way to attach a file
	// outside the root would be to turn the engine off entirely.
	m, _ := newMentionFixture(t)
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("outside content"), 0o644); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	spec := outside
	engine := permission.New([]permission.Rule{{
		Tool:       "Read",
		Specifier:  &spec,
		Decision:   permission.Allow,
	}})
	m.cfg.PermissionBridge = NewPermissionBridge(engine)

	out := m.resolveMentions("read @" + outside)

	if len(out.Refused) != 0 {
		t.Fatalf("Refused = %v, want the allow rule to take effect", out.Refused)
	}
	if !strings.Contains(out.Text, "outside content") {
		t.Errorf("the allowed file was not attached:\n%s", out.Text)
	}
}

func TestBareHandleInProseIsNotAMention(t *testing.T) {
	m, _ := newMentionFixture(t)

	out := m.resolveMentions("ping @someone about the build")

	if len(out.Attached) != 0 {
		t.Errorf("Attached = %v, want an @-handle left alone", out.Attached)
	}
	if !strings.Contains(out.Text, "@someone") {
		t.Errorf("the handle was stripped from the prompt:\n%s", out.Text)
	}
}

func TestPlainPromptIsUntouched(t *testing.T) {
	m, _ := newMentionFixture(t)

	out := m.resolveMentions("what does this do?")

	if out.Text != "what does this do?" {
		t.Errorf("Text = %q, want the prompt verbatim", out.Text)
	}
	if len(out.Attached)+len(out.Refused) != 0 {
		t.Errorf("a plain prompt produced attachments: %+v", out)
	}
}

func TestShellCommandOutputIsAttached(t *testing.T) {
	m, _ := newMentionFixture(t)

	out := m.resolveMentions("!echo hello-from-shell")

	if len(out.Refused) != 0 {
		t.Fatalf("Refused = %v, want the command to run", out.Refused)
	}
	if !strings.Contains(out.Text, "hello-from-shell") {
		t.Errorf("the output was not attached:\n%s", out.Text)
	}
	if !strings.Contains(out.Text, "!echo hello-from-shell") {
		t.Errorf("the command line is not named in the prompt:\n%s", out.Text)
	}
}

func TestShellCommandGoesThroughThePermissionEngine(t *testing.T) {
	m, dir := newMentionFixture(t)
	// A command whose effect is observable from the test, so a denied run is
	// distinguishable from one that simply failed.
	marker := filepath.Join(dir, "marker.txt")
		spec := "*marker*"
	engine := permission.New([]permission.Rule{{
		Tool:       "Bash",
		Specifier:  &spec,
		Decision:   permission.Deny,
	}})
	m.cfg.PermissionBridge = NewPermissionBridge(engine)

	out := m.resolveMentions("!echo written > " + marker)

	if len(out.Refused) != 1 {
		t.Fatalf("Refused = %v, want the deny rule to apply", out.Refused)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a denied command still ran")
	}
}

func TestBareBangIsNotACommand(t *testing.T) {
	// "!" alone and "!= ..." are things people type, not shell invocations.
	m, _ := newMentionFixture(t)

	for _, text := range []string{"why is x != y?", "wow!"} {
		out := m.resolveMentions(text)
		if len(out.Attached) != 0 || len(out.Refused) != 0 {
			t.Errorf("%q produced attachments: %+v", text, out)
		}
		if out.Text != text {
			t.Errorf("%q was rewritten to %q", text, out.Text)
		}
	}
}

func TestAtInsideAShellCommandIsNotAMention(t *testing.T) {
	// A command routinely contains @. Treating it as a mention would attach a
	// file nobody asked for.
	m, dir := newMentionFixture(t)
	writeMentionFile(t, dir, "private.go", "PRIVATE")

	out := m.resolveMentions("!echo user@example.com")

	for _, refused := range out.Refused {
		if strings.Contains(refused, "private.go") {
			t.Errorf("a @ inside a shell command was read as a mention: %v", out.Refused)
		}
	}
}

func TestHasAttachmentPrefix(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"plain question", false},
		{"look at @main.go", true},
		{"!echo hi", true},
		{"email me at bob", false},
		{"x != y", false},
	}
	for _, tc := range tests {
		if got := hasAttachmentPrefix(tc.text); got != tc.want {
			t.Errorf("hasAttachmentPrefix(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}

func TestAttachmentsResolveOffTheUpdatePath(t *testing.T) {
	// Expanding runs a shell, so it must not happen on the goroutine that
	// renders: startNextPrompt returns a command and the turn begins when the
	// result comes back through Update.
	m, dir := newMentionFixture(t)
	writeMentionFile(t, dir, "a.go", "AAA")

	model, cmd := m.startNextPrompt()

	if cmd == nil {
		t.Fatal("no command was returned")
	}
	if len(m.chatModel.Messages) != 0 {
		t.Error("the turn started before the attachments were resolved")
	}
	_, _ = model, dir
}

func TestAttachmentsReadyStartsTheTurn(t *testing.T) {
	m, dir := newMentionFixture(t)
	writeMentionFile(t, dir, "a.go", "AAA")
	m.expanding = true

	outcome := m.resolveMentions("look at @a.go")
	model, cmd := m.handleAttachmentsReady(attachmentsReadyMsg{visible: "look at @a.go", outcome: outcome})

	if m.expanding {
		t.Error("the model stayed busy after the attachments resolved")
	}
	if cmd == nil {
		t.Error("no turn was started")
	}
	_ = model
	_ = dir
}

func TestEverythingRefusedStartsNoTurn(t *testing.T) {
	// Nothing survived the check, so there is no question left to ask. Starting
	// a turn on the leftover prose would be worse than doing nothing.
	m, _ := newMentionFixture(t)
	m.expanding = true

	outcome := m.resolveMentions("read @../../../etc/passwd")
	_, cmd := m.handleAttachmentsReady(attachmentsReadyMsg{visible: "read @../../../etc/passwd", outcome: outcome})

	if cmd != nil {
		t.Error("a turn started with nothing left to send")
	}
	if !m.expanding {
		t.Error("the model is no longer marked busy")
	}
	notice := lastNotice(m)
	if !strings.Contains(notice, "Nothing left to send") {
		t.Errorf("notice = %q, want it to explain that nothing was sent", notice)
	}
}

func TestRefusalsAreShownBeforeTheTurnRuns(t *testing.T) {
	// A silent omission would leave the model answering a question the user
	// believed included a file.
	m, _ := newMentionFixture(t)
	m.expanding = true

	outcome := mentionOutcome{Text: "just text", Refused: []string{"@x.go was not attached: denied by rule"}}
	m.handleAttachmentsReady(attachmentsReadyMsg{visible: "read @x.go", outcome: outcome})

	if notice := lastNotice(m); !strings.Contains(notice, "denied by rule") {
		t.Errorf("notice = %q, want the refusal shown to the user", notice)
	}
}

func TestEscDuringExpansionDoesNotArmRewind(t *testing.T) {
	m, _ := newMentionFixture(t)
	m.expanding = true

	_, _, handled := m.handleInterruptKey(tea.Key{Code: tea.KeyEsc})

	if !handled {
		t.Fatal("Esc was not handled")
	}
	if m.escArmed {
		t.Error("Esc armed the rewind gesture while a shell command was running")
	}
	if m.rewindPicker != nil {
		t.Error("Esc opened the rewind picker while a shell command was running")
	}
}