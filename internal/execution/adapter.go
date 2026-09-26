package execution

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/procs"
)

// OSCommandExecutor implements CommandExecutor using OS exec.Cmd with process group isolation.
type OSCommandExecutor struct{}

// NewOSCommandExecutor creates a new OSCommandExecutor.
func NewOSCommandExecutor() *OSCommandExecutor {
	return &OSCommandExecutor{}
}

func (e *OSCommandExecutor) ExecuteCommand(ctx context.Context, command string, args []string, opts *CommandOptions) (*CommandResult, error) {
	if opts == nil {
		opts = &CommandOptions{}
	}

	execCtx := ctx
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(execCtx, command, args...)
	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}

	if len(opts.Env) > 0 {
		envMap := make(map[string]string)
		for _, e := range os.Environ() {
			if k, v, ok := strings.Cut(e, "="); ok {
				envMap[k] = v
			}
		}
		for k, v := range opts.Env {
			envMap[k] = v
		}
		envSlice := make([]string, 0, len(envMap))
		for k, v := range envMap {
			envSlice = append(envSlice, fmt.Sprintf("%s=%s", k, v))
		}
		cmd.Env = envSlice
	}

	if opts.Stdin != "" {
		cmd.Stdin = strings.NewReader(opts.Stdin)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	procs.Isolate(cmd)

	start := time.Now()
	err := cmd.Start()
	if err != nil {
		return nil, fmt.Errorf("starting command %s: %w", command, err)
	}

	waitErr := cmd.Wait()
	duration := time.Since(start)

	exitCode := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if ok := isExitError(waitErr, &exitErr); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	return &CommandResult{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration,
	}, nil
}

func isExitError(err error, target **exec.ExitError) bool {
	if err == nil {
		return false
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		*target = exitErr
		return true
	}
	return false
}
