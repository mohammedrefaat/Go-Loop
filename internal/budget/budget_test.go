package budget

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

// The tests below pin the three decisions a headless caller depends on:
// a nil *Limits enforces nothing, an unpriced model is recorded as unpriced
// rather than as free, and a stop is distinguishable from a context
// cancellation by errors.Is alone.

func TestNilLimitsEnforcesNothing(t *testing.T) {
	var l *Limits

	if !l.Unlimited() {
		t.Error("Unlimited() = false for a nil Limits, want true")
	}
	if err := l.CountTurn(); err != nil {
		t.Errorf("CountTurn() = %v, want nil for a nil Limits", err)
	}
	if err := l.AddCost(1000, true); err != nil {
		t.Errorf("AddCost() = %v, want nil for a nil Limits", err)
	}
	if err := l.Exceeded(); err != nil {
		t.Errorf("Exceeded() = %v, want nil for a nil Limits", err)
	}
	if got := l.Turns(); got != 0 {
		t.Errorf("Turns() = %d, want 0", got)
	}
	if spent, priced := l.SpentUSD(); spent != 0 || priced {
		t.Errorf("SpentUSD() = %v/%v, want 0/false", spent, priced)
	}
	if got := l.Summary(); got != "" {
		t.Errorf("Summary() = %q, want empty", got)
	}
}

func TestZeroLimitsAreUnlimited(t *testing.T) {
	// The flags' zero values must pass straight through without special-casing.
	l := New(0, 0)
	if !l.Unlimited() {
		t.Error("New(0, 0).Unlimited() = false, want true")
	}
	for range 100 {
		if err := l.CountTurn(); err != nil {
			t.Fatalf("CountTurn() = %v, want nil under an unlimited ceiling", err)
		}
		_ = l.AddCost(1.00, true)
	}
	if err := l.Exceeded(); err != nil {
		t.Errorf("Exceeded() = %v, want nil under an unlimited ceiling", err)
	}
}

func TestCountTurnStopsPastTheCeiling(t *testing.T) {
	l := New(3, 0)

	for i := 1; i <= 3; i++ {
		if err := l.CountTurn(); err != nil {
			t.Fatalf("turn %d: CountTurn() = %v, want nil — the ceiling is 3", i, err)
		}
	}
	err := l.CountTurn()
	if err == nil {
		t.Fatal("turn 4: CountTurn() = nil, want a stop")
	}
	if !errors.Is(err, MaxTurnsErr) {
		t.Errorf("errors.Is(err, MaxTurnsErr) = false, want true (err = %v)", err)
	}
	if errors.Is(err, BudgetExceededErr) {
		t.Error("errors.Is(err, BudgetExceededErr) = true, want false")
	}
	var s *Stopped
	if !errors.As(err, &s) {
		t.Fatalf("errors.As(*Stopped) = false, want true")
	}
	if s.Turns != 4 {
		t.Errorf("Stopped.Turns = %d, want 4", s.Turns)
	}
	if s.Limit != 3 {
		t.Errorf("Stopped.Limit = %v, want 3", s.Limit)
	}
	if !strings.Contains(s.Error(), "4 turns") || !strings.Contains(s.Error(), "limit is 3") {
		t.Errorf("Error() = %q, want it to name the turns taken and the limit", s.Error())
	}
}

func TestExceededIsCheckedBeforeTheNextRequest(t *testing.T) {
	// The ceiling trips the moment a turn passes it, and stays tripped, so a
	// caller polling Exceeded() before each request sees the refusal rather
	// than learning about it only from the event loop.
	l := New(2, 0)
	_ = l.CountTurn()
	_ = l.CountTurn()
	if err := l.Exceeded(); err != nil {
		t.Fatalf("Exceeded() = %v at exactly the limit, want nil — 2 turns is allowed", err)
	}
	_ = l.CountTurn()
	if err := l.Exceeded(); err == nil {
		t.Fatal("Exceeded() = nil after passing the limit, want a stop")
	}
}

func TestAddCostStopsAtTheDollarCeiling(t *testing.T) {
	l := New(0, 1.00)

	if err := l.AddCost(0.40, true); err != nil {
		t.Fatalf("AddCost(0.40) = %v, want nil below the ceiling", err)
	}
	err := l.AddCost(0.60, true)
	if err == nil {
		t.Fatal("AddCost(0.60) = nil, want a stop — the total reaches $1.00")
	}
	if !errors.Is(err, BudgetExceededErr) {
		t.Errorf("errors.Is(err, BudgetExceededErr) = false, want true (err = %v)", err)
	}
	var s *Stopped
	if !errors.As(err, &s) {
		t.Fatalf("errors.As(*Stopped) = false, want true")
	}
	if s.Limit != 1.00 {
		t.Errorf("Stopped.Limit = %v, want 1.00", s.Limit)
	}
	if got := s.Spent; got < 0.999 || got > 1.001 {
		t.Errorf("Stopped.Spent = %v, want 1.00", got)
	}
	if !s.Priced {
		t.Error("Stopped.Priced = false, want true — the cost was known")
	}
}

func TestUnpricedResponsesDoNotTripTheDollarCeiling(t *testing.T) {
	// The regression this guards: a model absent from the pricing snapshot
	// reports zero usage metadata. Charging that as $0 would stop the run's
	// turn counting early or, worse, let it run to the ceiling while the log
	// claimed the budget held.
	l := New(0, 1.00)

	for range 5 {
		if err := l.AddCost(0, false); err != nil {
			t.Fatalf("AddCost(0, unpriced) = %v, want nil — an unknown price is not a breach", err)
		}
	}
	if err := l.Exceeded(); err != nil {
		t.Errorf("Exceeded() = %v, want nil — nothing could be priced", err)
	}
	if spent, priced := l.SpentUSD(); spent != 0 || priced {
		t.Errorf("SpentUSD() = %v/%v, want 0/false", spent, priced)
	}
}

func TestPricedRunAfterUnpricedOneStillEnforces(t *testing.T) {
	l := New(0, 0.50)
	_ = l.AddCost(0, false) // a local model with no pricing entry
	if err := l.AddCost(0.50, true); err == nil {
		t.Error("AddCost(0.50) = nil, want a stop — the priced response crossed the ceiling")
	}
	if spent, priced := l.SpentUSD(); spent != 0.50 || !priced {
		t.Errorf("SpentUSD() = %v/%v, want 0.50/true", spent, priced)
	}
}

func TestTurnCeilingWinsWhenBothBind(t *testing.T) {
	// Both ceilings can trip on the same turn. The turn one is reported because
	// it is the one a user watching --max-turns is asking about.
	l := New(1, 0.10)
	_ = l.CountTurn()
	_ = l.AddCost(0.50, true) // already over
	_ = l.CountTurn()         // now over on turns too

	err := l.Exceeded()
	if !errors.Is(err, MaxTurnsErr) {
		t.Errorf("Exceeded() = %v, want a MaxTurnsErr when both ceilings bind", err)
	}
}

func TestIsStop(t *testing.T) {
	if IsStop(nil) {
		t.Error("IsStop(nil) = true, want false")
	}
	if IsStop(errors.New("network unreachable")) {
		t.Error("IsStop(plain error) = true, want false")
	}
	turns := New(1, 0)
	_ = turns.CountTurn()
	if !IsStop(turns.CountTurn()) {
		t.Error("IsStop(turn stop) = false, want true")
	}
	if !IsStop(New(0, 0.10).AddCost(1, true)) {
		t.Error("IsStop(cost stop) = false, want true")
	}
	// A wrapped stop still matches: callers wrap run results for context.
	wrapped := errors.Join(errors.New("agent run"), &Stopped{Which: MaxTurnsErr})
	if !IsStop(wrapped) {
		t.Error("IsStop(wrapped stop) = false, want true")
	}
}

func TestUnpricedStopSaysSo(t *testing.T) {
	// A Stopped built for an unpriced model must not read like an overspend.
	s := &Stopped{Which: BudgetExceededErr, Turns: 3, Limit: 5.00}
	msg := s.Error()
	if !strings.Contains(msg, "pricing is unknown") {
		t.Errorf("Error() = %q, want it to say the pricing is unknown", msg)
	}
	if strings.Contains(msg, "$0.0000") {
		t.Errorf("Error() = %q, want it not to imply a $0.00 spend", msg)
	}
}

func TestSummary(t *testing.T) {
	tests := []struct {
		name    string
		limits  *Limits
		count   int
		cost    float64
		priced  bool
		wantSub string
	}{
		{
			name:    "priced run reports turns and dollars",
			limits:  New(0, 0),
			count:   2,
			cost:    0.0125,
			priced:  true,
			wantSub: "2 turn(s), $0.0125 estimated",
		},
		{
			name:    "unpriced run says so rather than claiming zero",
			limits:  New(0, 0),
			count:   4,
			wantSub: "4 turn(s); cost unknown",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for range tc.count {
				_ = tc.limits.CountTurn()
			}
			_ = tc.limits.AddCost(tc.cost, tc.priced)
			if got := tc.limits.Summary(); !strings.Contains(got, tc.wantSub) {
				t.Errorf("Summary() = %q, want it to contain %q", got, tc.wantSub)
			}
		})
	}
}

// A Limits is shared between the event loop (turn counting) and the model
// wrapper (cost charging), which ADK runs on different goroutines.
func TestConcurrentUseIsSafe(t *testing.T) {
	l := New(1000, 1000)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 200 {
			_ = l.CountTurn()
		}
	}()
	go func() {
		defer wg.Done()
		for range 200 {
			_ = l.AddCost(0.0001, true)
		}
	}()
	// A third writer-ish reader, so the run is raced under -race the way it
	// runs live. Deliberately not in the WaitGroup — the readers below finish
	// whenever they finish and the test does not wait on them.
	go func() {
		for range 200 {
			_ = l.Exceeded()
			_, _ = l.SpentUSD()
			_ = l.Turns()
		}
	}()
	wg.Wait()

	if got := l.Turns(); got != 200 {
		t.Errorf("Turns() = %d, want 200 — every CountTurn must land", got)
	}
}
