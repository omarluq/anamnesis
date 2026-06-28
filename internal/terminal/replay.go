package terminal

import (
	"context"
	"time"
)

// replayFinalAnswer is the FINAL answer the default offline script renders into
// the chat pane once the scripted investigation completes.
const replayFinalAnswer = "Root cause: the i915 GPU driver oopsed during resume; pin the firmware to recover."

// replayController is an offline Controller that answers every query by replaying
// a fixed script of TraceEvents, optionally waiting a fixed pace before each event.
// It performs no network calls and touches no live journal, so it drives the
// shell's demo mode and the offline smoke through the very same Controller seam
// the live RLM adapter satisfies, letting tests and demos exercise the full UI
// loop without an OpenAI key.
type replayController struct {
	script []TraceEvent
	pace   time.Duration
}

// newReplayController returns a replayController that replays script, waiting
// pace before each event: a leading beat before the first line, then the same
// gap before every subsequent event. Pass a zero pace to emit each event as fast
// as the shell drains the channel, which keeps offline smoke runs instant.
func newReplayController(script []TraceEvent, pace time.Duration) *replayController {
	return &replayController{script: script, pace: pace}
}

// compile-time assertion that replayController satisfies the Controller seam.
var _ Controller = (*replayController)(nil)

// Start launches a goroutine that replays the scripted events for the run
// identified by runID and returns the channel the shell drains until the script
// is exhausted or ctx is canceled. The query is ignored on purpose: an offline
// replay answers every prompt with the same canned investigation transcript.
func (controller *replayController) Start(ctx context.Context, _ string, runID uint64) <-chan TraceEvent {
	out := make(chan TraceEvent)
	go controller.replay(ctx, out, runID)

	return out
}

// replay streams the script onto out, stamping each event with runID so the
// shell loop can gate stale work, and closes out once the script is exhausted or
// ctx is canceled.
func (controller *replayController) replay(ctx context.Context, out chan<- TraceEvent, runID uint64) {
	defer close(out)

	for _, event := range controller.script {
		event.RunID = runID

		if !controller.emit(ctx, out, event) {
			return
		}
	}
}

// emit honors the configured pace and then posts event onto out, reporting false
// when ctx is canceled before the event is delivered so the caller abandons the
// rest of the script.
func (controller *replayController) emit(ctx context.Context, out chan<- TraceEvent, event TraceEvent) bool {
	if !controller.wait(ctx) {
		return false
	}

	select {
	case <-ctx.Done():
		return false
	case out <- event:
		return true
	}
}

// wait blocks for the controller's pace and reports true once it elapses, returns
// true immediately when pacing is disabled, and reports false if ctx is canceled
// before the delay completes.
func (controller *replayController) wait(ctx context.Context) bool {
	if controller.pace <= 0 {
		return true
	}

	timer := time.NewTimer(controller.pace)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// defaultReplayScript returns the canned investigation transcript the offline
// demo and smoke replay: one thinking turn that inspects the latest boot, a
// fanned-out agent.Query start and its result at depth one, a usage meter for each
// step, and the FINAL answer.
func defaultReplayScript() []TraceEvent {
	return []TraceEvent{
		replayLine(TraceKindThinking, "Turn 1: inspecting the latest boot for failure signatures.", 0),
		replayUsage("turn 1 usage", 1280, 256, 900_000),
		replayLine(TraceKindQueryStart, "agent.Query: summarize the panic backtrace", 1),
		replayLine(TraceKindQueryEnd, "the i915 GPU driver oopsed during resume", 1),
		replayUsage("sub-call usage", 512, 128, 600_000),
		replayLine(TraceKindFinal, replayFinalAnswer, 0),
	}
}

// replayLine builds a non-usage TraceEvent of kind carrying text at the given
// sub-call depth, leaving the token and cost fields zeroed.
func replayLine(kind TraceKind, text string, depth int) TraceEvent {
	return TraceEvent{
		Kind:       kind,
		Text:       text,
		TokensIn:   0,
		TokensOut:  0,
		CostMicros: 0,
		Depth:      depth,
		RunID:      0,
	}
}

// replayUsage builds a usage TraceEvent carrying the token counts and cost the
// shell tallies into the cost pane, labeled with text for the trace log.
func replayUsage(text string, tokensIn, tokensOut int, costMicros int64) TraceEvent {
	return TraceEvent{
		Kind:       TraceKindUsage,
		Text:       text,
		TokensIn:   tokensIn,
		TokensOut:  tokensOut,
		CostMicros: costMicros,
		Depth:      0,
		RunID:      0,
	}
}
