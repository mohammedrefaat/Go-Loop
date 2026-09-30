package permission

import (
	"strings"
	"testing"
)

// TestLoad_EmptyInputIsAutoAndUngated pins the promise that a config with no
// permissions section leaves the agent behaving exactly as it did before the
// permission layer existed. If this ever fails, every existing user's default
// behaviour has changed under them.
func TestLoad_EmptyInputIsAutoAndUngated(t *testing.T) {
	engine, res := Load("", nil)
	if engine.Mode() != ModeAuto {
		t.Errorf("mode = %q, want %q", engine.Mode(), ModeAuto)
	}
	if len(res.Rules) != 0 {
		t.Errorf("got %d rules, want none", len(res.Rules))
	}
	if len(res.Errors) != 0 {
		t.Errorf("got %d errors, want none", len(res.Errors))
	}
	// auto with no rules allows a mutating call, because auto is permissive and
	// there is no rule against it.
	allowed, _ := engine.Check(t.Context(), Request{Tool: "bash", Arg: "rm -rf build"})
	if !allowed {
		t.Error("an empty config refused a bash call; the default must be unchanged")
	}
}

// TestLoad_SkipsBlanksAndComments covers the two ways of writing nothing. A
// commented rule is a normal thing for a config to contain, and reporting it as
// broken would make a valid file look invalid.
func TestLoad_SkipsBlanksAndComments(t *testing.T) {
	engine, res := Load("default", []string{
		"",
		"   ",
		"# deny Bash(rm *)",
		"\t# indented comment",
		"deny Bash(rm *)",
	})
	if len(res.Errors) != 0 {
		t.Errorf("blanks and comments reported as errors: %v", res.Errors)
	}
	if len(res.Rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(res.Rules))
	}
	allowed, _ := engine.Check(t.Context(), Request{Tool: "bash", Arg: "rm -rf build"})
	if allowed {
		t.Error("the one real rule did not take effect")
	}
}

// TestLoad_OneBadRuleDoesNotDiscardTheRest is the reason Load exists in this
// shape: dropping a whole file over a typo leaves the user with no permissions
// at all and no obvious cause.
func TestLoad_OneBadRuleDoesNotDiscardTheRest(t *testing.T) {
	engine, res := Load("default", []string{
		"deny Bash(rm *)",
		"ask Read(.env)",
		"nonsense rule with no decision prefix",
		"deny Bash(unclosed",
	})
	if len(res.Rules) != 2 {
		t.Errorf("kept %d rules, want the 2 valid ones", len(res.Rules))
	}
	if len(res.Errors) != 2 {
		t.Fatalf("got %d errors, want 2: %v", len(res.Errors), res.Errors)
	}
	// The surviving deny rule must still be enforced by the returned engine.
	allowed, _ := engine.Check(t.Context(), Request{Tool: "bash", Arg: "rm -rf build"})
	if allowed {
		t.Error("the valid rule was not enforced after a sibling failed to parse")
	}
}

// TestLoad_CatchesATypoThatWouldOtherwiseBeInert is the case the errors exist
// for. A dropped decision prefix parses as an allow rule for a tool named after
// the whole sentence; it never matches anything, so without the parse check the
// user has a rule in their config that does nothing and no way to find out.
func TestLoad_CatchesATypoThatWouldOtherwiseBeInert(t *testing.T) {
	_, res := Load("auto", []string{"nonsense rule with no decision prefix"})
	if len(res.Errors) != 1 {
		t.Fatalf("a sentence with no decision prefix loaded cleanly: %v", res.Rules)
	}
	if !strings.Contains(res.DescribeErrors(), "not a tool name") {
		t.Errorf("DescribeErrors = %q, want it to explain the mistake", res.DescribeErrors())
	}
}

// TestLoad_AllowRuleWithAGlobInTheSpecifierIsValid guards against over-calling
// a rule broken: `allow Bash(mcp__*)` puts the glob in the specifier, where it
// scopes which bash commands are allowed, and is a rule someone would write.
func TestLoad_AllowRuleWithAGlobInTheSpecifierIsValid(t *testing.T) {
	_, res := Load("auto", []string{"allow Bash(mcp__*)"})
	if len(res.Errors) != 0 {
		t.Errorf("a scoped allow rule was rejected: %v", res.Errors)
	}
	if len(res.Rules) != 1 {
		t.Errorf("kept %d rules, want 1", len(res.Rules))
	}
}

func TestLoad_ResolveMode(t *testing.T) {
	tests := []struct {
		in   string
		want Mode
	}{
		{"", ModeAuto},
		{"   ", ModeAuto},
		{"default", ModeDefault},
		{"plan", ModePlan},
		{"acceptEdits", ModeAcceptEdits},
		{"auto", ModeAuto},
		{"dontAsk", ModeDontAsk},
		{"bypassPermissions", ModeBypass},
		// An unrecognised mode must not leave the agent unable to act, and must
		// not silently become something more restrictive than asked for.
		{"nonsense", ModeAuto},
		{"PLAN", ModeAuto},
		{" default ", ModeDefault},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			engine, res := Load(tt.in, nil)
			if got := engine.Mode(); got != tt.want {
				t.Errorf("mode = %q, want %q", got, tt.want)
			}
			if res.Mode != tt.want {
				t.Errorf("LoadResult.Mode = %q, want %q", res.Mode, tt.want)
			}
		})
	}
}

// TestLoad_ModeIsCarriedIntoTheEngine checks the two halves agree — a caller
// reading res.Mode for a status line must not be told something different from
// what the engine will enforce.
func TestLoad_ModeIsCarriedIntoTheEngine(t *testing.T) {
	_, res := Load("plan", nil)
	if res.Mode != ModePlan {
		t.Fatalf("LoadResult.Mode = %q, want %q", res.Mode, ModePlan)
	}
}

func TestDescribeErrors_EmptyIsEmptyString(t *testing.T) {
	_, res := Load("auto", []string{"deny Bash(rm *)"})
	if got := res.DescribeErrors(); got != "" {
		t.Errorf("DescribeErrors = %q, want empty when everything parsed", got)
	}
}

// TestDescribeErrors_NamesEveryBadRule guards the reason the errors exist: the
// user has to be able to see which line of their config was ignored, or they
// will believe a rule is protecting something when it is not.
func TestDescribeErrors_NamesEveryBadRule(t *testing.T) {
	_, res := Load("auto", []string{
		"nonsense rule with no decision prefix",
		"deny Bash(unclosed",
		"deny Bash(rm *)",
	})
	got := res.DescribeErrors()
	if got == "" {
		t.Fatal("DescribeErrors is empty despite two bad rules")
	}
	for _, want := range []string{"nonsense rule with no decision prefix", "deny Bash(unclosed"} {
		if !strings.Contains(got, want) {
			t.Errorf("DescribeErrors = %q, does not name %q", got, want)
		}
	}
	if strings.Contains(got, "deny Bash(rm *)") {
		t.Errorf("DescribeErrors = %q, names the rule that parsed fine", got)
	}
}

// TestDescribeErrors_IsStable guards the sort. Map iteration order is random, so
// without it the same bad config produces a different message on every launch
// and the user cannot tell whether anything changed.
func TestDescribeErrors_IsStable(t *testing.T) {
	rules := []string{
		"zzz is not a rule",
		"aaa is not a rule",
		"mmm is not a rule",
		"deny Bash(unclosed",
	}
	_, first := Load("auto", rules)
	want := first.DescribeErrors()
	for i := 0; i < 20; i++ {
		_, again := Load("auto", rules)
		if got := again.DescribeErrors(); got != want {
			t.Fatalf("DescribeErrors = %q on run %d, want the stable %q", got, i, want)
		}
	}
}
