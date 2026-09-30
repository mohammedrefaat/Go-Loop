package permission

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// mustParse is a test helper for building a rule set inline: a rule that
// fails to parse is a bug in the test, not a condition to assert on, and
// letting it through as a zero Rule would produce a confusing failure much
// later.
func mustParse(t *testing.T, specs ...string) []Rule {
	t.Helper()
	rules := make([]Rule, 0, len(specs))
	for _, s := range specs {
		r, err := ParseRule(s)
		if err != nil {
			t.Fatalf("ParseRule(%q): %v", s, err)
		}
		rules = append(rules, r)
	}
	return rules
}

func TestParseRule(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		wantErr     bool
		wantDec     Decision
		wantTool    string
		wantSpec    string // "" means the rule must be bare-name (nil specifier)
		wantNoMatch bool
	}{
		{name: "bare name defaults to allow", in: "bash", wantDec: Allow, wantTool: "bash"},
		{name: "explicit allow", in: "allow Bash", wantDec: Allow, wantTool: "Bash"},
		{name: "deny", in: "deny Bash", wantDec: Deny, wantTool: "Bash"},
		{name: "ask", in: "ask Bash(rm *)", wantDec: Ask, wantTool: "Bash", wantSpec: "rm *"},
		{name: "colon form", in: "allow:Bash(ls *)", wantDec: Allow, wantTool: "Bash", wantSpec: "ls *"},
		{name: "leading and trailing space", in: "   deny  Bash(git push *)  ", wantDec: Deny, wantTool: "Bash", wantSpec: "git push *"},
		{name: "path rule", in: "deny Read(.env)", wantDec: Deny, wantTool: "Read", wantSpec: ".env"},
		{name: "wildcard tool", in: "deny mcp__*", wantDec: Deny, wantTool: "mcp__*"},
		{name: "bare star", in: "ask Bash(*)", wantDec: Ask, wantTool: "Bash", wantSpec: "*"},
		{name: "empty specifier is bare name", in: "deny Read()", wantDec: Deny, wantTool: "Read"},
		{name: "glob allow is rejected", in: "allow mcp__*", wantErr: true},
		{name: "empty rule", in: "", wantErr: true},
		{name: "whitespace only", in: "   ", wantErr: true},
		{name: "decision with no tool", in: "deny", wantErr: true},
		{name: "missing closing paren", in: "deny Bash(ls *", wantErr: true},
		{name: "empty tool name", in: "deny (ls)", wantErr: true},
		{name: "stray closing paren", in: "deny ls)", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRule(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseRule(%q) = %+v, want an error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRule(%q): %v", tt.in, err)
			}
			if got.Decision != tt.wantDec {
				t.Errorf("decision = %v, want %v", got.Decision, tt.wantDec)
			}
			if got.Tool != tt.wantTool {
				t.Errorf("tool = %q, want %q", got.Tool, tt.wantTool)
			}
			if tt.wantSpec == "" {
				if got.Specifier != nil {
					t.Errorf("specifier = %q, want a bare-name rule", *got.Specifier)
				}
			} else if got.Specifier == nil {
				t.Errorf("specifier = nil, want %q", tt.wantSpec)
			} else if *got.Specifier != tt.wantSpec {
				t.Errorf("specifier = %q, want %q", *got.Specifier, tt.wantSpec)
			}
			if got.Source != tt.in && got.Source == "" {
				t.Errorf("Source is empty; a rule that reports no origin is hard to debug")
			}
		})
	}
}

// TestParseRule_BareToolNameIsNotADecision guards the word-boundary check: a
// tool genuinely named "asktheuser" must not be read as the decision "ask"
// applied to a tool called "theuser".
func TestParseRule_BareToolNameIsNotADecision(t *testing.T) {
	r, err := ParseRule("asktheuser")
	if err != nil {
		t.Fatalf("ParseRule: %v", err)
	}
	if r.Decision != Allow {
		t.Errorf("decision = %v, want allow (the default, not a split on %q)", r.Decision, "ask")
	}
	if r.Tool != "asktheuser" {
		t.Errorf("tool = %q, want %q", r.Tool, "asktheuser")
	}
}

func TestRule_Match(t *testing.T) {
	tests := []struct {
		name string
		rule string
		tool string
		arg  string
		want bool
	}{
		{name: "bare name matches any argument", rule: "deny Bash", tool: "bash", arg: "rm -rf /", want: true},
		{name: "bare name does not match another tool", rule: "deny Bash", tool: "read", arg: "x.go", want: false},
		{name: "exact specifier", rule: "allow Bash(ls)", tool: "bash", arg: "ls", want: true},
		{name: "exact specifier rejects a different command", rule: "allow Bash(ls)", tool: "bash", arg: "rm", want: false},
		{name: "trailing star matches a family", rule: "ask Bash(ls *)", tool: "bash", arg: "ls -la", want: true},
		{name: "trailing star does not match another command", rule: "ask Bash(ls *)", tool: "bash", arg: "cat x", want: false},
		{name: "bare star matches everything", rule: "ask Bash(*)", tool: "bash", arg: "anything at all", want: true},
		{name: "colon star matches everything", rule: "ask Bash(:*)", tool: "bash", arg: "anything at all", want: true},
		// The anchoring matters: a rule the user wrote for npm must not be
		// satisfied by a command that merely contains those words.
		{name: "prefix match is anchored", rule: "allow Bash(npm run build)", tool: "bash", arg: "sudo npm run build", want: false},
		{name: "prefix match still matches the command itself", rule: "allow Bash(npm run build)", tool: "bash", arg: "npm run build", want: true},
		{name: "mid-pattern star spans a slash", rule: "deny mcp__*", tool: "mcp__github__list", arg: "", want: true},
		{name: "glob tool does not match an unrelated name", rule: "deny mcp__*", tool: "read", arg: "", want: false},
		{name: "single-character glob", rule: "deny a?c", tool: "abc", arg: "", want: true},
		{name: "single-character glob rejects wrong length", rule: "deny a?c", tool: "abbc", arg: "", want: false},
		// A bash command with parentheses in it is the reason the parser
		// matches the outermost pair.
		{name: "parentheses inside the specifier", rule: "deny Bash(ls (a|b))", tool: "bash", arg: "ls (a|b)", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := mustParse(t, tt.rule)[0]
			if got := rule.Match(tt.tool, tt.arg); got != tt.want {
				t.Errorf("%s.Match(%q, %q) = %v, want %v", tt.rule, tt.tool, tt.arg, got, tt.want)
			}
		})
	}
}

func TestRule_MatchPath(t *testing.T) {
	tests := []struct {
		name string
		rule string
		path string
		want bool
	}{
		{name: "exact file", rule: "deny Read(.env)", path: ".env", want: true},
		{name: "different file", rule: "deny Read(.env)", path: "main.go", want: false},
		{name: "unanchored literal matches at any depth", rule: "deny Read(.env)", path: "config/.env", want: true},
		{name: "literal does not match a longer segment", rule: "deny Read(env)", path: "environment.go", want: false},
		{name: "glob matches at any depth", rule: "deny Read(*.pem)", path: "certs/server.pem", want: true},
		{name: "glob rejects a different extension", rule: "deny Read(*.pem)", path: "certs/server.key", want: false},
		{name: "path prefix is anchored at the working directory", rule: "deny Read(internal/*)", path: "internal/tui/tui.go", want: true},
		{name: "anchored pattern rejects a lookalike prefix", rule: "deny Read(internal/*)", path: "internal-old/tui.go", want: false},
		{name: "windows separators are normalized", rule: "deny Read(internal/*)", path: "internal\\tui\\tui.go", want: true},
		{name: "drive letter is stripped", rule: "deny Read(*.pem)", path: `C:\proj\certs\server.pem`, want: true},
		{name: "dot-slash prefix is ignored", rule: "deny Read(./.env)", path: ".env", want: true},
		{name: "directory rule covers its contents", rule: "deny Read(secrets)", path: "secrets/key.pem", want: true},
		{name: "rule with no path pattern never matches a path", rule: "deny Bash", path: "main.go", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := mustParse(t, tt.rule)[0]
			if got := rule.MatchPath(tt.path); got != tt.want {
				t.Errorf("%s.MatchPath(%q) = %v, want %v", tt.rule, tt.path, got, tt.want)
			}
		})
	}
}

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		pattern, s string
		want       bool
	}{
		{pattern: "*", s: "", want: true},
		{pattern: "*", s: "anything", want: true},
		{pattern: "**", s: "a/b/c", want: true},
		{pattern: "a*b", s: "ab", want: true},
		{pattern: "a*b", s: "axxxb", want: true},
		{pattern: "a*b", s: "axxxc", want: false},
		{pattern: "a*b*c", s: "a_b_c", want: true},
		{pattern: "a*b*c", s: "a_b_d", want: false},
		{pattern: "mcp__*", s: "mcp__github__list", want: true},
		{pattern: "*.pem", s: "a/b/c.pem", want: true},
		{pattern: "exact", s: "exact", want: true},
		{pattern: "exact", s: "exacts", want: false},
		{pattern: "a?c", s: "abc", want: true},
		{pattern: "a?c", s: "ac", want: false},
		// A pattern of only stars must not leave the matcher mid-pattern
		// after the subject is consumed.
		{pattern: "***", s: "", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"/"+tt.s, func(t *testing.T) {
			if got := globMatch(tt.pattern, tt.s); got != tt.want {
				t.Errorf("globMatch(%q, %q) = %v, want %v", tt.pattern, tt.s, got, tt.want)
			}
		})
	}
}

func TestEngine_NoRulesAllowsInAuto(t *testing.T) {
	e := New(nil)
	ok, res := e.Check(context.Background(), Request{Tool: "bash", Arg: "rm -rf /"})
	if !ok {
		t.Errorf("Check = false (%s); auto mode must not change existing behaviour", res.Reason)
	}
}

func TestEngine_DenyBeatsAllowRegardlessOfOrder(t *testing.T) {
	// The safety property, stated twice: in both rule orders the deny wins,
	// because evaluation is deny → ask → allow and not a single pass.
	orders := [][]string{
		{"allow Bash", "deny Bash(rm *)"},
		{"deny Bash(rm *)", "allow Bash"},
	}
	for _, rules := range orders {
		e := New(mustParse(t, rules...), WithMode(ModeDefault))
		ok, res := e.Check(context.Background(), Request{Tool: "bash", Arg: "rm -rf build"})
		if ok {
			t.Errorf("rules %v: Check = true, want false; an allow must not carve an exception out of a deny", rules)
		}
		if res.Decision != Deny {
			t.Errorf("rules %v: decision = %v, want deny", rules, res.Decision)
		}
		if res.Rule == nil {
			t.Errorf("rules %v: Rule is nil; the caller cannot explain the refusal", rules)
		}
	}
}

func TestEngine_AskBeatsAllow(t *testing.T) {
	e := New(
		mustParse(t, "allow Bash", "ask Bash(rm *)"),
		WithMode(ModeDefault),
		WithApprover(func(context.Context, Request) (bool, error) { return false, nil }),
	)
	ok, res := e.Check(context.Background(), Request{Tool: "bash", Arg: "rm file"})
	if ok {
		t.Errorf("Check = true, want false; ask outranks allow")
	}
	if res.Decision != Deny {
		t.Errorf("decision = %v, want deny (the ask was declined)", res.Decision)
	}
}

func TestEngine_AskApproved(t *testing.T) {
	var seen Request
	e := New(
		mustParse(t, "ask Bash(rm *)"),
		WithMode(ModeDefault),
		WithApprover(func(_ context.Context, r Request) (bool, error) {
			seen = r
			return true, nil
		}),
	)
	ok, res := e.Check(context.Background(), Request{Tool: "bash", Arg: "rm file", Description: "Bash(rm file)"})
	if !ok {
		t.Fatalf("Check = false (%s), want true once the approver agrees", res.Reason)
	}
	if seen.Arg != "rm file" {
		t.Errorf("the approver saw arg %q, want %q; the prompt needs the real argument", seen.Arg, "rm file")
	}
}

func TestEngine_ApproverErrorIsARefusal(t *testing.T) {
	e := New(
		mustParse(t, "ask Bash"),
		WithMode(ModeDefault),
		WithApprover(func(context.Context, Request) (bool, error) {
			return true, errors.New("prompt channel closed")
		}),
	)
	ok, res := e.Check(context.Background(), Request{Tool: "bash", Arg: "rm file"})
	if ok {
		t.Error("Check = true, want false; a failed prompt must not authorise the call")
	}
	if res.Reason == "" {
		t.Error("Reason is empty; the refusal must be explainable")
	}
}

func TestEngine_NoApproverRefusesAsk(t *testing.T) {
	e := New(mustParse(t, "ask Bash"), WithMode(ModeDefault))
	ok, res := e.Check(context.Background(), Request{Tool: "bash", Arg: "rm file"})
	if ok {
		t.Error("Check = true, want false; with no way to ask, an ask cannot be granted")
	}
	if res.Reason == "" {
		t.Error("Reason is empty")
	}
}

func TestEngine_DontAskRefusesRatherThanPrompting(t *testing.T) {
	called := false
	e := New(
		mustParse(t, "ask Bash"),
		WithMode(ModeDontAsk),
		WithApprover(func(context.Context, Request) (bool, error) {
			called = true
			return true, nil
		}),
	)
	ok, _ := e.Check(context.Background(), Request{Tool: "bash", Arg: "rm file"})
	if ok {
		t.Error("Check = true, want false")
	}
	if called {
		t.Error("the approver was called in dontAsk mode, want no prompt")
	}
}

func TestEngine_NonInteractiveDeniesByDefault(t *testing.T) {
	e := New(mustParse(t, "ask Bash"), WithMode(ModeDefault), WithNonInteractive(Deny))
	ok, _ := e.Check(context.Background(), Request{Tool: "bash", Arg: "rm file"})
	if ok {
		t.Error("Check = true, want false; an unattended run must fail loudly rather than block or proceed")
	}
}

func TestEngine_NonInteractivePolicyAllow(t *testing.T) {
	e := New(
		mustParse(t, "ask Bash"),
		WithMode(ModeDefault),
		WithApprover(func(context.Context, Request) (bool, error) {
			t.Error("the approver was called on a non-interactive surface")
			return true, nil
		}),
		WithNonInteractive(Allow),
	)
	ok, _ := e.Check(context.Background(), Request{Tool: "bash", Arg: "rm file"})
	if !ok {
		t.Error("Check = false, want true; the headless policy said allow")
	}
}

func TestEngine_BypassOverridesDeny(t *testing.T) {
	e := New(mustParse(t, "deny Bash(rm *)"), WithMode(ModeBypass))
	ok, res := e.Check(context.Background(), Request{Tool: "bash", Arg: "rm -rf /"})
	if !ok {
		t.Errorf("Check = false (%s), want true; bypassPermissions is the escape hatch from an over-broad deny", res.Reason)
	}
}

func TestEngine_PlanModeBlocksWrites(t *testing.T) {
	e := New(nil, WithMode(ModePlan))
	blocked := []Request{
		{Tool: "write", Arg: "main.go"},
		{Tool: "edit", Arg: "main.go"},
		{Tool: "bash", Arg: "go build ./..."},
		// Hyphenated names, which is how the tool layer registers them.
		{Tool: "git-hunk", Arg: ""},
	}
	for _, req := range blocked {
		if ok, _ := e.Check(context.Background(), req); ok {
			t.Errorf("plan mode allowed %s; it can change the working tree", req.Tool)
		}
	}

	allowed := []Request{
		{Tool: "read", Arg: "main.go"},
		{Tool: "grep", Arg: "func"},
		{Tool: "git-overview", Arg: ""},
		{Tool: "ls", Arg: "."},
	}
	for _, req := range allowed {
		if ok, res := e.Check(context.Background(), req); !ok {
			t.Errorf("plan mode blocked %s (%s); read-only investigation must stay available", req.Tool, res.Reason)
		}
	}
}

func TestEngine_PlanModeStillHonoursDeny(t *testing.T) {
	e := New(mustParse(t, "deny Read(secret.txt)"), WithMode(ModePlan))
	if ok, _ := e.Check(context.Background(), Request{Tool: "read", Arg: "secret.txt"}); ok {
		t.Error("Check = true, want false; a deny rule outranks the plan mode allowance")
	}
}

func TestEngine_SetModeIgnoresUnknownValues(t *testing.T) {
	e := New(nil, WithMode(ModeDefault))
	e.SetMode("nonsense")
	if e.Mode() != ModeDefault {
		t.Errorf("mode = %q, want it left at %q; a bad value must not silently disable enforcement", e.Mode(), ModeDefault)
	}
}

func TestEngine_NextModeCyclesThroughEveryMode(t *testing.T) {
	e := New(nil, WithMode(Modes[0]))
	seen := map[Mode]bool{}
	for range Modes {
		next := e.NextMode()
		if seen[next] {
			t.Fatalf("NextMode returned %q twice before covering every mode", next)
		}
		seen[next] = true
		e.SetMode(next)
	}
	if len(seen) != len(Modes) {
		t.Errorf("cycled through %d modes, want %d", len(seen), len(Modes))
	}
}

func TestEngine_CycleModeAppliesAsItAdvances(t *testing.T) {
	// The whole reason CycleMode exists rather than "call NextMode then
	// SetMode": the mode has to be the one it returns. Reading it back is what
	// distinguishes a real advance from a value that was computed and dropped.
	e := New(nil, WithMode(Modes[0]))
	for i := range Modes {
		want := Modes[(i+1)%len(Modes)]
		if got := e.CycleMode(); got != want {
			t.Fatalf("CycleMode = %q, want %q", got, want)
		}
		if got := e.Mode(); got != want {
			t.Fatalf("after CycleMode the engine is in %q, want %q — the mode was reported but not applied", got, want)
		}
	}
}

func TestEngine_CycleModeWrapsFromAnUnrecognisedMode(t *testing.T) {
	// A mode the engine does not recognise — hand-set, or from a config written
	// by a newer version — has to recover somewhere. The first entry is the most
	// restrictive, so recovering tightens rather than silently starting wide.
	e := New(nil, WithMode(Mode("somethingNewer")))
	if got, want := e.CycleMode(), Modes[0]; got != want {
		t.Fatalf("CycleMode from an unknown mode = %q, want %q", got, want)
	}
}

func TestEngine_CycleModeIsAtomicUnderConcurrency(t *testing.T) {
	// Two presses racing must advance by two, not land on the same mode: read
	// and write under one lock, or both read the same old mode and the second
	// silently overrides the first.
	e := New(nil, WithMode(Modes[0]))
	const presses = 8
	// Each press advances one step and the list wraps, so the expected mode is
	// just the start advanced by presses. An engine that lost updates under
	// contention lands somewhere else, which is what makes this fail visibly.
	want := Modes[presses%len(Modes)]

	var wg sync.WaitGroup
	start := make(chan struct{})
	for range presses {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			e.CycleMode()
		}()
	}
	close(start)
	wg.Wait()

	if got := e.Mode(); got != want {
		t.Fatalf("mode = %q after %d concurrent presses, want %q — updates were lost", got, presses, want)
	}
}

func TestEngine_SetRulesReplacesAtomically(t *testing.T) {
	e := New(mustParse(t, "deny Bash"), WithMode(ModeDefault))
	if ok, _ := e.Check(context.Background(), Request{Tool: "bash", Arg: "ls"}); ok {
		t.Fatal("the original deny rule did not apply")
	}
	e.SetRules(mustParse(t, "deny Read"))
	if ok, _ := e.Check(context.Background(), Request{Tool: "bash", Arg: "ls"}); !ok {
		t.Error("bash is still denied after the rule set was replaced; SetRules must replace, not merge")
	}
	if ok, _ := e.Check(context.Background(), Request{Tool: "read", Arg: "x"}); ok {
		t.Error("read is allowed despite the new deny rule")
	}
}

func TestRule_String(t *testing.T) {
	tests := []struct{ in, want string }{
		{in: "deny Bash", want: "deny Bash"},
		{in: "ask Bash(rm *)", want: "ask Bash(rm *)"},
		{in: "allow Read(.env)", want: "allow Read(.env)"},
		{in: "deny mcp__*", want: "deny mcp__*"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := mustParse(t, tt.in)[0].String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecision_String(t *testing.T) {
	for _, d := range []Decision{Allow, Ask, Deny, Decision(99)} {
		if d.String() == "" || d.String() == "unknown" && d < 3 {
			t.Errorf("Decision(%d).String() = %q", int(d), d.String())
		}
	}
}
