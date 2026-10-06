package agent

import (
	"errors"
	"iter"
	"testing"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/budget"
)

// The tests below cover the counting rules LimitTurns depends on. The
// interesting cases are the ones where a plausible-looking count is wrong:
// SSE partials, thinking events, tool results, and a consumer that stops
// reading early.

// modelEv builds a non-partial model event — the aggregate that closes a
// round-trip.
func modelEv(text string) *session.Event {
	return &session.Event{Content: genai.NewContentFromText(text, genai.RoleModel)}
}

// modelPartialEv builds the SSE chunk shape: same role, Partial set.
func modelPartialEv(text string) *session.Event {
	ev := modelEv(text)
	ev.Partial = true
	return ev
}

// thinkingEv builds a thinking event, which is model output but not the answer.
func thinkingEv(text string) *session.Event {
	return &session.Event{Content: genai.NewContentFromText(text, "thinking")}
}

// userEv builds the echoed user message, which starts a turn.
func userEv(text string) *session.Event {
	return &session.Event{Author: "user", Content: genai.NewContentFromText(text, "user")}
}

// toolResultEv builds a tool response, which also starts a turn.
func toolResultEv(name string) *session.Event {
	return &session.Event{
		Content: &genai.Content{
			Role:  "user",
			Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{Name: name}}},
		},
	}
}

// seqOf turns a slice of events into the run sequence LimitTurns consumes.
func seqOf(events ...*session.Event) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

// drain ranges a sequence and reports what came out.
func drain(seq iter.Seq2[*session.Event, error]) (n int, err error) {
	for ev, e := range seq {
		if ev != nil {
			n++
		}
		if e != nil && err == nil {
			err = e
		}
	}
	return n, err
}

func TestLimitTurnsUnlimitedIsTheIdentity(t *testing.T) {
	// A run with no ceilings must be untouched: the same sequence value comes
	// back, so no wrapper is even allocated.
	events := []*session.Event{userEv("hi"), modelEv("hello"), toolResultEv("bash"), modelEv("done")}
	run := seqOf(events...)

	if got := LimitTurns(run, budget.New(0, 0)); got == nil {
		t.Fatal("LimitTurns(nil run) — want a sequence")
	}
	var nilLimits *budget.Limits
	n, err := drain(LimitTurns(run, nilLimits))
	if n != len(events) {
		t.Errorf("yielded %d events, want %d under an unlimited ceiling", n, len(events))
	}
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestLimitTurnsCountsOneTurnPerRoundTrip(t *testing.T) {
	// Four model round-trips, each separated by whatever re-armed the counter:
	// a user message, a tool result. The ceiling of 2 must cut the run after
	// the second round-trip, before the third.
	events := []*session.Event{
		userEv("go"),
		modelEv("calling a tool"), // turn 1
		toolResultEv("bash"),
		modelEv("still calling"), // turn 2
		toolResultEv("bash"),
		modelEv("one more"), // turn 3 — must not be yielded
		modelEv("done"),     // and neither must this
	}
	limits := budget.New(2, 0)

	n, err := drain(LimitTurns(seqOf(events...), limits))
	if err != nil {
		t.Errorf("err = %v, want nil — a stop is not an error", err)
	}
	// user, model 1, tool result, model 2, tool result — the sequence ends when
	// the third round-trip's answer is withheld, and that turn is still counted.
	if n != 5 {
		t.Errorf("yielded %d events, want 5 — the sequence must end before turn 3's output", n)
	}
	if got := limits.Turns(); got != 3 {
		t.Errorf("Turns() = %d, want 3 — the breaching turn is counted, then dropped", got)
	}
	stop := limits.Exceeded()
	if !errors.Is(stop, budget.MaxTurnsErr) {
		t.Fatalf("Exceeded() = %v, want a MaxTurnsErr", stop)
	}
}

func TestLimitTurnsDoesNotCountSSEDeltas(t *testing.T) {
	// The regression that matters most: one round-trip delivered as an SSE
	// stream plus its aggregate. Counting partials would make --max-turns 3
	// stop after roughly one real turn.
	events := make([]*session.Event, 0, 18)
	for range 3 {
		events = append(events, userEv("q"))
		for range 4 {
			events = append(events, modelPartialEv("tok"))
		}
		events = append(events, modelEv("the whole answer"))
	}
	limits := budget.New(3, 0)

	n, _ := drain(LimitTurns(seqOf(events...), limits))
	if got := limits.Turns(); got != 3 {
		t.Errorf("Turns() = %d, want 3 — partials must not count", got)
	}
	if stop := limits.Exceeded(); stop != nil {
		t.Errorf("Exceeded() = %v at exactly the ceiling, want nil — 3 turns is allowed", stop)
	}
	// Every event up to and including the third aggregate should have landed:
	// 3 turns x (1 user + 4 partials + 1 aggregate) = 18.
	if n != 18 {
		t.Errorf("yielded %d events, want 18", n)
	}
}

func TestLimitTurnsThinkingSharesItsRoundTrip(t *testing.T) {
	// A thinking event is model output, so it closes the pending turn, but it
	// is the same round-trip as the text that follows it. Both are non-partial
	// aggregates; the counter must not double-count them.
	events := []*session.Event{
		userEv("q"),
		thinkingEv("let me think"),
		modelEv("the answer"), // one turn, two aggregates
		toolResultEv("bash"),
		thinkingEv("hmm"),
		modelEv("second answer"), // turn 2
	}
	limits := budget.New(2, 0)

	drain(LimitTurns(seqOf(events...), limits))
	if got := limits.Turns(); got != 2 {
		t.Errorf("Turns() = %d, want 2 — a thinking block is not its own turn", got)
	}
}

func TestLimitTurnsStopsWhenTheCeilingIsAlreadyTripped(t *testing.T) {
	// A shared Limits can arrive already over. The run must not start rather
	// than burning one more round-trip discovering it.
	limits := budget.New(1, 0)
	_ = limits.CountTurn()
	_ = limits.CountTurn()

	events := []*session.Event{userEv("q"), modelEv("answer")}
	n, err := drain(LimitTurns(seqOf(events...), limits))
	if n != 0 {
		t.Errorf("yielded %d events, want 0 — the run was already over the ceiling", n)
	}
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if got := limits.Turns(); got != 2 {
		t.Errorf("Turns() = %d, want 2 — no turn may be counted after the stop", got)
	}
}

func TestLimitTurnsYieldsNothingWhenTheConsumerStops(t *testing.T) {
	// A consumer that breaks early is a normal end, not a budget stop. The
	// wrapper must not mistake it for one and leave Limits looking breached.
	events := []*session.Event{userEv("q"), modelEv("one"), toolResultEv("bash"), modelEv("two")}
	limits := budget.New(10, 0)

	seen := 0
	for range LimitTurns(seqOf(events...), limits) {
		seen++
		if seen == 2 {
			break
		}
	}
	if seen != 2 {
		t.Errorf("consumer saw %d events, want 2", seen)
	}
	if stop := limits.Exceeded(); stop != nil {
		t.Errorf("Exceeded() = %v after an early consumer break, want nil", stop)
	}
	if got := limits.Turns(); got != 1 {
		t.Errorf("Turns() = %d, want 1 — only the turn actually seen counts", got)
	}
}

func TestLimitTurnsDoesNotCountNilEvents(t *testing.T) {
	// ADK can hand through a nil event; a nil deref here would take down the
	// whole run rather than the budget.
	events := []*session.Event{nil, userEv("q"), nil, modelEv("answer")}
	limits := budget.New(5, 0)

	n, err := drain(LimitTurns(seqOf(events...), limits))
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if n != 2 { // the two non-nil events
		t.Errorf("yielded %d non-nil events, want 2", n)
	}
	if got := limits.Turns(); got != 1 {
		t.Errorf("Turns() = %d, want 1", got)
	}
}

func TestLimitTurnsPropagatesRunErrors(t *testing.T) {
	// A provider failure mid-run is not a budget stop and must reach the caller.
	boom := errors.New("provider exploded")
	run := func(yield func(*session.Event, error) bool) {
		yield(userEv("q"), nil)
		yield(nil, boom)
	}
	limits := budget.New(10, 0)

	_, err := drain(LimitTurns(iter.Seq2[*session.Event, error](run), limits))
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want %v", err, boom)
	}
	if stop := limits.Exceeded(); stop != nil {
		t.Errorf("Exceeded() = %v after a provider error, want nil", stop)
	}
}

func TestLimitTurnsRunsUnlimitedWhenOnlyTheBudgetIsSet(t *testing.T) {
	// A dollar ceiling with no turn ceiling still counts turns — the count is
	// what stream-json reports, and it costs nothing.
	limits := budget.New(0, 100.0)
	events := []*session.Event{userEv("q"), modelEv("a"), toolResultEv("bash"), modelEv("b")}

	n, err := drain(LimitTurns(seqOf(events...), limits))
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if n != len(events) {
		t.Errorf("yielded %d events, want %d", n, len(events))
	}
	if got := limits.Turns(); got != 2 {
		t.Errorf("Turns() = %d, want 2", got)
	}
}
