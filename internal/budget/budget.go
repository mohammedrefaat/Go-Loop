// Package budget enforces the two ceilings a headless run needs: a turn count
// and a dollar amount.
//
// It exists as its own package because the two limits are checked in different
// places. A turn limit is a property of the event stream — it is the number of
// model round-trips the run has taken. A cost limit is a property of the
// responses — it needs token counts and a price. Keeping the arithmetic here
// means both are testable without an agent, a provider, or a terminal, and the
// callers stay small enough to read in one sitting.
//
// Two design choices are load-bearing, and both exist because of what a
// headless caller needs to distinguish:
//
//   - A stop is a sentinel error, not a context cancellation. A cancelled
//     context is what Ctrl-C looks like, and a script that runs `pi --max-turns
//     5` in a loop must be able to tell "the agent finished its work" from
//     "the agent was cut off" from "somebody interrupted me" — three different
//     things to three different callers, all of which otherwise end the process
//     the same way.
//   - A stop reports what it stopped on, and how far it got. A budget that
//     reports only "exceeded" leaves the reader guessing which limit bound and
//     by how much, which is the only question someone has when a CI job stops
//     early.
package budget

import (
	"errors"
	"fmt"
	"sync"
)

// Sentinel errors. A caller matches with errors.Is rather than on the message,
// so the wording of Stopped.Error can change without breaking a caller.
//
// MaxTurnsErr is checked before BudgetExceededErr in the Error text but they are
// distinct values: a run can hit the turn ceiling on the same turn that would
// have blown the budget, and reporting only one of the two would hide the fact
// that the other was about to bind too.
var (
	// MaxTurnsErr reports that the run took more model round-trips than the
	// turn ceiling allows.
	MaxTurnsErr = errors.New("turn limit reached")

	// BudgetExceededErr reports that the run's estimated cost passed the dollar
	// ceiling.
	BudgetExceededErr = errors.New("budget exceeded")
)

// Stopped is the error a [Limits] returns when it refuses to let a run
// continue. It wraps one of the package sentinels, so errors.Is works, and
// carries the figures that explain the refusal.
type Stopped struct {
	// Which is the sentinel this stopped on: MaxTurnsErr or
	// BudgetExceededErr.
	Which error

	// Turns is how many model round-trips had been taken when the limit was
	// reached.
	Turns int

	// Limit is the turn or dollar ceiling that bound, matching Which.
	Limit float64

	// Spent is the estimated cost in USD at the moment of the stop. Zero when
	// the turn ceiling bound — the cost of the run was never the constraint.
	Spent float64

	// Priced reports whether a cost estimate was available at all. A model
	// absent from the pricing snapshot cannot produce one, and a budget that
	// silently treated that as $0 would let an unpriced model run unbounded
	// while appearing to enforce a limit.
	Priced bool
}

func (e *Stopped) Error() string {
	switch {
	case errors.Is(e.Which, MaxTurnsErr):
		return fmt.Sprintf("stopped after %d turns: the turn limit is %d", e.Turns, int(e.Limit))
	case !e.Priced:
		// A budget that could not be evaluated is a different failure from one
		// that was exceeded, and saying "spent $0.00 of $5.00" would imply the
		// first.
		return fmt.Sprintf("stopped after %d turns: the %s cost ceiling cannot be evaluated "+
			"because this model's pricing is unknown", e.Turns, usdLabel(e.Limit))
	default:
		return fmt.Sprintf("stopped after %d turns at $%.4f: the cost limit is %s",
			e.Turns, e.Spent, usdLabel(e.Limit))
	}
}

// Unwrap exposes the sentinel so errors.Is(err, MaxTurnsErr) works through the
// Stopped wrapper.
func (e *Stopped) Unwrap() error { return e.Which }

// usdLabel renders a dollar ceiling for an error message. It is not a currency
// conversion — the limit is already in USD.
func usdLabel(v float64) string { return fmt.Sprintf("$%.4f", v) }

// Limits is the ceiling pair for one headless run. The zero value enforces
// nothing, which is what every caller that passes no flags gets.
//
// A Limits is safe for concurrent use: the event loop and the response wrapper
// both touch it, and they run on different goroutines inside ADK.
type Limits struct {
	mu sync.Mutex

	// maxTurns is the turn ceiling; 0 means unlimited.
	maxTurns int
	// maxUSD is the cost ceiling in dollars; 0 means unlimited.
	maxUSD float64

	// turns is how many model round-trips the run has taken.
	turns int
	// spentUSD is the estimated cost so far.
	spentUSD float64
	// priced reports whether at least one response could be priced.
	priced bool
}

// New returns the ceilings for one run. Non-positive values mean unlimited, so
// a caller can pass a flag's zero value straight through.
func New(maxTurns int, maxUSD float64) *Limits {
	return &Limits{maxTurns: maxTurns, maxUSD: maxUSD}
}

// Unlimited reports whether nothing is enforced. A caller uses it to skip the
// work of counting and pricing entirely rather than counting into a struct
// nobody reads.
func (l *Limits) Unlimited() bool {
	if l == nil {
		return true
	}
	return l.maxTurns <= 0 && l.maxUSD <= 0
}

// CountTurn records one model round-trip and reports whether the run may
// continue.
//
// A turn is counted when the model answers, not when it acts: a turn that ends
// in tool calls has spent a round-trip of prompt tokens and will be billed for
// a fresh call next, so counting only final answers would let a
// tool-calling loop run unbounded under a turn limit that looks enforced.
func (l *Limits) CountTurn() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	l.turns++
	if l.maxTurns > 0 && l.turns > l.maxTurns {
		return &Stopped{Which: MaxTurnsErr, Turns: l.turns, Limit: float64(l.maxTurns)}
	}
	return nil
}

// AddCost records one response's estimated cost and reports whether the run may
// continue.
//
// It stops *before* letting the next request out rather than after the one that
// would have crossed the line. The two differ by one response, and the
// direction matters: a budget's purpose is to bound what is spent, and stopping
// after the overspend means the overspend already happened.
//
// A response that cannot be priced is counted as unpriced rather than as free.
// Reporting $0.00 for an unknown model would let it run to the turn ceiling
// while the log claimed the dollar ceiling held.
func (l *Limits) AddCost(cost float64, priced bool) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if priced {
		l.priced = true
		l.spentUSD += cost
	}
	if l.maxUSD > 0 && l.priced && l.spentUSD >= l.maxUSD {
		return &Stopped{
			Which:  BudgetExceededErr,
			Turns:  l.turns,
			Limit:  l.maxUSD,
			Spent:  l.spentUSD,
			Priced: true,
		}
	}
	return nil
}

// Exceeded reports a Stopped for whichever ceiling is currently breached, or
// nil while the run may continue.
//
// It is the pre-flight check: a caller asks before starting work, so the
// request that would cross the line is never sent. CountTurn and AddCost return
// the same value the moment a ceiling trips, which is what tells the caller
// mid-run.
func (l *Limits) Exceeded() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	// Returned through an explicit nil rather than as the *Stopped itself: a
	// nil *Stopped converted to error is a non-nil interface, which would make
	// every "did the budget stop this?" check in every caller report yes.
	if s := l.breached(); s != nil {
		return s
	}
	return nil
}

// breached returns the Stopped for the ceiling that has been passed, or nil.
// The turn ceiling is checked first because it is the one a user is most
// likely to be watching, and reporting it when both bind keeps the message
// about the limit that would have stopped the run first. Must hold mu.
func (l *Limits) breached() *Stopped {
	switch {
	case l.maxTurns > 0 && l.turns > l.maxTurns:
		return &Stopped{Which: MaxTurnsErr, Turns: l.turns, Limit: float64(l.maxTurns)}
	case l.maxUSD > 0 && l.priced && l.spentUSD >= l.maxUSD:
		return &Stopped{
			Which:  BudgetExceededErr,
			Turns:  l.turns,
			Limit:  l.maxUSD,
			Spent:  l.spentUSD,
			Priced: true,
		}
	}
	return nil
}

// IsStop reports whether err is a budget stop. Callers use it to keep a
// deliberate refusal out of the retry path: a budget that has been reached will
// still be reached on the next attempt, so retrying spends money to arrive at
// the same answer.
func IsStop(err error) bool {
	var s *Stopped
	return errors.As(err, &s)
}

// Turns returns how many model round-trips have been recorded.
func (l *Limits) Turns() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.turns
}

// SpentUSD returns the estimated cost so far, and whether any response could be
// priced. Both are reported together because a zero cost is only meaningful
// alongside an answer to "could we price it at all".
func (l *Limits) SpentUSD() (float64, bool) {
	if l == nil {
		return 0, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.spentUSD, l.priced
}

// Summary renders the run's figures for a final report line. It is a plain
// sentence rather than a struct field so both output modes can print it
// identically without importing each other.
func (l *Limits) Summary() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	turns, spent, priced := l.turns, l.spentUSD, l.priced
	l.mu.Unlock()

	switch {
	case !priced:
		return fmt.Sprintf("%d turn(s); cost unknown (no pricing for this model)", turns)
	default:
		return fmt.Sprintf("%d turn(s), $%.4f estimated", turns, spent)
	}
}
