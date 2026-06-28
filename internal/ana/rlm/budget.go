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
	defaultMaxDepth    = 3
	defaultMaxSubCalls = 60
	// defaultPerEvalTimeout bounds a single controller eval — the model-generated Go
	// runs synchronously through the mvm interpreter, which has no preemption of its
	// own, so a model-emitted non-terminating loop would otherwise wedge the eval
	// goroutine forever. It is the ONLY time bound left on a run: there is no wall-clock
	// or turn budget, so an investigation runs until the model signals agent.FINAL (or
	// the user quits); this per-eval cap is purely an anti-wedge safety for the
	// non-preemptible interpreter, not an investigation budget. 10 minutes is generous:
	// a legitimately slow turn is slow because of ctx-honoring network sub-calls, which
	// it clears; a wedged loop does zero I/O, so the timeout caps it.
	defaultPerEvalTimeout = 600 * time.Second
)

// Distinct budget sentinels; each carries its own machine-readable oops code.
var (
	errDepthExceeded    = oops.In("rlm").Code("budget_depth_exceeded").Errorf("recursion depth budget exhausted")
	errSubCallsExceeded = oops.In("rlm").Code("budget_sub_calls_exceeded").Errorf("sub-call budget exhausted")
)

// Budget holds the controller's hard limits together with thread-safe counters
// for turns, recursion depth, and sub-calls. The zero value is not usable;
// construct one with NewBudget. A Budget must not be copied after first use.
type Budget struct {
	// PerEvalTimeout bounds a single eval of model-generated Go, so a non-terminating
	// loop the interpreter cannot preempt is force-finished rather than hanging the
	// session forever. It is the only time bound on a run. See defaultPerEvalTimeout.
	PerEvalTimeout time.Duration
	// MaxDepth bounds the agent.Query recursion depth.
	MaxDepth int
	// MaxSubCalls bounds the total sub-calls reserved per session.
	MaxSubCalls int
	depth       atomic.Int64
	subCalls    atomic.Int64
	// maxDepthSeen is the high-water mark of tree depth any sub-call reached, advanced
	// by RecordDepth and read only for the run-end observability summary.
	maxDepthSeen atomic.Int64
}

// NewBudget returns a Budget seeded with the SPEC §6/§18 limits and zeroed counters.
func NewBudget() *Budget {
	budget := new(Budget)
	budget.PerEvalTimeout = defaultPerEvalTimeout
	budget.MaxDepth = defaultMaxDepth
	budget.MaxSubCalls = defaultMaxSubCalls

	return budget
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

// RecordDepth advances the high-water mark of tree depth any sub-call reached, read
// only for the run-end observability summary. It is safe under the concurrent
// QueryBatched fan-out: it retries a compare-and-swap until the stored mark is at
// least level, so a deeper sub-call settling out of order never lowers it.
func (budget *Budget) RecordDepth(level int) {
	for {
		seen := budget.maxDepthSeen.Load()
		if int64(level) <= seen || budget.maxDepthSeen.CompareAndSwap(seen, int64(level)) {
			return
		}
	}
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
