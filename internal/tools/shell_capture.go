package tools

import (
	"bytes"
	"context"
	"strings"
	"time"
)

// CaptureOutput runs a shell command in dir and returns what it printed.
//
// It exists for the TUI's `!cmd` mode, which runs a command on the user's
// behalf and attaches the output to the next prompt. That path is deliberately
// not the bash *tool*: the bash tool is the agent's, and it streams, enforces
// timeouts, and records output for the session log. `!cmd` is the user's, and
// wants one thing back — the text.
//
// The shell resolution is shared with the tool (shellCommand), so `!` means the
// same command language the agent would get. A mode where `!` quietly ran a
// different shell from Bash would make the permission rule the user wrote
// describe something other than what they see.
func CaptureOutput(ctx context.Context, dir, script string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := shellCommand(ctx, script)
	if dir != "" {
		// A relative path in the command means different things in different
		// directories, so the command runs where the session is rooted rather
		// than wherever the binary happened to start.
		cmd.Dir = dir
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	// stderr is folded into stdout: the user asked what the command printed,
	// and for most of them — a build, a test run — the failure is on stderr and
	// is the part worth reading.
	cmd.Stderr = &out

	err := cmd.Run()
	text := out.String()
	if err != nil {
		// A command that fails still has output worth attaching — usually the
		// error that explains why. Returning the text alongside the error lets
		// the caller decide; here the text is kept either way.
		return text, err
	}
	return text, nil
}

// ShellCommandSummary renders a command for display without running it, with
// newlines flattened so a multi-line paste cannot break a single-line prompt.
func ShellCommandSummary(script string) string {
	return strings.Join(strings.Fields(script), " ")
}