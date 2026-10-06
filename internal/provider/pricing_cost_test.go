package provider

import (
	"math"
	"testing"
)

// closeTo compares two USD figures to the cent. A budget that is off by a
// fraction of a microdollar per token still compounds over a long run, so the
// tests pin the arithmetic rather than just its sign.
func closeTo(t *testing.T, got, want, tolerance float64, what string) {
	t.Helper()
	if math.Abs(got-want) > tolerance {
		t.Errorf("%s = %v, want %v (±%v)", what, got, want, tolerance)
	}
}

// basicModel is a flat-rate model with a discounted cache read. The three-way
// split (fresh input, cache read, output) is the part that has to be right, and
// a model with no tiers keeps the base-rate cases independent of tier logic.
var basicModel = PricingModel{
	Input:      3.00,  // $3 / 1M
	Output:     15.00, // $15 / 1M
	CacheRead:  0.30,  // $0.30 / 1M — 0.1x
	CacheWrite: 3.75,
}

// tieredModel adds the long-context tier on top of basicModel's rates, the
// shape Anthropic's long-context models publish.
var tieredModel = PricingModel{
	Input:      3.00,
	Output:     15.00,
	CacheRead:  0.30,
	CacheWrite: 3.75,
	Tiers: []PricingTier{
		{ContextOver: 200_000, Input: 6.00, Output: 22.50, CacheRead: 0.60},
	},
}

func TestEstimateCostUSDBasicRates(t *testing.T) {
	tests := []struct {
		name string
		pm   PricingModel
		u    TokenUsage
		want float64
	}{
		{
			name: "no usage costs nothing",
			pm:   basicModel,
			u:    TokenUsage{},
			want: 0,
		},
		{
			name: "1M input at the input rate",
			pm:   basicModel,
			u:    TokenUsage{Input: 1_000_000},
			want: 3.00,
		},
		{
			name: "1M output at the output rate",
			pm:   basicModel,
			u:    TokenUsage{Output: 1_000_000},
			want: 15.00,
		},
		{
			name: "input and output bill separately",
			pm:   basicModel,
			u:    TokenUsage{Input: 1_000_000, Output: 1_000_000},
			want: 18.00,
		},
		{
			name: "cached reads bill at the cache rate, not the input rate",
			// 800k fresh at $3/M = $2.40, plus 200k cached at $0.30/M = $0.06.
			pm:   basicModel,
			u:    TokenUsage{Input: 1_000_000, Cached: 200_000},
			want: 2.46,
		},
		{
			name: "a fully cached prompt costs a tenth of a fresh one",
			pm:   basicModel,
			u:    TokenUsage{Input: 1_000_000, Cached: 1_000_000},
			want: 0.30,
		},
		{
			name: "a fractional rate scales per token",
			pm:   basicModel,
			u:    TokenUsage{Input: 500},
			want: 500.0 / 1e6 * 3.00,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			closeTo(t, EstimateCostUSD(tc.pm, tc.u), tc.want, 1e-9, "EstimateCostUSD")
		})
	}
}

func TestEstimateCostUSDClampsHostileUsage(t *testing.T) {
	// A provider that reported a negative count must not produce a negative
	// bill, which would move the budget in the wrong direction and let a run
	// continue that should have stopped.
	pm := basicModel
	if got := EstimateCostUSD(pm, TokenUsage{Input: -1000, Output: -5, Cached: -1}); got != 0 {
		t.Errorf("EstimateCostUSD(negative usage) = %v, want 0", got)
	}
	// Cached above Input would otherwise subtract the fresh cost below zero.
	got := EstimateCostUSD(pm, TokenUsage{Input: 1000, Cached: 5000})
	closeTo(t, got, 1000.0/1e6*0.30, 1e-12, "EstimateCostUSD with Cached > Input")
}

func TestEstimateCostUSDPicksTheTierThePromptFallsInto(t *testing.T) {
	// Tiers replace the base rates rather than adding to them, so a prompt
	// over the threshold bills entirely at the higher rate.
	tests := []struct {
		name  string
		input int64
		want  float64
	}{
		{name: "under the threshold uses base rates", input: 199_999, want: 199_999.0 / 1e6 * 3.00},
		{name: "exactly at the threshold uses the tier", input: 200_000, want: 200_000.0 / 1e6 * 6.00},
		{name: "over the threshold uses the tier", input: 500_000, want: 500_000.0 / 1e6 * 6.00},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			closeTo(t, EstimateCostUSD(tieredModel, TokenUsage{Input: tc.input}), tc.want, 1e-9, "EstimateCostUSD")
		})
	}
}

func TestRateForPicksTheDeepestMatchingTier(t *testing.T) {
	// models.dev publishes several tiers; the answer is the deepest one the
	// prompt has crossed, not the first match and not a sum.
	pm := PricingModel{
		Input:  1.00,
		Output: 2.00,
		Tiers: []PricingTier{
			{ContextOver: 100_000, Input: 2.00, Output: 4.00},
			{ContextOver: 200_000, Input: 4.00, Output: 8.00},
			{ContextOver: 400_000, Input: 8.00, Output: 16.00},
		},
	}
	tests := []struct {
		prompt  int64
		wantIn  float64
		wantOut float64
	}{
		{prompt: 0, wantIn: 1.00, wantOut: 2.00},
		{prompt: 99_999, wantIn: 1.00, wantOut: 2.00},
		{prompt: 100_000, wantIn: 2.00, wantOut: 4.00},
		{prompt: 199_999, wantIn: 2.00, wantOut: 4.00},
		{prompt: 200_000, wantIn: 4.00, wantOut: 8.00},
		{prompt: 400_000, wantIn: 8.00, wantOut: 16.00},
		{prompt: 10_000_000, wantIn: 8.00, wantOut: 16.00},
	}
	for _, tc := range tests {
		in, out, _ := pm.rateFor(tc.prompt)
		if in != tc.wantIn || out != tc.wantOut {
			t.Errorf("rateFor(%d) = %v/%v, want %v/%v", tc.prompt, in, out, tc.wantIn, tc.wantOut)
		}
	}
}

func TestRateForIgnoresTierOrderInTheSnapshot(t *testing.T) {
	// The tiers come out of a JSON map, so their order is whatever the decoder
	// produced. Selection must not depend on it.
	pm := PricingModel{
		Input: 1.00,
		Tiers: []PricingTier{
			{ContextOver: 400_000, Input: 8.00},
			{ContextOver: 100_000, Input: 2.00},
			{ContextOver: 200_000, Input: 4.00},
		},
	}
	in, _, _ := pm.rateFor(300_000)
	if in != 4.00 {
		t.Errorf("rateFor(300000) = %v, want 4.00 regardless of tier order", in)
	}
}

func TestEstimateCostUSDCarriesTheTierRateIntoTheCacheSplit(t *testing.T) {
	// A cached read over the threshold bills at the tier's cache rate. Getting
	// this wrong would under- or over-state the dominant cost of a long run.
	u := TokenUsage{Input: 300_000, Cached: 200_000, Output: 100_000}
	// 100k fresh @ $6/M = $0.60; 200k cached @ $0.60/M = $0.12; 100k out @ $22.50/M = $2.25
	closeTo(t, EstimateCostUSD(tieredModel, u), 2.97, 1e-9, "EstimateCostUSD")
}

func TestTokenUsageFresh(t *testing.T) {
	tests := []struct {
		name string
		u    TokenUsage
		want int64
	}{
		{name: "nothing cached", u: TokenUsage{Input: 100}, want: 100},
		{name: "all cached", u: TokenUsage{Input: 100, Cached: 100}, want: 0},
		{name: "partly cached", u: TokenUsage{Input: 100, Cached: 30}, want: 70},
		{name: "over-reported cache clamps to zero", u: TokenUsage{Input: 100, Cached: 250}, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.u.Fresh(); got != tc.want {
				t.Errorf("Fresh() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCostForModelCoversOllamaCloud(t *testing.T) {
	// The gap that made this a new function: models.dev publishes no ollama
	// entry, so a cloud-tagged model has to come from the vendored snapshot.
	// CostFor alone would miss it and the run would report an unknown cost.
	pm, ok := CostForModel("ollama", "gemma4:31b-cloud")
	if !ok {
		t.Fatal("CostForModel(ollama, gemma4:31b-cloud) = not found, want a price")
	}
	if pm.hasPrice() != true {
		t.Error("the Ollama Cloud entry carries no rate")
	}
	if _, ok := CostFor("ollama", "gemma4:31b-cloud"); ok {
		t.Log("CostFor also resolves it — the models.dev snapshot grew an ollama section; " +
			"CostForModel is still the correct entry point")
	}
}

func TestCostForModelFallsThroughForUnknownModels(t *testing.T) {
	if _, ok := CostForModel("openai", "definitely-not-a-model"); ok {
		t.Error("CostForModel(openai, unknown) = found, want not found — an unknown model " +
			"must be reported unpriced rather than priced at zero")
	}
	if _, ok := CostForModel("ollama", "some-local-model"); ok {
		t.Error("CostForModel(ollama, local) = found, want not found — a locally-installed " +
			"model has no published price")
	}
}

func TestCostForModelIsIdempotentWithCostFor(t *testing.T) {
	// Wherever CostFor would resolve, CostForModel must resolve the same
	// thing. Only the Ollama Cloud branch may differ.
	for _, tc := range []struct{ provider, model string }{
		{"anthropic", "claude-sonnet-4-5"},
		{"openai", "gpt-5.2"},
		{"gemini", "gemini-3.5-pro"},
		{"ollama", "llama3"},
	} {
		want, wantOK := CostFor(tc.provider, tc.model)
		got, gotOK := CostForModel(tc.provider, tc.model)
		// Compared field by field: PricingModel carries a slice, so it is not
		// comparable with ==.
		if gotOK != wantOK || got.Input != want.Input || got.Output != want.Output ||
			got.CacheRead != want.CacheRead || len(got.Tiers) != len(want.Tiers) {
			t.Errorf("CostForModel(%s, %s) = %+v/%v, want %+v/%v",
				tc.provider, tc.model, got, gotOK, want, wantOK)
		}
	}
}
