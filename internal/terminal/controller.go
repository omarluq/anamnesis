package terminal

import "context"

// Controller starts investigation runs for chat queries and surfaces their
// progress as TraceEvents. The live RLM adapter (internal/ana/rlm) and an
// offline replay controller both implement it, so the shell can drive either a
// real investigation or a scripted demo through the same seam without knowing
// which one it holds.
//
// Start launches the run for query under runID and returns the receive end of a
// channel carrying that run's TraceEvents, each one stamped with runID so the
// single UI loop can drop stale work. Implementations run on their own
// goroutine, close the channel when the run ends, and abandon it when ctx is
// canceled.
type Controller interface {
	// Start begins a run for query, tags every emitted event with runID, and
	// returns the channel the shell loop drains until the implementation closes
	// it.
	Start(ctx context.Context, query string, runID uint64) <-chan TraceEvent
}
