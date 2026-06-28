package terminal

import "context"

// Investigator runs one investigation of query end to end, publishing its trace
// events — thinking turns, agent.Query starts and ends, usage meters and the FINAL
// answer — onto events, each stamped with runID. It returns
// the final answer and a nil error on success, or a non-nil error when the run
// could not be assembled or driven to completion. The live RLM adapter binds
// rlm.Investigate to this shape and the di layer supplies the host collaborators
// behind it, which keeps the terminal package free of any rlm import and so out
// of the rlm → terminal trace-event dependency that would otherwise cycle.
type Investigator func(ctx context.Context, query string, events chan<- TraceEvent, runID uint64) (string, error)

// rlmController is the live Controller the chat shell drives: it adapts an
// Investigator to the Controller seam, owning the trace channel's lifecycle so
// the shell drains one run's events and learns the run ended when the channel
// closes. It issues no model or host calls itself — all investigation work
// happens inside the bound Investigator — so the same shell loop drives it, the
// offline replay controller, and a scripted mock through one narrow contract.
type rlmController struct {
	investigate Investigator
}

// NewRLMController returns a Controller that drives investigate once per submit.
// The di layer constructs it with rlm.Investigate bound to the host
// collaborators it resolves, so the terminal package depends only on the
// Investigator function shape rather than on the rlm package.
func NewRLMController(investigate Investigator) Controller {
	return &rlmController{investigate: investigate}
}

// compile-time assertion that rlmController satisfies the Controller seam.
var _ Controller = (*rlmController)(nil)

// Start launches the investigation for query on its own goroutine and returns
// the receive end of the channel the shell drains. The goroutine owns the
// channel: it closes the channel when the run ends so the loop clears its
// working state, and abandons it when ctx is canceled.
func (adapter *rlmController) Start(ctx context.Context, query string, runID uint64) <-chan TraceEvent {
	out := make(chan TraceEvent)
	go adapter.run(ctx, query, runID, out)

	return out
}

// run drives the bound Investigator and closes out once it returns. The
// Investigator emits the FINAL answer itself on success; on failure run posts a
// final event carrying the cause so the shell clears its spinner and shows the
// user why the run ended rather than leaving the composer wedged on a silent
// closed channel.
func (adapter *rlmController) run(ctx context.Context, query string, runID uint64, out chan<- TraceEvent) {
	defer close(out)

	if _, err := adapter.investigate(ctx, query, out, runID); err != nil {
		adapter.emitFailure(ctx, out, runID, err)
	}
}

// emitFailure posts a final-answer event carrying cause so the chat pane surfaces
// the failed run and the loop clears its working spinner. The send is gated on
// ctx so a run the shell already superseded or quit abandons the channel instead
// of blocking forever on a reader that has gone away.
func (adapter *rlmController) emitFailure(ctx context.Context, out chan<- TraceEvent, runID uint64, cause error) {
	event := TraceEvent{
		Kind:       TraceKindFinal,
		Text:       "investigation failed: " + cause.Error(),
		TokensIn:   0,
		TokensOut:  0,
		CostMicros: 0,
		Depth:      0,
		RunID:      runID,
	}

	select {
	case <-ctx.Done():
	case out <- event:
	}
}
