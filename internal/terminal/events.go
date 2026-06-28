package terminal

// TraceKind labels the category of a TraceEvent emitted by a controller. The set
// is the shared contract the RLM emitter (internal/ana/rlm) produces and the chat
// transcript consumes: thinking turns, the start and completion of a turn's code
// evaluation, the start and completion of a recursive agent.Query sub-call, the
// start and completion of the §16 judge pass, and the final answer.
type TraceKind string

const (
	// TraceKindThinking marks a reasoning turn the controller produced; the
	// transcript renders it as a dim/italic thinking block. When the turn streamed
	// its reasoning via TraceKindThinkingDelta, this event settles the pending block
	// with the authoritative summary instead of appending a duplicate.
	TraceKindThinking TraceKind = "thinking"
	// TraceKindThinkingDelta marks one incremental chunk of the controller's reasoning
	// summary streaming in; the transcript opens a pending thinking block on the first
	// delta and appends each subsequent chunk so thinking renders live.
	TraceKindThinkingDelta TraceKind = "thinking-delta"
	// TraceKindCodeStart marks the start of a turn's generated-Go evaluation; the
	// transcript opens a pending code block carrying the source.
	TraceKindCodeStart TraceKind = "code-start"
	// TraceKindCodeEnd marks the completion of a turn's evaluation; the transcript
	// fills the pending code block's output section with the captured result.
	TraceKindCodeEnd TraceKind = "code-end"
	// TraceKindQueryStart marks the start of a recursive agent.Query sub-call; the
	// transcript opens a pending query block.
	TraceKindQueryStart TraceKind = "query-start"
	// TraceKindQueryEnd marks the completion of the matching agent.Query sub-call;
	// the transcript fills the pending block's output section. Its QueryID matches
	// the TraceKindQueryStart it completes, so parallel fan-out pairs each end with
	// its own start rather than by completion order.
	TraceKindQueryEnd TraceKind = "query-end"
	// TraceKindJudgeStart marks the start of the §16 judge pass over a resolved
	// answer; the transcript opens a pending judge block at depth 0.
	TraceKindJudgeStart TraceKind = "judge-start"
	// TraceKindJudgeEnd marks the completion of the judge pass; the transcript
	// settles the pending judge block as an approval (empty text) or a critique.
	TraceKindJudgeEnd TraceKind = "judge-end"
	// TraceKindFinal marks the final answer produced by the loop; the transcript
	// renders it as assistant markdown.
	TraceKindFinal TraceKind = "final"
)

// TraceEvent is a typed, immutable message a background controller posts onto the
// shell's trace channel. The single UI loop goroutine owns all mutation, so events
// are values only. RunID tags the originating run; the loop drops events whose
// RunID does not match the active run to ignore stale work.
type TraceEvent struct {
	// Kind selects how the event is translated into a transcript message.
	Kind TraceKind
	// Text is the event payload: the thinking text, the query prompt
	// (TraceKindQueryStart), the query result (TraceKindQueryEnd), or the final
	// answer.
	Text string
	// Err is the error text for a settled code block that failed — empty on
	// success. A non-empty Err lands in the block's error: section and paints the
	// block red.
	Err string
	// Depth is the recursive sub-call nesting level recorded on the event: 0 for a
	// top-level turn and higher for nested fan-out. It is carried for trace fidelity
	// (the emitter stamps it and the run-end log reports it), not to indent the block.
	Depth int
	// RunID identifies the run that produced the event for stale-event gating.
	RunID uint64
	// QueryID correlates a TraceKindQueryStart with its matching TraceKindQueryEnd:
	// every sub-call is minted a unique id so the transcript pairs an end with its
	// own start even when parallel fan-out completes out of order. It is 0 on every
	// non-query event.
	QueryID uint64
}
