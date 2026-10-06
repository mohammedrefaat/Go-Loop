package agent

import (
	"iter"

	"google.golang.org/adk/v2/session"

	"github.com/dimetron/pi-go/internal/budget"
)

// LimitTurns wraps a run so it stops at the turn ceiling in limits.
//
// The ceiling cannot live inside ADK's flow, which loops until the model stops
// asking for tools, so it is enforced here — on the one place every caller of
// RunStreaming already goes through.
//
// Three things make this a wrapper rather than a field on the flow:
//
//   - The sequence is lazy. Nothing has happened until the caller ranges, so a
//     counter that ran at call time would count turns for a run that never
//     started. The count below happens during ranging, which is when a turn
//     actually occurs.
//   - Stopping means returning from the yield closure, not yielding an error.
//     An error here would travel out through the caller's retry loop, and
//     retry.IsTransient classifies by message substring — a sentence mentioning
//     a limit reads as a rate limit and spends the whole retry budget arriving
//     at the same refusal. Ending the sequence with a nil error leaves the stop
//     distinguishable through limits.Exceeded() and unretryable.
//   - It stops before yielding the event that crossed the line. ADK treats a
//     false yield as end-of-run, so the model call for that turn has happened
//     and been paid for, but neither its output nor any tool it asked for
//     reaches the session. That is the direction a budget has to work in: the
//     overspend is already banked, and stopping afterwards would let the turn
//     after it start.
//
// The caller must consult [budget.Limits.Exceeded] after the range ends. This
// function cannot report the stop itself — the sequence's only channel back to
// the caller is the error one, and using it would reintroduce the retry
// problem.
func LimitTurns(run iter.Seq2[*session.Event, error], limits *budget.Limits) iter.Seq2[*session.Event, error] {
	if limits.Unlimited() {
		return run
	}
	return func(yield func(*session.Event, error) bool) {
		// Pre-flight. Limits are per-run so a fresh --max-turns cannot reach
		// this, but the wrapper is exported and Limits can be shared, and the
		// check is free.
		if limits.Exceeded() != nil {
			return
		}
		// A turn begins at anything that is not model output: the echoed user
		// message, a tool result. set below, so the model events that follow are
		// one round-trip rather than several.
		started := true
		for ev, err := range run {
			if turn := started && isModelTurn(ev); turn {
				_ = limits.CountTurn()
				started = false
				if limits.Exceeded() != nil {
					return
				}
			} else if ev != nil && !isModelOutput(ev) {
				started = true
			}
			if !yield(ev, err) {
				return
			}
		}
	}
}

// isModelOutput reports whether ev is the model producing something, as opposed
// to a tool result or the echoed user message.
func isModelOutput(ev *session.Event) bool {
	if ev == nil || ev.Content == nil {
		return false
	}
	return modelRoles[ev.Content.Role]
}

// isModelTurn reports whether ev closes one model round-trip.
//
// It is the non-partial model event, and that qualifier is the whole reason
// this is not simply `isModelOutput`. Under SSE streaming a single round-trip
// emits the reply as a stream of partials and then repeats it once as an
// aggregate; counting both would make one turn count many times over and turn a
// --max-turns of 5 into two real turns. A tool-calling turn aggregates too —
// the event carries the FunctionCall rather than text — so counting aggregates
// counts rounds, whether or not the model asked for anything.
func isModelTurn(ev *session.Event) bool {
	return isModelOutput(ev) && !ev.Partial
}
