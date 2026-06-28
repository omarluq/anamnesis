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

// Start launches the investigation for query and returns the receive end of the
// channel the shell drains. Ownership of that channel is split across two
// goroutines so the run never closes a channel an investigation goroutine may
// still be sending on: run drives the Investigator against a private inner
// channel that is NEVER closed, and pump forwards inner onto out and is the SOLE
// closer of out. The split is load-bearing because the mvm interpreter cannot be
// preempted — a timed-out eval is abandoned and its agent.QueryBatched fan-out
// keeps emitting trace events long after the run returned. Closing the channel
// those leaked goroutines send on would panic them on send-to-closed-channel;
// routing their late sends at the never-closed inner channel — which the emitter
// already abandons once the run context is canceled — makes a post-teardown emit
// a safe no-op, while out still closes to tell the shell the run ended.
func (adapter *rlmController) Start(ctx context.Context, query string, runID uint64) <-chan TraceEvent {
	out := make(chan TraceEvent)
	inner := make(chan TraceEvent)
	runDone := make(chan struct{})

	go adapter.run(ctx, query, runID, inner, runDone)
	go adapter.pump(ctx, inner, out, runDone)

	return out
}

// run drives the bound Investigator, publishing its events onto the never-closed
// inner channel, and signals the run ended by closing runDone once it returns.
// The Investigator emits the FINAL answer itself on success; on failure run posts
// a final event carrying the cause so the shell clears its spinner and shows the
// user why the run ended rather than leaving the composer wedged. It never closes
// inner: a leaked fan-out goroutine from an abandoned eval may send on it
// arbitrarily late, and closing it from under that sender would panic on
// send-to-closed-channel — the hazard the run/pump split exists to remove.
func (adapter *rlmController) run(
	ctx context.Context,
	query string,
	runID uint64,
	events chan<- TraceEvent,
	runDone chan<- struct{},
) {
	defer close(runDone)

	if _, err := adapter.investigate(ctx, query, events, runID); err != nil {
		adapter.emitFailure(ctx, events, runID, err)
	}
}

// pump forwards every event from the run's inner channel onto the shell-facing
// out channel and is the sole sender on out, so it alone closes out — when the run
// signals completion through runDone or when ctx is canceled. Because run never
// closes inner and nothing else sends on out, the close that tells the shell the
// run ended can never race a leaked goroutine's emit. Each forwarding send is
// itself gated on ctx so a shell that has moved on to a newer run — and stopped
// draining out — never wedges the pump.
func (adapter *rlmController) pump(
	ctx context.Context,
	events <-chan TraceEvent,
	out chan<- TraceEvent,
	runDone <-chan struct{},
) {
	defer close(out)

	for {
		select {
		case event := <-events:
			adapter.forward(ctx, out, event)
		case <-runDone:
			return
		case <-ctx.Done():
			return
		}
	}
}

// forward posts event onto out but abandons the send when ctx is canceled first,
// so a run the shell has superseded — and stopped draining — cannot wedge the pump
// on a send no reader will ever take.
func (adapter *rlmController) forward(ctx context.Context, out chan<- TraceEvent, event TraceEvent) {
	select {
	case out <- event:
	case <-ctx.Done():
	}
}

// emitFailure posts a final-answer event carrying cause so the chat pane surfaces
// the failed run and the loop clears its working spinner. The send is gated on ctx
// so a run the shell already superseded or quit abandons the channel instead of
// blocking forever on a reader that has gone away. A failure event dropped on that
// gate is backstopped by pump closing out when run returns, which clears the
// spinner too, so the composer never wedges whichever way the race falls.
func (adapter *rlmController) emitFailure(ctx context.Context, events chan<- TraceEvent, runID uint64, cause error) {
	event := TraceEvent{
		Kind:    TraceKindFinal,
		Text:    "investigation failed: " + cause.Error(),
		Err:     "",
		Depth:   0,
		RunID:   runID,
		QueryID: 0,
	}

	select {
	case <-ctx.Done():
	case events <- event:
	}
}
