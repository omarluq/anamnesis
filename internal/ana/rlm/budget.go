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
	defaultWallTimeout = 120 * time.Second
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
