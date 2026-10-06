package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	adkmodel "google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/budget"
)

// The tests below cover the CI-facing contract: the default dialect is
// unchanged, stream-json adds accounting, and a budget stop ends the run with
// a non-nil error so the process exits non-zero.

// loopLLM calls a tool forever, which is the shape --max-turns exists to stop.
// Without a ceiling this never terminates.
type loopLLM struct {
	name  string
	mu    sync.Mutex
	calls int
}

func (m *loopLLM) Name() string { return m.name }

func (m *loopLLM) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *loopLLM) GenerateContent(_ context.Context, _ *adkmodel.LLMRequest, _ bool) iter.Seq2[*adkmodel.LLMResponse, error] {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	return func(yield func(*adkmodel.LLMResponse, error) bool) {
		yield(&adkmodel.LLMResponse{
			Content: &genai.Content{
				Role:  genai.RoleModel,
				Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: "sleep", Args: map[string]any{"ms": 1}}}},
			},
		}, nil)
	}
}

// parseJSONLines decodes JSONL output into events, failing the test on a line
// that is not valid JSON.
func parseJSONLines(t *testing.T, stdout string) []jsonEvent {
	t.Helper()
	var out []jsonEvent
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev jsonEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		out = append(out, ev)
	}
	return out
}

// eventsOfType filters a decoded stream.
func eventsOfType(events []jsonEvent, typ string) []jsonEvent {
	var out []jsonEvent
	for _, ev := range events {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

// lastEventOfType returns the final matching event, or nil.
func lastEventOfType(events []jsonEvent, typ string) *jsonEvent {
	matches := eventsOfType(events, typ)
	if len(matches) == 0 {
		return nil
	}
	return &matches[len(matches)-1]
}

func TestResolveJSONDialect(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    jsonDialect
		wantErr bool
	}{
		{name: "unset is the pi dialect", value: "", want: jsonPi},
		{name: "pi is the pi dialect", value: "pi", want: jsonPi},
		{name: "stream-json selects accounting", value: "stream-json", want: jsonStream},
		{name: "surrounding space is tolerated", value: "  stream-json  ", want: jsonStream},
		{name: "an unknown name is an error", value: "yaml", wantErr: true},
		{name: "a near miss is an error", value: "stream_json", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			orig := flagOutputFormat
			flagOutputFormat = tc.value
			t.Cleanup(func() { flagOutputFormat = orig })

			got, err := resolveJSONDialect()
			if tc.wantErr {
				// A hard error, not a warning: a CI job that asked for
				// stream-json and silently got the other dialect fails in a way
				// that looks like the agent misbehaved.
				if err == nil {
					t.Fatalf("resolveJSONDialect() = %v, want an error for %q", got, tc.value)
				}
				if !strings.Contains(err.Error(), tc.value) {
					t.Errorf("error %q does not name the bad value %q", err, tc.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveJSONDialect() error: %v", err)
			}
			if got != tc.want {
				t.Errorf("resolveJSONDialect() = %v, want %v", got, tc.want)
			}
		})
	}
}

// The default dialect must be byte-identical to what it was before the flag
// existed: a consumer parsing it today cannot break.
func TestRunJSONDefaultDialectIsUnchanged(t *testing.T) {
	llm := &cliMockLLM{name: "m", response: "hello there"}
	ag, sessionID := newTestAgent(t, llm)

	var stdout string
	func() {
		orig := flagOutputFormat
		flagOutputFormat = ""
		defer func() { flagOutputFormat = orig }()
		stdout = captureStdout(t, func() {
			if err := runJSON(context.Background(), ag, sessionID, "hi", nil, nil); err != nil {
				t.Fatalf("runJSON: %v", err)
			}
		})
	}()

	events := parseJSONLines(t, stdout)
	if len(events) == 0 {
		t.Fatal("no events emitted")
	}
	if events[0].Type != "message_start" {
		t.Errorf("first event = %q, want message_start — no system/init event may precede it", events[0].Type)
	}
	if last := events[len(events)-1]; last.Type != "message_end" {
		t.Errorf("last event = %q, want message_end — no result event may follow it", last.Type)
	}
	for _, typ := range []string{"system", "turn", "result"} {
		if got := eventsOfType(events, typ); len(got) != 0 {
			t.Errorf("%d %q events in the default dialect, want 0", len(got), typ)
		}
	}
	// No raw line may carry a "usage" key. omitempty does not apply to a struct
	// value, so this only holds while Usage is a pointer — the regression that
	// put an all-zero usage object on every line of the default dialect.
	for i, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", i, err)
		}
		if _, ok := raw["usage"]; ok {
			t.Errorf("line %d carries a usage key in the default dialect: %v", i, line)
		}
	}
	// No accounting field may leak onto an ordinary event.
	var delta jsonEvent
	for _, ev := range events {
		if ev.Type == "text_delta" {
			delta = ev
			break
		}
	}
	if delta.Turn != 0 || delta.NumTurns != 0 || delta.Subtype != "" || delta.CostKnown {
		t.Errorf("text_delta carries accounting fields: %+v", delta)
	}
}

func TestRunJSONStreamJSONAddsInitAndResult(t *testing.T) {
	llm := &cliMockLLM{name: "stream-model", response: "the answer"}
	ag, sessionID := newTestAgent(t, llm)

	var stdout string
	orig := flagOutputFormat
	flagOutputFormat = "stream-json"
	t.Cleanup(func() { flagOutputFormat = orig })
	stdout = captureStdout(t, func() {
		if err := runJSON(context.Background(), ag, sessionID, "hi", nil, budget.New(10, 0)); err != nil {
			t.Fatalf("runJSON: %v", err)
		}
	})

	events := parseJSONLines(t, stdout)

	init := lastEventOfType(events, "system")
	if init == nil {
		t.Fatalf("no system/init event in stream-json: %+v", typesOf(events))
	}
	if init.Subtype != "init" {
		t.Errorf("init subtype = %q, want \"init\"", init.Subtype)
	}
	if init.SessionID != sessionID {
		t.Errorf("init session_id = %q, want %q", init.SessionID, sessionID)
	}
	if init.Model != "stream-model" {
		t.Errorf("init model = %q, want %q — a harness needs to know what it billed", init.Model, "stream-model")
	}

	result := lastEventOfType(events, "result")
	if result == nil {
		t.Fatalf("no result event in stream-json: %+v", typesOf(events))
	}
	if result.Subtype != "success" {
		t.Errorf("result subtype = %q, want \"success\"", result.Subtype)
	}
	if result.Error != "" {
		t.Errorf("result error = %q, want empty on success", result.Error)
	}
	if result.Result != "the answer" {
		t.Errorf("result result = %q, want the assembled reply %q", result.Result, "the answer")
	}
	if result.NumTurns != 1 {
		t.Errorf("result num_turns = %d, want 1", result.NumTurns)
	}
	if result.SessionID != sessionID {
		t.Errorf("result session_id = %q, want %q", result.SessionID, sessionID)
	}
}

func typesOf(events []jsonEvent) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Type)
	}
	return out
}

func TestJSONRunRecordUsageIgnoresSSEDeltas(t *testing.T) {
	// An SSE stream repeats the round-trip's usage on every chunk and carries
	// the full figure on the last one. Summing all of them would multiply the
	// prompt count by the chunk count — the same inflation the turn counter
	// avoids.
	var buf bytes.Buffer
	run := &jsonRun{enc: json.NewEncoder(&buf), started: time.Now(), dialect: jsonStream}

	full := &session.Event{}
	full.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:        1000,
		CandidatesTokenCount:    50,
		CachedContentTokenCount: 800,
	}
	for range 4 {
		chunk := *full
		chunk.Partial = true
		run.recordUsage(&chunk)
	}
	run.recordUsage(full)

	if got := run.tokens.InputTokens; got != 1000 {
		t.Errorf("input tokens = %d, want 1000 — partials must not be summed", got)
	}
	if got := run.tokens.OutputTokens; got != 50 {
		t.Errorf("output tokens = %d, want 50", got)
	}
	if got := run.tokens.CacheReadTokens; got != 800 {
		t.Errorf("cache read tokens = %d, want 800", got)
	}
}

func TestJSONRunRecordUsageIsInertInTheDefaultDialect(t *testing.T) {
	// The default dialect must not pay for counting it will never report.
	var buf bytes.Buffer
	run := &jsonRun{enc: json.NewEncoder(&buf), started: time.Now(), dialect: jsonPi}

	ev := &session.Event{}
	ev.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 500}
	run.recordUsage(ev)

	if got := run.tokens.InputTokens; got != 0 {
		t.Errorf("input tokens = %d in the default dialect, want 0", got)
	}
}

func TestJSONRunRecordUsageHandlesNilEventsAndMetadata(t *testing.T) {
	var buf bytes.Buffer
	run := &jsonRun{enc: json.NewEncoder(&buf), started: time.Now(), dialect: jsonStream}

	run.recordUsage(nil)
	run.recordUsage(&session.Event{}) // no Content, no UsageMetadata

	if got := run.tokens.InputTokens; got != 0 {
		t.Errorf("input tokens = %d, want 0 — a nil deref here would take down the run", got)
	}
}

func TestRunJSONStreamJSONReportsTurns(t *testing.T) {
	// A turn event per round-trip, numbered from 1, and never repeated.
	llm := &cliToolCallingLLM{
		name:         "turner",
		functionCall: &genai.FunctionCall{Name: "sleep", Args: map[string]any{"ms": 1}},
		finalText:    "done",
	}
	ag, sessionID := newTestAgent(t, llm)

	orig := flagOutputFormat
	flagOutputFormat = "stream-json"
	t.Cleanup(func() { flagOutputFormat = orig })

	stdout := captureStdout(t, func() {
		if err := runJSON(context.Background(), ag, sessionID, "go", nil, budget.New(10, 0)); err != nil {
			t.Fatalf("runJSON: %v", err)
		}
	})

	events := parseJSONLines(t, stdout)
	turns := eventsOfType(events, "turn")
	if len(turns) == 0 {
		t.Fatalf("no turn events: %v", typesOf(events))
	}
	seen := map[int]bool{}
	for i, ev := range turns {
		want := i + 1
		if ev.Turn != want {
			t.Errorf("turn event %d has turn = %d, want %d", i, ev.Turn, want)
		}
		if seen[ev.Turn] {
			t.Errorf("turn %d announced twice — one event per round-trip, no more", ev.Turn)
		}
		seen[ev.Turn] = true
	}

	result := lastEventOfType(events, "result")
	if result == nil {
		t.Fatal("no result event")
	}
	if result.NumTurns != len(turns) {
		t.Errorf("result num_turns = %d, want %d — the totals must agree with the per-turn events",
			result.NumTurns, len(turns))
	}
}

// The whole point of --max-turns: a model that never stops calling tools is cut
// off, and the caller learns it was cut off.
func TestRunJSONMaxTurnsStopsAndReportsIt(t *testing.T) {
	llm := &loopLLM{name: "looper"}
	ag, sessionID := newTestAgent(t, llm)

	orig := flagOutputFormat
	flagOutputFormat = "stream-json"
	t.Cleanup(func() { flagOutputFormat = orig })

	var err error
	stdout := captureStdout(t, func() {
		err = runJSON(context.Background(), ag, sessionID, "loop forever", nil, budget.New(3, 0))
	})

	// A non-nil error is what makes the process exit non-zero. Returning nil
	// here would tell CI the agent finished its work.
	if err == nil {
		t.Fatal("runJSON = nil after hitting --max-turns, want a stop")
	}
	if !errors.Is(err, budget.MaxTurnsErr) {
		t.Errorf("err = %v, want a budget.MaxTurnsErr", err)
	}
	if !budget.IsStop(err) {
		t.Error("budget.IsStop(err) = false, want true — the retry path must recognise it")
	}

	events := parseJSONLines(t, stdout)
	if last := events[len(events)-1]; last.Type != "result" {
		t.Errorf("last event = %q, want result — the stream must end with the verdict", last.Type)
	}
	result := lastEventOfType(events, "result")
	if result == nil {
		t.Fatalf("no result event: %v", typesOf(events))
	}
	if result.Subtype != "error_max_turns" {
		t.Errorf("result subtype = %q, want \"error_max_turns\"", result.Subtype)
	}
	if result.Error == "" {
		t.Error("result error is empty — a consumer that only reads `error` would treat a cut-off run as a success")
	}
	if result.NumTurns != 4 {
		t.Errorf("result num_turns = %d, want 4 (three allowed, the breaching one counted)", result.NumTurns)
	}
	if calls := llm.callCount(); calls > 4 {
		t.Errorf("the model was called %d times, want at most 4", calls)
	}
}

func TestRunJSONMaxBudgetIsNotReportedAsSuccess(t *testing.T) {
	// A run whose budget is already spent must not report "success", whoever
	// spent it. Here the ceiling was crossed by an earlier response — in a live
	// run that is the model wrapper refusing the next request — and runJSON's
	// post-range check is what keeps the two ends consistent.
	llm := &cliMockLLM{name: "priced-model", response: "one short reply"}
	ag, sessionID := newTestAgent(t, llm)

	orig := flagOutputFormat
	flagOutputFormat = "stream-json"
	t.Cleanup(func() { flagOutputFormat = orig })

	limits := budget.New(0, 1.00)
	// AddCost returns the stop once the ceiling is crossed; that is the state
	// being set up here, not a failure.
	_ = limits.AddCost(2.00, true)

	var err error
	stdout := captureStdout(t, func() {
		err = runJSON(context.Background(), ag, sessionID, "hi", nil, limits)
	})

	if !errors.Is(err, budget.BudgetExceededErr) {
		t.Fatalf("runJSON = %v, want a budget.BudgetExceededErr", err)
	}

	events := parseJSONLines(t, stdout)
	result := lastEventOfType(events, "result")
	if result == nil {
		t.Fatalf("no result event: %v", typesOf(events))
	}
	if result.Subtype != "error_max_budget" {
		t.Errorf("result subtype = %q, want \"error_max_budget\"", result.Subtype)
	}
	if result.Error == "" {
		t.Error("result error is empty — a cut-off run must not read as a success")
	}
	if !result.CostKnown {
		t.Error("cost_known = false, want true — the spend was priced")
	}
	if result.TotalCostUSD < 1.99 {
		t.Errorf("total_cost_usd = %v, want at least the $2.00 charged", result.TotalCostUSD)
	}
}

func TestRunJSONUnpricedRunSaysCostIsUnknown(t *testing.T) {
	// A model with no published rate must report cost_known false rather than
	// a $0.00 total, which would read as "this run was free".
	llm := &cliMockLLM{name: "definitely-not-a-real-model", response: "hi"}
	ag, sessionID := newTestAgent(t, llm)

	orig := flagOutputFormat
	flagOutputFormat = "stream-json"
	t.Cleanup(func() { flagOutputFormat = orig })

	stdout := captureStdout(t, func() {
		if err := runJSON(context.Background(), ag, sessionID, "hi", nil, budget.New(10, 0)); err != nil {
			t.Fatalf("runJSON: %v", err)
		}
	})

	result := lastEventOfType(parseJSONLines(t, stdout), "result")
	if result == nil {
		t.Fatal("no result event")
	}
	if result.CostKnown {
		t.Error("cost_known = true for a model with no pricing entry, want false")
	}
}

func TestRunJSONProviderFailureReportsErrorSubtype(t *testing.T) {
	llm := &cliErrorLLM{name: "broken", err: errors.New("upstream 503")}
	ag, sessionID := newTestAgent(t, llm)

	orig := flagOutputFormat
	flagOutputFormat = "stream-json"
	t.Cleanup(func() { flagOutputFormat = orig })

	var err error
	stdout := captureStdout(t, func() {
		err = runJSON(context.Background(), ag, sessionID, "hi", nil, budget.New(10, 0))
	})
	if err == nil {
		t.Fatal("runJSON = nil on a provider failure, want an error")
	}

	result := lastEventOfType(parseJSONLines(t, stdout), "result")
	if result == nil {
		t.Fatal("no result event on the failure path")
	}
	if result.Subtype != "error_during_execution" {
		t.Errorf("result subtype = %q, want \"error_during_execution\"", result.Subtype)
	}
}

func TestRunJSONUnknownOutputFormatIsRejectedBeforeAnyRun(t *testing.T) {
	llm := &cliMockLLM{name: "m", response: "should not run"}
	ag, sessionID := newTestAgent(t, llm)

	orig := flagOutputFormat
	flagOutputFormat = "nonsense"
	t.Cleanup(func() { flagOutputFormat = orig })

	stdout := captureStdout(t, func() {
		err := runJSON(context.Background(), ag, sessionID, "hi", nil, nil)
		if err == nil {
			t.Fatal("runJSON = nil for an unknown --output-format, want an error")
		}
	})
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("stdout = %q, want nothing — the dialect is validated before the agent runs", stdout)
	}
}

func TestBudgetSubtype(t *testing.T) {
	turns := budget.New(1, 0)
	_ = turns.CountTurn()
	_ = turns.CountTurn()
	if got := budgetSubtype(turns.Exceeded()); got != "error_max_turns" {
		t.Errorf("budgetSubtype(turn stop) = %q, want \"error_max_turns\"", got)
	}

	dollars := budget.New(0, 1.00)
	_ = dollars.AddCost(2.00, true)
	if got := budgetSubtype(dollars.Exceeded()); got != "error_max_budget" {
		t.Errorf("budgetSubtype(cost stop) = %q, want \"error_max_budget\"", got)
	}
}

func TestHeadlessLimits(t *testing.T) {
	origTurns, origUSD := flagMaxTurns, flagMaxBudgetUSD
	t.Cleanup(func() { flagMaxTurns, flagMaxBudgetUSD = origTurns, origUSD })

	flagMaxTurns, flagMaxBudgetUSD = 0, 0
	if got := headlessLimits(); got != nil {
		t.Errorf("headlessLimits() = %v with no flags, want nil — an unlimited run pays no counting cost", got)
	}

	flagMaxTurns, flagMaxBudgetUSD = 5, 0
	limits := headlessLimits()
	if limits == nil || limits.Unlimited() {
		t.Fatal("headlessLimits() with --max-turns = nil/unlimited, want a real ceiling")
	}

	flagMaxTurns, flagMaxBudgetUSD = 0, 1.50
	limits = headlessLimits()
	if limits == nil || limits.Unlimited() {
		t.Fatal("headlessLimits() with --max-budget-usd = nil/unlimited, want a real ceiling")
	}
}

func TestIsInteractiveMode(t *testing.T) {
	// The cost wrapper is installed only for headless runs: in the TUI a
	// runaway would be cut off mid-render, and no --max-turns was asked for.
	for _, tc := range []struct {
		mode string
		want bool
	}{
		{"interactive", true},
		{"socket", true},
		{"rpc", true},
		{"print", false},
		{"json", false},
		{"", false},
		{"nonsense", false},
	} {
		if got := isInteractiveMode(tc.mode); got != tc.want {
			t.Errorf("isInteractiveMode(%q) = %v, want %v", tc.mode, got, tc.want)
		}
	}
}

func TestRunPrintReportsABudgetStop(t *testing.T) {
	// runPrint has no stream to annotate, so the stop goes to stderr and the
	// non-nil error is what the exit code comes from.
	llm := &loopLLM{name: "looper"}
	ag, sessionID := newTestAgent(t, llm)

	var err error
	captureStdout(t, func() {
		stderr := captureStderr(t, func() {
			err = runPrint(context.Background(), ag, sessionID, "loop forever", nil, budget.New(2, 0))
		})
		if !strings.Contains(stderr, "turn limit") {
			t.Errorf("stderr = %q, want it to explain the stop", stderr)
		}
	})

	if err == nil {
		t.Fatal("runPrint = nil after hitting the turn limit, want a stop")
	}
	if !errors.Is(err, budget.MaxTurnsErr) {
		t.Errorf("err = %v, want a budget.MaxTurnsErr", err)
	}
}

func TestRunPrintWithNoLimitsReturnsNil(t *testing.T) {
	llm := &cliMockLLM{name: "m", response: "all done"}
	ag, sessionID := newTestAgent(t, llm)

	var err error
	captureStdout(t, func() {
		_ = captureStderr(t, func() {
			err = runPrint(context.Background(), ag, sessionID, "hi", nil, nil)
		})
	})
	if err != nil {
		t.Errorf("runPrint = %v with no ceiling, want nil", err)
	}
}
