package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/outputstyle"
)

func TestResolveOutputStyleExplicitWinsOverRole(t *testing.T) {
	resetGlobalFlags(t)
	flagSmol = true
	flagOutputStyle = "deep"

	sel, warn := resolveOutputStyle()
	if sel.Name() != "deep" {
		t.Errorf("Name() = %q, want %q — an explicit --output-style must beat the role's implied voice", sel.Name(), "deep")
	}
	if sel.Source() != "explicit" {
		t.Errorf("Source() = %q, want %q", sel.Source(), "explicit")
	}
	if warn != "" {
		t.Errorf("warn = %q, want none for a known style", warn)
	}
}

func TestResolveOutputStyleRoleImpliesWhenNoExplicit(t *testing.T) {
	for role, want := range map[string]bool{"smol": true, "slow": true, "plan": true} {
		resetGlobalFlags(t)
		switch role {
		case "smol":
			flagSmol = true
		case "slow":
			flagSlow = true
		case "plan":
			flagPlan = true
		}
		sel, _ := resolveOutputStyle()
		if sel.Name() == "" {
			t.Errorf("role %q implies no output style; --%s would change the model but not the voice", role, role)
		}
		if sel.Source() != "role" {
			t.Errorf("role %q: Source() = %q, want %q", role, sel.Source(), "role")
		}
		_ = want
	}
}

func TestResolveOutputStyleDefaultRoleHasNoVoice(t *testing.T) {
	resetGlobalFlags(t)
	sel, warn := resolveOutputStyle()
	if sel.Name() != "" {
		t.Errorf("Name() = %q, want empty for the default role", sel.Name())
	}
	if warn != "" {
		t.Errorf("warn = %q, want none when no style was requested", warn)
	}
}

func TestResolveOutputStyleUnknownStyleWarnsButDoesNotFail(t *testing.T) {
	// A mistyped flag must not stop someone from working. The prompt is still
	// correct without a style, so this is a warning, not an error.
	resetGlobalFlags(t)
	flagOutputStyle = "no-such-style"

	sel, warn := resolveOutputStyle()
	if sel.Name() != "no-such-style" {
		t.Errorf("Name() = %q, want the requested name so the warning can name it", sel.Name())
	}
	if !strings.Contains(warn, "no-such-style") {
		t.Errorf("warn = %q, want it to name the unknown style", warn)
	}
	if !strings.Contains(warn, "terse") {
		t.Errorf("warn = %q, want it to list the available built-in styles", warn)
	}

	if text, _ := loadOutputStyle(); text != "" {
		t.Errorf("loadOutputStyle() = %q, want empty for an unknown style", text)
	}
}

func TestLoadOutputStyleForBuiltin(t *testing.T) {
	resetGlobalFlags(t)
	flagOutputStyle = "terse"

	text, warn := loadOutputStyle()
	if text == "" {
		t.Fatalf("loadOutputStyle() returned no text (warn=%q)", warn)
	}
	if !strings.Contains(text, "fewest words") {
		t.Errorf("loadOutputStyle() = %q, want the terse style's instruction", text)
	}
	if !strings.Contains(text, "# Output Style") {
		t.Errorf("loadOutputStyle() = %q, want the style section heading", text)
	}
}

func TestLoadOutputStyleIsCaseInsensitive(t *testing.T) {
	// --output-style Terse must work: the flag is typed by hand and the role
	// table uses display names.
	resetGlobalFlags(t)
	flagOutputStyle = "Terse"
	text, warn := loadOutputStyle()
	if text == "" {
		t.Errorf("loadOutputStyle() = %q (warn=%q), want the terse style", text, warn)
	}
}

func TestLoadOutputStyleEmptyWhenUnset(t *testing.T) {
	resetGlobalFlags(t)
	text, warn := loadOutputStyle()
	if text != "" || warn != "" {
		t.Errorf("loadOutputStyle() = (%q, %q), want both empty", text, warn)
	}
}

func TestLoadOutputStyleFromUserDirectory(t *testing.T) {
	resetGlobalFlags(t)
	flagOutputStyle = "custom"

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir reads this on Windows

	dir, dirErr := outputstyle.Dir()
	if dirErr != nil {
		t.Fatalf("outputstyle.Dir: %v", dirErr)
	}
	if err := writeStyleFile(dir, "custom.md", "---\nname: Custom\n---\nSpeak like a pirate.\n"); err != nil {
		t.Fatal(err)
	}

	text, warn := loadOutputStyle()
	if !strings.Contains(text, "pirate") {
		t.Errorf("loadOutputStyle() = %q (warn=%q), want the user style's instruction", text, warn)
	}
}

func TestUserStyleOverridesBuiltinOfTheSameName(t *testing.T) {
	// A user must be able to retune a shipped style without patching the
	// binary, so the user directory is consulted first.
	resetGlobalFlags(t)
	flagOutputStyle = "terse"

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	dir, dirErr := outputstyle.Dir()
	if dirErr != nil {
		t.Fatalf("outputstyle.Dir: %v", dirErr)
	}
	if err := writeStyleFile(dir, "terse.md", "---\nname: Terse\n---\nMY OWN VERSION\n"); err != nil {
		t.Fatal(err)
	}

	text, _ := loadOutputStyle()
	if !strings.Contains(text, "MY OWN VERSION") {
		t.Errorf("loadOutputStyle() = %q, want the user override", text)
	}
	if strings.Contains(text, "fewest words") {
		t.Error("loadOutputStyle() returned the built-in; the user override must win")
	}
}

// TestOutputStyleComposesWithInstructionParts is the integration point: the
// style leads the prompt, and the built-in prompt still follows it.
func TestOutputStyleComposesWithInstructionParts(t *testing.T) {
	resetGlobalFlags(t)
	flagSystem = ""
	flagSkillTouched = nil
	flagOutputStyle = "terse"
	flagSmol = false
	flagSlow = false
	flagPlan = false

	parts := buildDeferredInstructionParts()
	if !strings.Contains(parts.Base, "# Output Style") {
		t.Errorf("Base is missing the output style section:\n%s", head(parts.Base, 300))
	}
	if !strings.Contains(parts.Base, agent.SystemInstruction[:40]) {
		t.Error("Base is missing the built-in system prompt; a style must shape the register, not replace it")
	}
	if !strings.HasPrefix(parts.Base, "\n\n# Output Style") {
		t.Error("the style should lead, so the model reads it as the session's register")
	}
}

func TestSystemFlagWinsOverOutputStyle(t *testing.T) {
	// --system is a full replacement; composing a style into a prompt the
	// caller replaced outright would be a surprise.
	resetGlobalFlags(t)
	flagSystem = "be terse"
	flagOutputStyle = "deep"

	parts := buildDeferredInstructionParts()
	if parts.Base != "be terse" {
		t.Errorf("Base = %q, want exactly %q", parts.Base, "be terse")
	}
}

func TestNoOutputStyleLeavesBaseUnchanged(t *testing.T) {
	resetGlobalFlags(t)
	flagSystem = ""
	flagOutputStyle = ""
	flagSkillTouched = nil

	plain := buildDeferredInstructionParts()
	if strings.Contains(plain.Base, "# Output Style") {
		t.Error("no style was requested, so the prompt must be unchanged")
	}
}

func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func writeStyleFile(dir, name, content string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}
