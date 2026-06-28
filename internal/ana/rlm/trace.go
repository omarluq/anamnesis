package rlm

import (
	"context"

	"github.com/omarluq/anamnesis/internal/terminal"
)

// Emitter publishes controller trace events onto the shell's trace channel,
// tagging every event with the run ID so the UI loop can drop stale work. Sends
// are synchronous: the controller posts and the single UI loop drains.
type Emitter struct {
	done   <-chan struct{}
	events chan<- terminal.TraceEvent
	runID  uint64
}

// Compile-time assertion that the emitter satisfies the recursion core's query
// tracer seam, so a drift in either signature fails the build rather than silently
// dropping the structural wiring at newRecursor.
var _ QueryTracer = (*Emitter)(nil)

// NewEmitter returns an Emitter that sends on events and stamps each event with
// runID. It captures ctx.Done() so every send races the run context's cancellation:
// a stalled UI consumer (or a canceled run) unblocks the emitter instead of wedging
// the run on a full trace channel — a hazard recursion amplifies because child loops
// and fan-out all share one run emitter.
func NewEmitter(ctx context.Context, events chan<- terminal.TraceEvent, runID uint64) *Emitter {
	return &Emitter{done: ctx.Done(), events: events, runID: runID}
}

// Thinking emits a reasoning-turn event carrying the controller's thinking, which
// the chat transcript renders as the turn's dim/italic thinking block. After a turn
// streamed its reasoning live via ThinkingDelta, this final event settles that
// pending block with the authoritative summary rather than appending a duplicate.
func (emitter *Emitter) Thinking(text string) {
	emitter.emit(terminal.TraceKindThinking, text)
}

// ThinkingDelta emits one incremental chunk of the controller's reasoning summary as
// it streams from the model, so the chat transcript can grow the turn's thinking
// block live instead of waiting for the whole turn. The matching Thinking event
// settles the block once the turn completes.
func (emitter *Emitter) ThinkingDelta(text string) {
	emitter.emit(terminal.TraceKindThinkingDelta, text)
}

// CodeStart emits the opening event of a controller turn's code evaluation,
// carrying the generated Go source as the code block's body. The chat transcript
// opens a pending code block that CodeEnd later settles with the captured output.
func (emitter *Emitter) CodeStart(code string) {
	emitter.emit(terminal.TraceKindCodeStart, code)
}

// CodeEnd emits the closing event of a controller turn's code evaluation: output
// carries the captured stdout and return value, and errText carries the evaluation
// error (empty on success). A non-empty errText settles the code block as a red
// failure; an empty one settles it as a green success.
func (emitter *Emitter) CodeEnd(output, errText string) {
	emitter.send(terminal.TraceEvent{
		Kind:    terminal.TraceKindCodeEnd,
		Text:    output,
		Err:     errText,
		RunID:   emitter.runID,
		Depth:   0,
		QueryID: 0,
	})
}

// QueryStart emits the opening event of agent.Query sub-call queryID at depth,
// carrying the sub-call prompt as the query block's args. queryID pairs this start
// with its matching end across parallel fan-out, and depth indents the block so
// nested fan-out reads as a tree; QueryEnd with the same id completes it.
func (emitter *Emitter) QueryStart(queryID uint64, prompt string, depth int) {
	emitter.send(terminal.TraceEvent{
		Kind:    terminal.TraceKindQueryStart,
		Text:    prompt,
		Err:     "",
		RunID:   emitter.runID,
		Depth:   depth,
		QueryID: queryID,
	})
}

// QueryEnd emits the closing event of agent.Query sub-call queryID at depth,
// carrying the sub-call result, completing the query block QueryStart opened with
// the same id rather than the newest pending block at this depth.
func (emitter *Emitter) QueryEnd(queryID uint64, result string, depth int) {
	emitter.send(terminal.TraceEvent{
		Kind:    terminal.TraceKindQueryEnd,
		Text:    result,
		Err:     "",
		RunID:   emitter.runID,
		Depth:   depth,
		QueryID: queryID,
	})
}

// JudgeStart emits the opening event of the §16 judge pass, carrying the answer
// under review; the transcript opens a pending judge block at depth 0.
func (emitter *Emitter) JudgeStart(answer string) {
	emitter.emit(terminal.TraceKindJudgeStart, answer)
}

// JudgeEnd emits the closing event of the §16 judge pass, carrying the judge's
// critique — empty on approval; the transcript settles the pending judge block.
func (emitter *Emitter) JudgeEnd(critique string) {
	emitter.emit(terminal.TraceKindJudgeEnd, critique)
}

// Final emits the final-answer event carrying text.
func (emitter *Emitter) Final(text string) {
	emitter.emit(terminal.TraceKindFinal, text)
}

// emit sends a text-only, depth-zero event of the given kind, stamped with the run
// ID and a zero QueryID. The depth-carrying query lifecycle and the failure-carrying
// CodeEnd build their events directly, so every event routed through emit is a
// top-level turn event.
func (emitter *Emitter) emit(kind terminal.TraceKind, text string) {
	emitter.send(terminal.TraceEvent{
		Kind:    kind,
		Text:    text,
		Err:     "",
		RunID:   emitter.runID,
		Depth:   0,
		QueryID: 0,
	})
}

// send posts event on the trace channel but abandons the send when the run context
// is canceled first, so a full channel (or a stalled UI consumer) cannot wedge the
// run past a context cancellation.
func (emitter *Emitter) send(event terminal.TraceEvent) {
	select {
	case emitter.events <- event:
	case <-emitter.done:
	}
}
