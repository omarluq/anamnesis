// Package rlm implements the recursive language model controller loop: budget
// enforcement, controller-turn history rendering, and trace event emission.
package rlm

import (
	"sync/atomic"
	"time"

	"github.com/samber/oops"
)

// Hard limits seeded from SPEC §6 and §18; enforced in code, not in the prompt.
const (
	defaultMaxTurns    = 12
	defaultMaxDepth    = 3
	defaultMaxSubCalls = 30
	// defaultWallTimeout is the hard wall-clock backstop for a whole investigation.
	// gpt-5.5 is a reasoning model whose turns each take tens of seconds, so a
	// multi-turn investigation needs far more than the original 120s (which tripped
	// "context deadline exceeded" mid-run); the 12-turn and 30-sub-call budgets are
	// the real bound, with this as the outer cap.
	defaultWallTimeout = 600 * time.Second
)

// Distinct budget sentinels; each carries its own machine-readable oops code.
var (
	errTurnsExceeded    = oops.In("rlm").Code("budget_turns_exceeded").Errorf("controller turn budget exhausted")
	errDepthExceeded    = oops.In("rlm").Code("budget_depth_exceeded").Errorf("recursion depth budget exhausted")
	errSubCallsExceeded = oops.In("rlm").Code("budget_sub_calls_exceeded").Errorf("sub-call budget exhausted")
)

// Budget holds the controller's hard limits together with thread-safe counters
// for turns, recursion depth, and sub-calls. The zero value is not usable;
// construct one with NewBudget. A Budget must not be copied after first use.
type Budget struct {
	// WallTimeout bounds the wall-clock time for a whole session.
	WallTimeout time.Duration
	// MaxTurns bounds the number of controller turns per session.
	MaxTurns int
	// MaxDepth bounds the agent.Query recursion depth.
	MaxDepth int
	// MaxSubCalls bounds the total sub-calls reserved per session.
	MaxSubCalls int
	turns       atomic.Int64
	depth       atomic.Int64
	subCalls    atomic.Int64
}

// NewBudget returns a Budget seeded with the SPEC §6/§18 limits and zeroed counters.
func NewBudget() *Budget {
	budget := new(Budget)
	budget.WallTimeout = defaultWallTimeout
	budget.MaxTurns = defaultMaxTurns
	budget.MaxDepth = defaultMaxDepth
	budget.MaxSubCalls = defaultMaxSubCalls

	return budget
}

// ReserveTurn consumes one controller turn, returning errTurnsExceeded once the
// MaxTurns budget is spent. The consumed count is monotonic across a session.
func (budget *Budget) ReserveTurn() error {
	return reserve(&budget.turns, budget.MaxTurns, errTurnsExceeded)
}

// ReserveSubCall consumes one sub-call, returning errSubCallsExceeded once the
// MaxSubCalls budget is spent. The accepted count never exceeds MaxSubCalls even
// under concurrent callers.
func (budget *Budget) ReserveSubCall() error {
	return reserve(&budget.subCalls, budget.MaxSubCalls, errSubCallsExceeded)
}

// ReserveSubCalls reserves count sub-calls in one atomic admission step, returning
// errSubCallsExceeded — and reserving nothing — when the remaining budget cannot
// cover the whole batch. Admitting the batch as a unit lets the parallel fan-out
// accept or reject every pair together, so a batch larger than the remaining budget
// produces no partial sub-calls. The compare-and-swap loop never exposes a transient
// overshoot, so a concurrent batch that does fit is never spuriously rejected. A
// non-positive count reserves nothing and succeeds.
func (budget *Budget) ReserveSubCalls(count int) error {
	if count <= 0 {
		return nil
	}

	for {
		current := budget.subCalls.Load()
		next := current + int64(count)

		if next > int64(budget.MaxSubCalls) {
			return errSubCallsExceeded
		}

		if budget.subCalls.CompareAndSwap(current, next) {
			return nil
		}
	}
}

// EnterDepth claims one recursion level, returning errDepthExceeded when the
// MaxDepth gauge is already saturated. A rejected claim leaves the gauge
// unchanged so concurrent callers never overshoot the limit. Pair every
// accepted EnterDepth with one ExitDepth.
func (budget *Budget) EnterDepth() error {
	if budget.depth.Add(1) > int64(budget.MaxDepth) {
		budget.depth.Add(-1)

		return errDepthExceeded
	}

	return nil
}

// ExitDepth releases one recursion level previously claimed by EnterDepth.
func (budget *Budget) ExitDepth() {
	budget.depth.Add(-1)
}

// reserve consumes one unit from counter, returning sentinel once the limit is
// spent. The increment is atomic, so the accepted count never overshoots limit
// even under concurrent callers.
func reserve(counter *atomic.Int64, limit int, sentinel error) error {
	if counter.Add(1) > int64(limit) {
		return sentinel
	}

	return nil
}
