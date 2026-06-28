package rlm

import (
	"context"

	"github.com/omarluq/anamnesis/internal/terminal"
)

// flatQueryDepth is the trace depth stamped on agent.Query lifecycle events on the
// pre-recursion code path: a sub-call fans out one flat level below the depth-0
// controller turn, so it sits at depth 1 — matching the offline replay script and
// the trace pane's one-level indent. The recursion branch later threads a real
// RecursionContext depth through these call sites in place of this fixed level.
const flatQueryDepth = 1

// Emitter publishes controller trace events onto the shell's trace channel,
// tagging every event with the run ID so the UI loop can drop stale work. Sends
// are synchronous: the controller posts and the single UI loop drains.
type Emitter struct {
	done   <-chan struct{}
	events chan<- terminal.TraceEvent
	runID  uint64
}

// NewEmitter returns an Emitter that sends on events and stamps each event with
// runID. It captures ctx.Done() so every send races the run context's cancellation:
// a stalled UI consumer (or the §6 wall-clock deadline) unblocks the emitter instead
// of wedging the run on a full trace channel — a hazard recursion amplifies because
// child loops and fan-out all share one run emitter.
func NewEmitter(ctx context.Context, events chan<- terminal.TraceEvent, runID uint64) *Emitter {
	return &Emitter{done: ctx.Done(), events: events, runID: runID}
}

// Thinking emits a reasoning-turn event carrying the controller's thinking, which
// the chat transcript renders as the turn's dim/italic thinking block.
func (emitter *Emitter) Thinking(text string) {
	emitter.emit(terminal.TraceKindThinking, text, 0)
}

// CodeStart emits the opening event of a controller turn's code evaluation,
// carrying the generated Go source as the code block's body. The chat transcript
// opens a pending code block that CodeEnd later settles with the captured output.
func (emitter *Emitter) CodeStart(code string) {
	emitter.emit(terminal.TraceKindCodeStart, code, 0)
}

// CodeEnd emits the closing event of a controller turn's code evaluation, carrying
// the captured stdout, return value, and any evaluation error as the output that
// settles the pending code block CodeStart opened.
func (emitter *Emitter) CodeEnd(output string) {
	emitter.emit(terminal.TraceKindCodeEnd, output, 0)
}

// QueryStart emits the opening event of an agent.Query sub-call at depth, carrying
// the sub-call prompt as the query block's args. depth indents the block so nested
// fan-out reads as a tree; QueryEnd at the same depth completes it.
func (emitter *Emitter) QueryStart(prompt string, depth int) {
	emitter.emit(terminal.TraceKindQueryStart, prompt, depth)
}

// QueryEnd emits the closing event of an agent.Query sub-call at depth, carrying
// the sub-call result, completing the query block QueryStart opened at the same
// depth.
func (emitter *Emitter) QueryEnd(result string, depth int) {
	emitter.emit(terminal.TraceKindQueryEnd, result, depth)
}

// Final emits the final-answer event carrying text.
func (emitter *Emitter) Final(text string) {
	emitter.emit(terminal.TraceKindFinal, text, 0)
}

// emit sends a text-only event of the given kind at depth, stamped with the run ID.
func (emitter *Emitter) emit(kind terminal.TraceKind, text string, depth int) {
	emitter.send(terminal.TraceEvent{
		Kind:  kind,
		Text:  text,
		RunID: emitter.runID,
		Depth: depth,
	})
}

// send posts event on the trace channel but abandons the send when the run context
// is canceled first, so a full channel (or a stalled UI consumer) cannot wedge the
// run past its §6 wall-clock deadline.
func (emitter *Emitter) send(event terminal.TraceEvent) {
	select {
	case emitter.events <- event:
	case <-emitter.done:
	}
}
