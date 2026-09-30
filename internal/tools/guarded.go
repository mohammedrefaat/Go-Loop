package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/dimetron/pi-go/internal/permission"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
)

// The permission seam.
//
// This is the one place in pi-go where a tool call is checked against the
// user's permission rules, and it is deliberately the *outermost* wrapper:
// ADK's own tool.WithConfirmation cannot serve here, because its provider
// returns a bool and so can only request confirmation, never refuse one, and
// because it signals "await confirmation" by returning
// ErrConfirmationRequired for the ADK runner to loop back around. In a TUI the
// answer arrives asynchronously, so the correct behaviour is to hold the call
// until the user answers. Gating here, before the tool runs and after the
// argument coercion below, means every surface — TUI, ACP, headless, subagents
// — gets the same decision without each re-implementing it.

// Guard decides whether a tool call may proceed. A nil Guard in the config
// leaves tools unguarded, which is how the permission system stays opt-in at
// the wiring level even though its own default mode is permissive.
type Guard interface {
	Check(ctx context.Context, req permission.Request) (bool, permission.Result)
}

// permissionArgField names the argument field a rule matches on, per tool.
// A tool absent from this map is matched on an empty argument, so a
// `deny Bash` rule still works while a `Bash(rm *)` rule cannot fire against a
// tool whose argument shape is unknown.
//
// Keys are the tool's registered name in the exact case the tool layer uses;
// ToolName below is matched case-insensitively so a config that writes
// `Bash(...)` still resolves to the `bash` entry here.
var permissionArgField = map[string]string{
	"bash":            "command",
	"read":            "file_path",
	"read_image":      "file_path",
	"write":           "file_path",
	"edit":            "file_path",
	"grep":            "pattern",
	"find":            "pattern",
	"ls":              "path",
	"tree":            "path",
	"git-file-diff":   "file_path",
	"git-hunk":        "file_path",
	"lsp-code-action": "file_path",
	"session-stats":   "session_dir",
}

// guardedTool wraps a tool and consults a Guard before running it.
//
// It implements the ADK runnableTool shape, which is what the coercer in
// registry.go recognises, so guarding composes with the existing argument
// coercion rather than replacing it.
type guardedTool struct {
	name string
	tool.Tool
	guard Guard
}

// Run checks the permission rules, then delegates. A refused call returns an
// error instead of a result: the model needs to be told the call did not
// happen, because silently returning an empty result reads as success and the
// agent will carry on as though the file was written.
func (g *guardedTool) Run(ctx agent.Context, args any) (map[string]any, error) {
	allowed, res := g.guard.Check(ctx, permission.Request{
		Tool:        g.name,
		Arg:         g.permissionArg(args),
		Description: g.describe(args),
	})
	if !allowed {
		return nil, fmt.Errorf("tool %q was not run: %s", g.name, res.Reason)
	}
	// tool.Tool does not carry Run; only the runnable tools do. Asserting
	// rather than embedding mirrors coercingTool above, and an inner tool
	// that cannot run is reported rather than silently allowed to proceed.
	type runner interface {
		Run(agent.Context, any) (map[string]any, error)
	}
	r, ok := g.Tool.(runner)
	if !ok {
		return nil, fmt.Errorf("inner tool %s does not implement Run", g.name)
	}
	return r.Run(ctx, args)
}

// ProcessRequest forwards the inner tool's request processing.
//
// This is not optional. Embedding tool.Tool only promotes Name, Description
// and IsLongRunning, so without an explicit forward the wrapper advertises no
// ProcessRequest at all — and the ADK flow rejects the whole run with
// `tool "read" does not implement RequestProcessor() method`. The guard is a
// wrapper, not a replacement: everything about how the tool is presented to the
// model has to keep working, and only the run decision changes.
func (g *guardedTool) ProcessRequest(ctx agent.Context, req *model.LLMRequest) error {
	type processor interface {
		ProcessRequest(agent.Context, *model.LLMRequest) error
	}
	p, ok := g.Tool.(processor)
	if !ok {
		// The inner tool genuinely has no request processing, so there is
		// nothing to forward. Say so rather than inventing behaviour: the flow
		// will report it against the real tool.
		return fmt.Errorf("tool %q does not implement ProcessRequest()", g.name)
	}
	return p.ProcessRequest(ctx, req)
}

// permissionArg extracts the field the rules match on. The args are re-marshalled
// rather than type-asserted, because by this point in the stack they are a
// `map[string]any` produced by the coercer, and the struct form the tool
// builder declared is no longer what is in hand.
func (g *guardedTool) permissionArg(args any) string {
	field, ok := permissionArgField[strings.ToLower(g.name)]
	if !ok {
		return ""
	}
	m, ok := args.(map[string]any)
	if !ok {
		return ""
	}
	s, _ := m[field].(string)
	return s
}

// describe builds the one-line summary shown in an approval prompt. It is
// deliberately short: the full argument is rendered separately, and a prompt
// that starts with a 2000-character bash command is a prompt nobody reads.
func (g *guardedTool) describe(args any) string {
	arg := g.permissionArg(args)
	if arg == "" {
		return g.name
	}
	const maxArg = 120
	if len(arg) > maxArg {
		arg = arg[:maxArg] + "…"
	}
	return fmt.Sprintf("%s(%s)", g.name, arg)
}

// GuardTool wraps a single tool. It is exported so the ACP and headless
// surfaces can guard tools they build outside CoreTools, but it is a no-op
// when guard is nil.
func GuardTool(t tool.Tool, guard Guard) tool.Tool {
	if guard == nil || t == nil {
		return t
	}
	return &guardedTool{name: t.Name(), Tool: t, guard: guard}
}

// GuardTools guards a whole tool slice, preserving order and nil-guarded
// no-op behaviour.
func GuardTools(tools []tool.Tool, guard Guard) []tool.Tool {
	if guard == nil || len(tools) == 0 {
		return tools
	}
	out := make([]tool.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, GuardTool(t, guard))
	}
	return out
}

// EngineGuard adapts a *permission.Engine to the Guard interface. It is a thin
// wrapper rather than a direct implementation so the tools package does not
// own permission policy, and so a test can substitute a fixed verdict.
type EngineGuard struct {
	Engine *permission.Engine
}

// Check implements Guard.
func (e EngineGuard) Check(ctx context.Context, req permission.Request) (bool, permission.Result) {
	if e.Engine == nil {
		// No engine means no configured policy, which reads as allow. This is
		// the same default as permission.ModeAuto, and it keeps a partially
		// wired surface running rather than failing every call.
		return true, permission.Result{Decision: permission.Allow, Reason: "no permission engine configured"}
	}
	return e.Engine.Check(ctx, req)
}
