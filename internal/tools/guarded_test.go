package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/permission"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
)

// fakeGuard records what it was asked and returns a fixed verdict, so a test
// can assert both that the seam was consulted with the right values and that
// the answer was honoured.
type fakeGuard struct {
	allow    bool
	reason   string
	calls    []permission.Request
	rawArgs  []any
	ctxIsSet bool
}

func (g *fakeGuard) Check(_ context.Context, req permission.Request) (bool, permission.Result) {
	g.calls = append(g.calls, req)
	return g.allow, permission.Result{Decision: permission.Allow, Reason: g.reason}
}

type guardedTestInput struct {
	FilePath string `json:"file_path"`
	Command  string `json:"command,omitempty"`
}

type guardedTestOutput struct {
	Result string `json:"result"`
}

// makeGuardedTool builds a tool named "bash", so the guard resolves its
// argument through the "command" field just as the real bash tool would.
func makeGuardedTool(t *testing.T) tool.Tool {
	t.Helper()
	inner, err := newTool("bash", "test tool", func(_ agent.Context, input guardedTestInput) (guardedTestOutput, error) {
		return guardedTestOutput{Result: "ran with " + input.Command}, nil
	})
	if err != nil {
		t.Fatalf("newTool: %v", err)
	}
	return inner
}

func TestGuardTool_AllowedRunsTheTool(t *testing.T) {
	inner := makeGuardedTool(t)
	g := &fakeGuard{allow: true}
	guarded := GuardTool(inner, g)

	out, err := guarded.(*guardedTool).Run(mockToolCtx{Context: context.Background()}, map[string]any{"command": "ls -la"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out["result"] != "ran with ls -la" {
		t.Errorf("result = %v, want the inner tool to have run", out["result"])
	}
	if len(g.calls) != 1 {
		t.Fatalf("guard consulted %d times, want 1", len(g.calls))
	}
	if g.calls[0].Tool != "bash" {
		t.Errorf("guard saw tool %q, want %q", g.calls[0].Tool, "bash")
	}
	if g.calls[0].Arg != "ls -la" {
		t.Errorf("guard saw arg %q, want %q; the rule needs the real argument", g.calls[0].Arg, "ls -la")
	}
}

func TestGuardTool_DeniedNeverRunsTheTool(t *testing.T) {
	inner := makeGuardedTool(t)
	g := &fakeGuard{allow: false, reason: "denied by rule deny bash"}
	guarded := GuardTool(inner, g)

	out, err := guarded.(*guardedTool).Run(mockToolCtx{Context: context.Background()}, map[string]any{"command": "ls -la"})
	if err == nil {
		t.Fatal("Run returned no error, want a refusal; an empty result reads as success to the model")
	}
	if out != nil {
		t.Errorf("result = %v, want nil on a refused call", out)
	}
	// The reason must reach the model, or it will retry the same call.
	if !strings.Contains(err.Error(), "denied by rule deny bash") {
		t.Errorf("error %q does not carry the reason", err)
	}
	if !strings.Contains(err.Error(), "bash") {
		t.Errorf("error %q does not name the tool", err)
	}
}

func TestGuardTool_NilGuardIsAPassThrough(t *testing.T) {
	inner := makeGuardedTool(t)
	if got := GuardTool(inner, nil); got != inner {
		t.Error("GuardTool with a nil guard changed the tool; an unopted-in surface must behave as before")
	}
}

func TestGuardTools_PreservesOrderAndCount(t *testing.T) {
	in := []tool.Tool{makeGuardedTool(t), makeGuardedTool(t), makeGuardedTool(t)}
	out := GuardTools(in, &fakeGuard{allow: true})
	if len(out) != len(in) {
		t.Fatalf("got %d tools, want %d", len(out), len(in))
	}
	for i := range out {
		if _, ok := out[i].(*guardedTool); !ok {
			t.Errorf("tool %d is %T, want *guardedTool", i, out[i])
		}
	}
}

func TestGuardTools_NilGuardReturnsInputUnchanged(t *testing.T) {
	in := []tool.Tool{makeGuardedTool(t)}
	out := GuardTools(in, nil)
	if len(out) != 1 {
		t.Fatalf("got %d tools, want 1", len(out))
	}
	if _, ok := out[0].(*guardedTool); ok {
		t.Error("a nil guard still wrapped the tool")
	}
}

// TestGuardTools_CoreToolsHonoursTheGuard is the end-to-end check that the
// option actually reaches the tools the rest of pi-go uses, not just the
// wrapper in isolation.
func TestGuardTools_CoreToolsHonoursTheGuard(t *testing.T) {
	// testSandbox (tools_test.go) registers sb.Close(), which is what lets
	// t.TempDir clean up on Windows; a bare NewSandbox leaves the directory
	// open and the test fails at teardown with no assertion output.
	sb := testSandbox(t, t.TempDir())
	// CoreTools builds a private bash supervisor when none is supplied; it
	// keeps process handles, so the test has to release them too.
	sup := NewBashSupervisor()
	t.Cleanup(sup.KillAll)
	g := &fakeGuard{allow: false, reason: "denied for the test"}
	tools, err := CoreTools(sb, WithBashSupervisor(sup), WithGuard(g))
	if err != nil {
		t.Fatalf("CoreTools: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("CoreTools returned no tools")
	}
	for _, tl := range tools {
		gt, ok := tl.(*guardedTool)
		if !ok {
			t.Fatalf("tool %q is %T, want *guardedTool; the guard must be the outermost wrapper", tl.Name(), tl)
		}
		if _, err := gt.Run(mockToolCtx{Context: context.Background()}, map[string]any{}); err == nil {
			t.Errorf("tool %q ran despite a denying guard", tl.Name())
		}
	}
	if len(g.calls) != len(tools) {
		t.Errorf("guard consulted %d times for %d tools", len(g.calls), len(tools))
	}
}

// TestGuardTool_PermissionArgFieldNames pins the field each rule matches on.
// A wrong field name here is silent: the guard is still consulted, it just
// compares the rule against an empty string, so every scoped rule stops
// firing while bare-name rules keep working.
func TestGuardTool_PermissionArgFieldNames(t *testing.T) {
	tests := []struct {
		tool     string
		argKey   string
		argValue string
	}{
		{tool: "bash", argKey: "command", argValue: "rm -rf build"},
		{tool: "read", argKey: "file_path", argValue: "internal/tui/tui.go"},
		{tool: "write", argKey: "file_path", argValue: "main.go"},
		{tool: "edit", argKey: "file_path", argValue: "main.go"},
		{tool: "git-file-diff", argKey: "file_path", argValue: "main.go"},
		{tool: "git-hunk", argKey: "file_path", argValue: "main.go"},
		{tool: "read_image", argKey: "file_path", argValue: "a.png"},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			g := &guardedTool{name: tt.tool}
			if got := g.permissionArg(map[string]any{tt.argKey: tt.argValue}); got != tt.argValue {
				t.Errorf("permissionArg = %q, want %q", got, tt.argValue)
			}
		})
	}
}

// TestGuardTool_PermissionArgUnknownTool checks the documented fallback: a tool
// with no known argument field yields an empty argument, so a bare-name rule
// still applies rather than the call skipping the check entirely.
func TestGuardTool_PermissionArgUnknownTool(t *testing.T) {
	g := &guardedTool{name: "mcp__github__list"}
	if got := g.permissionArg(map[string]any{"repo": "x"}); got != "" {
		t.Errorf("permissionArg = %q, want empty for an unmapped tool", got)
	}
}

func TestGuardTool_Describe(t *testing.T) {
	tests := []struct {
		name  string
		tool  string
		args  map[string]any
		want  string
		short bool
	}{
		{name: "with an argument", tool: "bash", args: map[string]any{"command": "ls -la"}, want: "bash(ls -la)"},
		{name: "without an argument", tool: "read", args: map[string]any{}, want: "read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &guardedTool{name: tt.tool}
			if got := g.describe(tt.args); got != tt.want {
				t.Errorf("describe = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestGuardTool_DescribeTruncates covers the long-command case: an approval
// prompt that opens with a 2000-character command is a prompt nobody reads.
func TestGuardTool_DescribeTruncates(t *testing.T) {
	g := &guardedTool{name: "bash"}
	long := strings.Repeat("x", 500)
	got := g.describe(map[string]any{"command": long})
	if len([]rune(got)) > 140 {
		t.Errorf("describe produced %d characters; the argument is not truncated", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…)") {
		t.Errorf("describe = %q, want a truncation marker", got)
	}
}

// TestEngineGuard_EndToEnd proves the real engine drives the real wrapper: a
// deny rule on bash must stop a bash call whose argument the rule names.
func TestEngineGuard_EndToEnd(t *testing.T) {
	rule, err := permission.ParseRule("deny Bash(rm *)")
	if err != nil {
		t.Fatalf("ParseRule: %v", err)
	}
	engine := permission.New([]permission.Rule{rule}, permission.WithMode(permission.ModeDefault))
	guard := EngineGuard{Engine: engine}

	inner := makeGuardedTool(t)
	guarded := GuardTool(inner, guard)

	// The rule names rm, so an rm command must be refused...
	if _, err := guarded.(*guardedTool).Run(mockToolCtx{Context: context.Background()}, map[string]any{"command": "rm -rf build"}); err == nil {
		t.Error("an rm command ran despite `deny Bash(rm *)`")
	}
	// ...and a different command must not be caught by the same rule.
	g := &guardedTool{name: "bash"}
	if got := g.permissionArg(map[string]any{"command": "ls -la"}); got != "ls -la" {
		t.Fatalf("permissionArg = %q", got)
	}
}

// TestEngineGuard_NilEngineAllows pins the documented fallback for a partially
// wired surface.
func TestEngineGuard_NilEngineAllows(t *testing.T) {
	ok, res := EngineGuard{}.Check(context.Background(), permission.Request{Tool: "bash", Arg: "rm -rf /"})
	if !ok {
		t.Errorf("Check = false (%s), want true when no engine is configured", res.Reason)
	}
}
