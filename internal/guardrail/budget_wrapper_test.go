package guardrail

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/budget"
)

// The tests below pin the two behaviours a CI harness depends on: the wrapper
// refuses the request *before* spending, and the refusal arrives in a shape the
// existing error paths already understand.

// countingLLM emits n responses, each with fixed usage, and records how many
// times it was actually called.
type countingLLM struct {
	name    string
	calls   int
	perCall struct {
		input  int32
		output int32
		cached int32
	}
	// emitErr, when set, is yielded after the responses instead of them.
	emitErr error
}

func (m *countingLLM) Name() string { return m.name }

func (m *countingLLM) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	m.calls++
	return func(yield func(*model.LLMResponse, error) bool) {
		if m.emitErr != nil {
			yield(nil, m.emitErr)
			return
		}
		resp := &model.LLMResponse{
			Content:      &genai.Content{Role: "model", Parts: []*genai.Part{{Text: "ok"}}},
			TurnComplete: true,
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount:        m.perCall.input,
				CandidatesTokenCount:    m.perCall.output,
				CachedContentTokenCount: m.perCall.cached,
			},
		}
		if !yield(resp, nil) {
			return
		}
		// A second chunk with no usage, the way an SSE stream closes: the
		// wrapper must not charge it twice.
		yield(&model.LLMResponse{
			Content:      &genai.Content{Role: "model", Parts: []*genai.Part{{Text: "!"}}},
			TurnComplete: true,
		}, nil)
	}
}

// drainLLM ranges a wrapped model's stream and returns the responses and errors
// it produced.
func drainLLM(t *testing.T, llm model.LLM) (responses []*model.LLMResponse, errs []error) {
	t.Helper()
	for resp, err := range llm.GenerateContent(context.Background(), nil, false) {
		if resp != nil {
			responses = append(responses, resp)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return responses, errs
}

func TestWrapBudgetedModelUnlimitedReturnsTheInnerModel(t *testing.T) {
	// No ceilings means no wrapper: not one extra allocation, and the identity
	// of the model is preserved so nothing downstream can tell it changed.
	inner := &countingLLM{name: "m"}
	for _, limits := range []*budget.Limits{nil, budget.New(0, 0)} {
		if got := WrapBudgetedModel(inner, limits, "anthropic", "claude-sonnet-4-5"); got != model.LLM(inner) {
			t.Errorf("WrapBudgetedModel(%v) = %T, want the inner model unwrapped", limits, got)
		}
	}
}

func TestWrapBudgetedModelChargesEachResponse(t *testing.T) {
	inner := &countingLLM{name: "m"}
	// 1M input at $3/M = $3.00, plus 1000 output at $15/M = $0.015.
	inner.perCall.input = 1_000_000
	inner.perCall.output = 1_000

	limits := budget.New(0, 100.0)
	wrapped := WrapBudgetedModel(inner, limits, "anthropic", "claude-sonnet-4-5")
	if wrapped.Name() != "m" {
		t.Errorf("Name() = %q, want %q — the wrapper must not rename the model", wrapped.Name(), "m")
	}

	responses, errs := drainLLM(t, wrapped)
	if len(errs) != 0 {
		t.Fatalf("errs = %v, want none", errs)
	}
	if len(responses) != 2 {
		t.Fatalf("got %d responses, want 2 — the wrapper must pass the stream through", len(responses))
	}

	spent, priced := limits.SpentUSD()
	if !priced {
		t.Error("priced = false, want true — anthropic/claude-sonnet-4-5 has a published rate")
	}
	if spent < 3.014 || spent > 3.016 {
		t.Errorf("spent = %v, want $3.015 (one charged response; the usage-free chunk is free)", spent)
	}
}

func TestWrapBudgetedModelSplitsCachedFromFreshInput(t *testing.T) {
	// The charged figure is the whole point of the wrapper: 1M prompt of which
	// 900k came from cache bills as 100k fresh + 900k at the cache rate, not
	// 1M at the input rate. That difference is ~10x.
	inner := &countingLLM{name: "m"}
	inner.perCall.input = 1_000_000
	inner.perCall.cached = 900_000

	limits := budget.New(0, 100.0)
	wrapped := WrapBudgetedModel(inner, limits, "anthropic", "claude-sonnet-4-5")
	drainLLM(t, wrapped)

	spent, _ := limits.SpentUSD()
	// 100k fresh @ $3/M = $0.30; 900k cached @ $0.30/M = $0.27.
	if spent < 0.569 || spent > 0.571 {
		t.Errorf("spent = %v, want $0.57 — cached reads must bill at the cache rate", spent)
	}
}

func TestWrapBudgetedModelRefusesOnceTheCeilingIsHit(t *testing.T) {
	// The next call after the ceiling is reached must not reach the provider:
	// the whole purpose of the wrapper is bounding what is spent.
	inner := &countingLLM{name: "m"}
	inner.perCall.input = 1_000_000 // $3.00 per call
	inner.perCall.output = 0

	limits := budget.New(0, 5.00) // one call fits ($3), the second crosses ($6)
	wrapped := WrapBudgetedModel(inner, limits, "anthropic", "claude-sonnet-4-5")

	// Call 1: clean pass-through, $3 charged.
	responses, errs := drainLLM(t, wrapped)
	if len(errs) != 0 {
		t.Fatalf("call 1 errs = %v, want none — the refusal arrives on the response", errs)
	}
	if len(responses) != 2 || responses[0].ErrorCode != "" {
		t.Fatalf("call 1 returned %d responses (first ErrorCode %q), want a clean pass-through",
			len(responses), responses[0].ErrorCode)
	}

	// Call 2: the pre-flight sees $3 spent against a $5 ceiling, so the request
	// goes out — and lands at $6. It was already allowed when it started.
	responses, errs = drainLLM(t, wrapped)
	if len(errs) != 0 {
		t.Fatalf("call 2 errs = %v, want none", errs)
	}
	if len(responses) != 2 || responses[0].ErrorCode != "" {
		t.Errorf("call 2 returned %d responses (first ErrorCode %q), want a clean pass-through",
			len(responses), responses[0].ErrorCode)
	}
	if stop := limits.Exceeded(); stop == nil {
		t.Fatal("Exceeded() = nil after two calls at $6 against a $5 ceiling, want a stop")
	}

	// Call 3: refused before the provider is touched.
	responses, errs = drainLLM(t, wrapped)
	if len(errs) != 0 {
		t.Fatalf("call 3 errs = %v, want none", errs)
	}
	if len(responses) != 1 {
		t.Fatalf("call 3 returned %d responses, want exactly the refusal", len(responses))
	}
	got := responses[0]
	if got.ErrorCode != budgetErrorCode {
		t.Errorf("ErrorCode = %q, want %q", got.ErrorCode, budgetErrorCode)
	}
	if got.ErrorMessage == "" {
		t.Error("ErrorMessage is empty — the refusal must explain itself")
	}
	if !strings.Contains(got.ErrorMessage, "cost limit") {
		t.Errorf("ErrorMessage = %q, want it to name the cost limit", got.ErrorMessage)
	}
	if inner.calls != 2 {
		t.Errorf("the provider was called %d times, want 2 — the refusal must stop the *request*", inner.calls)
	}
}

func TestWrapBudgetedModelRefusalIsDistinctFromTheDailyLimit(t *testing.T) {
	// A consumer branching on the code has to be able to tell "this invocation
	// was capped" from "your account's quota is gone"; they call for different
	// responses.
	limits := budget.New(0, 1.00)
	_ = limits.AddCost(1.00, true)
	inner := &countingLLM{name: "m"}
	wrapped := WrapBudgetedModel(inner, limits, "anthropic", "claude-sonnet-4-5")

	responses, _ := drainLLM(t, wrapped)
	if len(responses) != 1 {
		t.Fatalf("got %d responses, want 1", len(responses))
	}
	if responses[0].ErrorCode == "DAILY_LIMIT_EXCEEDED" {
		t.Error("ErrorCode = DAILY_LIMIT_EXCEEDED, want BUDGET_EXCEEDED — the two mean different things")
	}
}

func TestWrapBudgetedModelChargesBeforeTheYield(t *testing.T) {
	// A consumer that breaks out of the stream has still paid for the response
	// it saw. Charging after the yield would lose that cost and let the run
	// continue on a budget it has already spent.
	inner := &countingLLM{name: "m"}
	inner.perCall.input = 1_000_000

	limits := budget.New(0, 100.0)
	wrapped := WrapBudgetedModel(inner, limits, "anthropic", "claude-sonnet-4-5")

	for range wrapped.GenerateContent(context.Background(), nil, false) {
		break // take only the first chunk and walk away
	}

	spent, priced := limits.SpentUSD()
	if !priced || spent < 2.99 || spent > 3.01 {
		t.Errorf("spent = %v/%v, want $3.00/true — the response was paid for", spent, priced)
	}
}

func TestWrapBudgetedModelPassesErrorsThrough(t *testing.T) {
	boom := errors.New("upstream 503")
	inner := &countingLLM{name: "m", emitErr: boom}

	limits := budget.New(0, 100.0)
	wrapped := WrapBudgetedModel(inner, limits, "anthropic", "claude-sonnet-4-5")

	_, errs := drainLLM(t, wrapped)
	if len(errs) != 1 || !errors.Is(errs[0], boom) {
		t.Errorf("errs = %v, want [%v] — a provider failure must reach the caller intact", errs, boom)
	}
	// A failed call was not billed.
	if _, priced := limits.SpentUSD(); priced {
		t.Error("priced = true after a failed call, want false — a failure has no usage to charge")
	}
}

func TestWrapBudgetedModelUnknownModelReportsUnpricedRatherThanFree(t *testing.T) {
	// A model absent from the pricing snapshot must not be treated as $0.00:
	// the ceiling is then unevaluable and the run continues on turns alone,
	// rather than stopping instantly or pretending the budget held.
	inner := &countingLLM{name: "m"}
	inner.perCall.input = 1_000_000

	limits := budget.New(0, 0.01) // a penny: any priced model would blow this
	wrapped := WrapBudgetedModel(inner, limits, "openai", "definitely-not-a-real-model")

	responses, errs := drainLLM(t, wrapped)
	if len(errs) != 0 {
		t.Fatalf("errs = %v, want none", errs)
	}
	for _, resp := range responses {
		if resp.ErrorCode != "" {
			t.Fatalf("ErrorCode = %q, want none — an unpriced model cannot trip a dollar ceiling", resp.ErrorCode)
		}
	}
	spent, priced := limits.SpentUSD()
	if priced {
		t.Errorf("priced = true for an unknown model, want false")
	}
	if spent != 0 {
		t.Errorf("spent = %v, want 0 with priced false", spent)
	}
	if stop := limits.Exceeded(); stop != nil {
		t.Errorf("Exceeded() = %v, want nil — an unevaluable budget does not stop a run", stop)
	}
}

func TestWrapBudgetedModelRefusesWhenTheBudgetAlreadySpent(t *testing.T) {
	// A shared Limits can arrive already over — for instance when the event
	// loop's turn accounting is not what stopped the run.
	limits := budget.New(0, 1.00)
	_ = limits.AddCost(2.00, true)

	inner := &countingLLM{name: "m"}
	wrapped := WrapBudgetedModel(inner, limits, "anthropic", "claude-sonnet-4-5")

	responses, _ := drainLLM(t, wrapped)
	if len(responses) != 1 || responses[0].ErrorCode != budgetErrorCode {
		t.Fatalf("responses = %+v, want a single BUDGET_EXCEEDED refusal", responses)
	}
	if inner.calls != 0 {
		t.Errorf("the provider was called %d times, want 0", inner.calls)
	}
}
