package rlm

import "github.com/omarluq/anamnesis/internal/terminal"

// Emitter publishes controller trace events onto the shell's trace channel,
// tagging every event with the run ID so the UI loop can drop stale work. Sends
// are synchronous: the controller posts and the single UI loop drains.
type Emitter struct {
	events chan<- terminal.TraceEvent
	runID  uint64
}

// NewEmitter returns an Emitter that sends on events and stamps each event with runID.
func NewEmitter(events chan<- terminal.TraceEvent, runID uint64) *Emitter {
	return &Emitter{events: events, runID: runID}
}

// Turn emits a reasoning-turn event carrying text.
func (emitter *Emitter) Turn(text string) {
	emitter.emit(terminal.TraceKindTurn, text)
}

// SubCall emits a nested sub-call event carrying text.
func (emitter *Emitter) SubCall(text string) {
	emitter.emit(terminal.TraceKindSubCall, text)
}

// Final emits the final-answer event carrying text.
func (emitter *Emitter) Final(text string) {
	emitter.emit(terminal.TraceKindFinal, text)
}

// Usage emits a usage event carrying input/output token counts and the step cost
// in millionths of a US dollar, for the cost pane's running totals.
func (emitter *Emitter) Usage(tokensIn, tokensOut int, costMicros int64) {
	emitter.events <- terminal.TraceEvent{
		Kind:       terminal.TraceKindUsage,
		Text:       "",
		TokensIn:   tokensIn,
		TokensOut:  tokensOut,
		CostMicros: costMicros,
		RunID:      emitter.runID,
	}
}

// emit sends a text-only event of the given kind, stamped with the run ID.
func (emitter *Emitter) emit(kind terminal.TraceKind, text string) {
	emitter.events <- terminal.TraceEvent{
		Kind:       kind,
		Text:       text,
		TokensIn:   0,
		TokensOut:  0,
		CostMicros: 0,
		RunID:      emitter.runID,
	}
}
