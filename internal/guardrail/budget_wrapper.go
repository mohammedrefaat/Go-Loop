package guardrail

import (
	"context"
	"iter"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/budget"
	"github.com/dimetron/pi-go/internal/provider"
)

// budgetErrorCode names a refusal to spend. It is deliberately distinct from
// DAILY_LIMIT_EXCEEDED, which means the daily token ceiling: a consumer
// branching on the code can tell "this invocation was capped" from "your
// account's quota is gone", and the two call for different responses.
const budgetErrorCode = "BUDGET_EXCEEDED"

// WrapBudgetedModel wraps an LLM so a run stops when its cost ceiling is
// reached. limits may be nil, in which case the model is returned unwrapped.
//
// The check is a pre-flight on the request, not an observation of the response:
// the cheapest moment to refuse a call is before it is sent, and a budget that
// stopped afterwards would already have paid for the call it is complaining
// about.
//
// The refusal arrives as an ErrorCode on the response rather than as a Go
// error. That is how a provider failure already reaches pi-go — providers do
// the same — and it is what internal/agent.EventError and provider.retry's
// streamFailure exist to convert back into an error at the edges.
func WrapBudgetedModel(llm model.LLM, limits *budget.Limits, providerName, modelName string) model.LLM {
	if limits.Unlimited() {
		return llm
	}
	pricing, priced := provider.CostForModel(providerName, modelName)
	return &budgetedModel{
		inner:   llm,
		limits:  limits,
		pricing: pricing,
		priced:  priced,
	}
}

type budgetedModel struct {
	inner   model.LLM
	limits  *budget.Limits
	pricing provider.PricingModel
	// priced records whether the pricing snapshot had an entry for this model.
	// False does not mean the model is free; it means the budget cannot be
	// evaluated against it, and AddCost is told so rather than handed a zero.
	priced bool
}

func (b *budgetedModel) Name() string { return b.inner.Name() }

func (b *budgetedModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if stop := b.limits.Exceeded(); stop != nil {
			yield(budgetResponse(stop), nil)
			return
		}
		for resp, err := range b.inner.GenerateContent(ctx, req, stream) {
			if err != nil || resp == nil || resp.UsageMetadata == nil {
				yield(resp, err)
				if !yieldOK(resp, err) {
					return
				}
				continue
			}
			// Charge before yielding. The response is already paid for, so the
			// cost belongs to this run whether or not the consumer is still
			// listening; charging after the yield would lose it on an early
			// break.
			_ = b.limits.AddCost(b.cost(resp.UsageMetadata), b.priced)
			if !yield(resp, nil) {
				return
			}
		}
	}
}

// cost prices one response's usage, and reports whether it could be priced.
func (b *budgetedModel) cost(u *genai.GenerateContentResponseUsageMetadata) float64 {
	if !b.priced {
		return 0
	}
	return provider.EstimateCostUSD(b.pricing, provider.TokenUsage{
		Input:  int64(u.PromptTokenCount),
		Output: int64(u.CandidatesTokenCount),
		Cached: int64(u.CachedContentTokenCount),
	})
}

// budgetResponse renders a stop as the error-carrying response shape pi-go
// already has a path for.
func budgetResponse(stop error) *model.LLMResponse {
	return &model.LLMResponse{
		ErrorCode:    budgetErrorCode,
		ErrorMessage: stop.Error(),
	}
}

// yieldOK reports whether the consumer is still listening. An error already
// ends a stream for every caller, and yielding past one would let a
// continuation be mistaken for more output.
func yieldOK(resp *model.LLMResponse, err error) bool {
	return err == nil && (resp == nil || resp.ErrorCode == "")
}
