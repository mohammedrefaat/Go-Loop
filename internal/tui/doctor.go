package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/dimetron/pi-go/internal/audit"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/keymap"
	"github.com/dimetron/pi-go/internal/permission"
)

// /doctor composes the diagnostics pi-go already has rather than adding new
// ones. Each check answers a question a user actually asks when something is
// not working: is my config being read, are my permission rules doing what I
// think, are my keys bound, are my skills safe, what is this session running.
//
// Three things it deliberately does not do:
//
//   - It makes no network call. `pi ping` answers reachability with six phases
//     and a live model round-trip, which is the right tool when the question is
//     "can I reach the provider" and the wrong thing to gate a report on — a
//     report that hangs on an unreachable host cannot be used to find out why
//     the host is unreachable. /doctor names the model it resolved and points at
//     /ping, which is one keypress away.
//   - It does not stop at the first failure. Every check runs. A diagnostic that
//     stops early takes several runs to become useful.
//   - It does not exit the process. `runAudit` calls os.Exit; that is right for a
//     CI gate and wrong here, where the report is a message in a transcript.

// doctorStatus is one check's outcome.
type doctorStatus int

const (
	doctorOK doctorStatus = iota
	doctorWarn
	doctorFail
	// doctorSkip is for a check that could not run at all — no such thing as
	// failing to look for PATH tooling when PATH cannot be read. It is distinct
	// from OK so a skipped check is never mistaken for a passing one.
	doctorSkip
)

func (s doctorStatus) String() string {
	switch s {
	case doctorOK:
		return "ok"
	case doctorWarn:
		return "warn"
	case doctorFail:
		return "fail"
	case doctorSkip:
		return "skip"
	default:
		return "?"
	}
}

// doctorCheck is one diagnostic and what it found.
type doctorCheck struct {
	name string
	// detail is the headline: the one line that says what happened.
	detail string
	status doctorStatus
	// notes are the specifics — the rule that failed to parse, the file that
	// could not be read. Separate from detail because a check usually has one
	// summary and several reasons.
	notes []string
	// advice is what to do about it, when there is something concrete to do.
	advice string
}

// doctorReport is the full result, kept as data so it can be rendered and
// asserted on without going through slash-command dispatch.
type doctorReport struct {
	checks []doctorCheck
}

// attention counts the checks that need action, which is what the headline
// leads with. Warnings count: a binding that silently fell back to its default
// is something the user asked to be told about.
func (r doctorReport) attention() int {
	n := 0
	for _, c := range r.checks {
		if c.status == doctorFail || c.status == doctorWarn {
			n++
		}
	}
	return n
}

// handleDoctorCommand runs every diagnostic and appends the report.
func (m *model) handleDoctorCommand(args []string) (tea.Model, tea.Cmd) {
	m.inputModel.Clear()
	if len(args) != 0 {
		m.appendAssistant("`/doctor` takes no arguments. Run `/ping` to test provider connectivity, or `pi audit` to scan a single skill file.")
		return m, nil
	}
	m.appendAssistant(m.runDoctor().render())
	return m, nil
}

// runDoctor performs every check. Separated from the command so the whole report
// is testable.
func (m *model) runDoctor() doctorReport {
	return doctorReport{checks: []doctorCheck{
		m.doctorConfig(),
		m.doctorPermissions(),
		m.doctorKeymap(),
		m.doctorSkills(),
		m.doctorModel(),
		m.doctorTooling(),
		m.doctorEnvironment(),
	}}
}

// doctorConfig re-reads config from disk rather than trusting what the session
// started with.
//
// This is the check that catches the most common real problem: a session started
// before an edit to config.json, or an edit containing a typo. The running
// session holds the config it loaded at startup, so asking it whether the config
// is valid would answer about a file that may no longer exist.
func (m *model) doctorConfig() doctorCheck {
	const name = "config"
	cfg, err := config.LoadFrom(m.cwd())
	if err != nil {
		// A parse failure means pi-go could not read its own settings, so every
		// other check below is answering against defaults rather than what the
		// user wrote. That is a fail, not a warning.
		return doctorCheck{
			name:   name,
			status: doctorFail,
			detail: "config.json could not be read",
			notes:  []string{err.Error()},
			advice: "Fix the JSON syntax. Until then pi-go is running on built-in defaults.",
		}
	}

	// ResolveRole falls back from the requested role to "default", so a missing
	// role is only worth reporting for "default" itself.
	model, providerName, _, _, _, err := cfg.ResolveRole("default")
	if err != nil {
		return doctorCheck{
			name:   name,
			status: doctorFail,
			detail: "no usable default model role",
			notes:  append([]string{err.Error()}, configReadNote(m.cwd())...),
			advice: `Set a model, e.g. {"roles": {"default": {"model": "gpt-5.6-sol"}}}.`,
		}
	}

	// A model name with no recognized provider prefix falls back to
	// DefaultProvider, which can be the wrong provider entirely — and it fails
	// at request time with an auth error rather than at config time.
	var notes []string
	if !knownProviderPrefix(model) {
		provider := cfg.DefaultProvider
		if provider == "" {
			provider = "(none set)"
		}
		notes = append(notes, fmt.Sprintf(
			"%q has no recognized provider prefix, so the request will go to %s", model, provider))
	}
	notes = append(notes, configReadNote(m.cwd())...)

	return doctorCheck{
		name:   name,
		status: doctorOK,
		detail: fmt.Sprintf("readable; default role resolves to %s via %s", model, providerName),
		notes:  notes,
	}
}

// configReadNote names where config came from, in the order LoadFrom reads it,
// so "my edit did nothing" is answerable without a second command.
//
// The project path is described rather than resolved. Locating it means a second
// walk up the tree, and a second walk can disagree with the load it is meant to
// describe — which would make this note wrong in exactly the case the user ran
// the command to investigate.
func configReadNote(cwd string) []string {
	global := filepath.Join(".pi-go", "config.json")
	if home, err := os.UserHomeDir(); err == nil {
		global = filepath.Join(home, ".pi-go", "config.json")
	}
	return []string{
		"read: " + global,
		"then the nearest .pi-go/config.json walking up from " + cwd + ", if any",
	}
}

// knownProviderPrefix mirrors config's own auto-detection so this check can say
// "this will be inferred" rather than silently inferring. It is a copy rather
// than a call because config does not export its detector; it drifting out of
// date costs one possibly-stale hint, not a wrong verdict — the fail above does
// not depend on it.
func knownProviderPrefix(model string) bool {
	for _, p := range []string{"claude", "gpt", "gemini", "mistral", "magistral", "grok"} {
		if strings.HasPrefix(model, p) {
			return true
		}
	}
	return false
}

// doctorPermissions reports rules that failed to parse.
//
// A rule that does not parse is the failure that matters most here: it looks
// like policy in the config file and matches nothing at runtime, so the user
// believes something is protected that is not.
//
// It checks for malformed rules only. Cross-checking a rule's tool name against
// the live tool set — a rule naming `Bashh` — would need an enumeration of
// registered tools that pi-go does not expose, and guessing one from a partial
// list would report rules as dangling that are perfectly fine.
func (m *model) doctorPermissions() doctorCheck {
	const name = "permissions"
	cfg, err := config.LoadFrom(m.cwd())
	if err != nil {
		return doctorCheck{
			name:   name,
			status: doctorSkip,
			detail: "not checked — config could not be read",
		}
	}

	var declared int
	if cfg.Permissions != nil {
		declared = len(cfg.Permissions.Rules)
	}

	_, res := permission.FromConfig(cfg.Permissions)

	if len(res.Errors) > 0 {
		notes := make([]string, 0, len(res.Errors))
		for text, err := range res.Errors {
			notes = append(notes, fmt.Sprintf("%s: %v", text, err))
		}
		sort.Strings(notes)
		return doctorCheck{
			name:   name,
			status: doctorFail,
			detail: fmt.Sprintf("%d of %d rule(s) failed to parse and will never match", len(res.Errors), declared),
			notes:  notes,
			advice: "A rule that does not parse is inert, not protective. Fix or delete it.",
		}
	}

	// The engine the session is actually running can differ from the config on
	// disk, which is worth saying out loud: a user who edits the mode and then
	// runs /doctor should not conclude the edit took effect.
	detail := fmt.Sprintf("mode %s, %d rule(s), all parse", res.Mode, len(res.Rules))
	live := m.livePermissionMode()
	switch {
	case live != "" && live != res.Mode:
		detail += fmt.Sprintf(" — this session is running %s; restart to apply", live)
	case res.Mode == permission.ModeAuto:
		detail += " — every tool call is auto-approved"
	}

	return doctorCheck{name: name, status: doctorOK, detail: detail}
}

// livePermissionMode is the mode the running session enforces, or "" when no
// engine has arrived yet.
func (m *model) livePermissionMode() permission.Mode {
	if m.cfg.PermissionBridge == nil {
		return ""
	}
	engine := m.cfg.PermissionBridge.Engine()
	if engine == nil {
		return ""
	}
	return engine.Mode()
}

// doctorKeymap reports bindings the file could not use.
//
// Warnings are drain-once by design — they are surfaced once at startup — so
// asking again here is the only way to see them after the fact. Reload first:
// the file may have been edited since startup, and a stale report is worse than
// no report.
func (m *model) doctorKeymap() doctorCheck {
	const name = "keybindings"
	if m.keymap == nil {
		return doctorCheck{
			name:   name,
			status: doctorSkip,
			detail: "no keymap loaded — built-in keys are in use",
		}
	}

	if _, err := m.keymap.Reload(); err != nil {
		return doctorCheck{
			name:   name,
			status: doctorFail,
			detail: "keybindings.json could not be read",
			notes:  []string{err.Error()},
			advice: "Fix the JSON syntax. The last working bindings are still in effect.",
		}
	}

	path := m.keymap.Path()
	if path == "" {
		path = keymap.FilePath() + " (absent — defaults in use)"
	}

	// Draining here means the startup notice does not repeat them after the user
	// has already read them in /doctor.
	if warnings := m.keymap.Warnings(); len(warnings) > 0 {
		return doctorCheck{
			name:   name,
			status: doctorWarn,
			detail: fmt.Sprintf("%d binding(s) in %s could not be used", len(warnings), path),
			notes:  warnings,
			advice: "Each of these fell back to its default, or was skipped entirely.",
		}
	}

	bound := 0
	for _, c := range []string{keymap.ContextChat, keymap.ContextTranscript, keymap.ContextOverlay} {
		bound += len(m.keymap.Bound(c))
	}
	return doctorCheck{
		name:   name,
		status: doctorOK,
		detail: fmt.Sprintf("%d binding(s) active from %s", bound, path),
	}
}

// doctorSkills scans the loaded skill files for hidden characters — the same
// check `pi audit` runs, over the same directories.
func (m *model) doctorSkills() doctorCheck {
	const name = "skills"
	dirs := m.cfg.SkillDirs
	if len(dirs) == 0 {
		dirs = extension.DefaultSkillDirsIn(m.cwd())
	}

	result, err := audit.ScanSkillDirs(dirs...)
	if err != nil {
		return doctorCheck{
			name:   name,
			status: doctorFail,
			detail: "skill directories could not be scanned",
			notes:  []string{err.Error()},
			advice: "Check that the directories in `skillDirs` exist and are readable.",
		}
	}
	if len(result.Files) == 0 {
		return doctorCheck{
			name:   name,
			status: doctorOK,
			detail: fmt.Sprintf("no skill files found in %s", strings.Join(shortenAll(dirs), ", ")),
		}
	}

	critical, warning, info := result.CountBySeverity()
	detail := fmt.Sprintf("%d file(s) scanned: %d critical, %d warning, %d info",
		len(result.Files), critical, warning, info)

	switch {
	case critical > 0:
		notes := make([]string, 0, critical)
		for _, f := range result.Findings {
			if f.Severity == audit.SeverityCritical {
				notes = append(notes, fmt.Sprintf("%s:%d:%d  %s", shortenPath(f.File), f.Line, f.Col, f.Description))
			}
		}
		sort.Strings(notes)
		return doctorCheck{
			name:   name,
			status: doctorFail,
			detail: detail,
			notes:  notes,
			advice: "Run `pi audit` to review, or `pi audit --strip --dry-run` to preview removal.",
		}
	case warning > 0:
		return doctorCheck{
			name:   name,
			status: doctorWarn,
			detail: detail,
			advice: "These skills still load. Run `pi audit --verbose` for the full list.",
		}
	default:
		return doctorCheck{name: name, status: doctorOK, detail: detail}
	}
}

// doctorModel reports what the session resolved to. It says nothing about
// whether the provider is reachable: that is /ping, and it is separate because
// the answer takes seconds and fails for reasons unrelated to configuration.
func (m *model) doctorModel() doctorCheck {
	const name = "model"
	if m.cfg.ModelName == "" {
		return doctorCheck{
			name:   name,
			status: doctorFail,
			detail: "no model resolved for this session",
			advice: "Set one with `/model <name>`.",
		}
	}

	detail := m.cfg.ModelName + " via " + m.cfg.ProviderName
	switch {
	case m.cfg.ProviderName == "":
		return doctorCheck{
			name:   name,
			status: doctorWarn,
			detail: m.cfg.ModelName + " with no provider resolved",
			advice: "The provider is inferred per request, which can pick the wrong one.",
		}
	case m.cfg.ActiveRole != "" && m.cfg.ActiveRole != "default":
		detail += fmt.Sprintf(" (role %s)", m.cfg.ActiveRole)
	}

	return doctorCheck{
		name:   name,
		status: doctorOK,
		detail: detail,
		notes:  []string{"reachability is not tested here — run `/ping`"},
	}
}

// doctorTooling reports the external binaries pi-go shells out to. A missing one
// is invisible until a tool call needs it: the agent proposes a command, the
// shell says "not found", and nothing in the session records that the tool was
// never installed.
func (m *model) doctorTooling() doctorCheck {
	const name = "tooling"
	// git is the one that matters: /commit, the subagent worktree manager and
	// the hooks all assume it. The rest are conveniences the agent degrades from.
	required := []string{"git"}
	optional := []string{"rg", "golangci-lint", "make"}

	var missingRequired, missingOptional []string
	for _, bin := range required {
		if _, err := exec.LookPath(bin); err != nil {
			missingRequired = append(missingRequired, bin)
		}
	}
	for _, bin := range optional {
		if _, err := exec.LookPath(bin); err != nil {
			missingOptional = append(missingOptional, bin)
		}
	}

	var notes []string
	if found := foundAmong(optional, missingOptional); len(found) > 0 {
		notes = append(notes, "also found: "+strings.Join(found, ", "))
	}

	if len(missingRequired) > 0 {
		return doctorCheck{
			name:   name,
			status: doctorFail,
			detail: fmt.Sprintf("required tooling not on PATH: %s", strings.Join(missingRequired, ", ")),
			notes:  notes,
			advice: "Features that shell out to it will fail at call time, not at startup.",
		}
	}

	detail := "git found"
	if len(missingOptional) > 0 {
		detail += fmt.Sprintf("; not installed: %s", strings.Join(missingOptional, ", "))
	}
	return doctorCheck{name: name, status: doctorOK, detail: detail, notes: notes}
}

// foundAmong lists the optional tools that were present, so an all-missing list
// does not read as "none of these work" when only one is absent.
func foundAmong(optional, missing []string) []string {
	gone := make(map[string]bool, len(missing))
	for _, b := range missing {
		gone[b] = true
	}
	var found []string
	for _, b := range optional {
		if !gone[b] {
			found = append(found, b)
		}
	}
	return found
}

// doctorEnvironment reports the session context: version, session, working
// directory, log file. These are the facts a bug report needs and the ones a
// user cannot otherwise see from inside the TUI.
func (m *model) doctorEnvironment() doctorCheck {
	notes := []string{
		"version: " + orNone(m.cfg.AppVersion),
		"session: " + orNone(m.cfg.SessionID),
		"cwd: " + m.cwd(),
	}
	if m.cfg.Logger != nil {
		if path := m.cfg.Logger.Path(); path != "" {
			notes = append(notes, "log: "+path)
		}
	}
	return doctorCheck{
		name:   "environment",
		status: doctorOK,
		detail: "pi-go " + orNone(m.cfg.AppVersion),
		notes:  notes,
	}
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(unknown)"
	}
	return s
}

// render formats the report as markdown for the transcript.
func (r doctorReport) render() string {
	var b strings.Builder

	n := r.attention()
	headline := "All checks passed."
	if n > 0 {
		headline = fmt.Sprintf("%d of %d checks need attention.", n, len(r.checks))
	}
	fmt.Fprintf(&b, "**Diagnostics** — %s\n", headline)

	for _, c := range r.checks {
		fmt.Fprintf(&b, "\n%s **%s** (%s) — %s\n", doctorMark(c.status), c.name, c.status, c.detail)
		for _, note := range c.notes {
			fmt.Fprintf(&b, "  - %s\n", note)
		}
		if c.advice != "" {
			fmt.Fprintf(&b, "  - _%s_\n", c.advice)
		}
	}
	return b.String()
}

// doctorMark is the glyph for a status. The status word is written next to it
// as well, because the glyph is decoration and the word is what survives a
// paste into an issue.
func doctorMark(s doctorStatus) string {
	switch s {
	case doctorOK:
		return "✓"
	case doctorWarn:
		return "!"
	case doctorFail:
		return "✗"
	default:
		return "–"
	}
}

// shortenAll abbreviates each path for a one-line message, keeping the last two
// segments — enough to tell ~/.pi-go/skills from .claude/skills, which is the
// distinction that matters when several are configured.
func shortenAll(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, shortenPath(p))
	}
	return out
}

func shortenPath(p string) string {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(p)), "/")
	if len(parts) <= 2 {
		return p
	}
	return ".../" + strings.Join(parts[len(parts)-2:], "/")
}
