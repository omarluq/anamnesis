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
	defaultMaxTurns    = 30
	defaultMaxDepth    = 3
	defaultMaxSubCalls = 60
	// defaultWallTimeout is the hard wall-clock backstop for a whole investigation.
	// gpt-5.5 reasons at maximum effort with unbounded output, so each turn can take
	// a minute or more; a thorough multi-turn investigation needs generous headroom,
	// so this is sized at 30 minutes. The 30-turn and 60-sub-call budgets are the real
	// bound, with this as the outer cap that prevents a wedged run from hanging forever.
	defaultWallTimeout = 1800 * time.Second
	// defaultPerEvalTimeout bounds a single controller eval — the model-generated Go
	// run synchronously through the mvm interpreter, which has no preemption of its
	// own, so a model-emitted non-terminating loop would otherwise wedge the eval
	// goroutine forever (the wall-clock ctx cannot interrupt a loop that never checks
	// it). 10 minutes is deliberately generous: a legitimately slow turn is slow
	// because of ctx-honoring network sub-calls (a max-effort controller model,
	// unbounded output, an 8-wide QueryBatched fan-out each possibly a child loop), so
	// 10 minutes clears real work; a wedged loop does zero I/O, so the timeout caps it.
	// EvalContext still selects on ctx.Done, so defaultWallTimeout stays the outer bound.
	defaultPerEvalTimeout = 600 * time.Second
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
	// PerEvalTimeout bounds a single eval of model-generated Go, so a non-terminating
	// loop the interpreter cannot preempt is force-finished rather than hanging the
	// session forever. See defaultPerEvalTimeout.
	PerEvalTimeout time.Duration
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
	budget.PerEvalTimeout = defaultPerEvalTimeout
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
