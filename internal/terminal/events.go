package terminal

// TraceKind labels the category of a TraceEvent emitted by a controller. The set
// is the shared contract the RLM emitter (internal/ana/rlm) produces and the chat
// transcript consumes: thinking turns, the start and completion of a turn's code
// evaluation, the start and completion of a recursive agent.Query sub-call, and
// the final answer.
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
	// transcript opens a pending query block, indented by Depth.
	TraceKindQueryStart TraceKind = "query-start"
	// TraceKindQueryEnd marks the completion of the matching agent.Query sub-call;
	// the transcript fills the pending block's output section.
	TraceKindQueryEnd TraceKind = "query-end"
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
	// Depth is the recursive sub-call nesting level: 0 for a top-level turn and
	// higher for nested fan-out, indenting the rendered query block.
	Depth int
	// RunID identifies the run that produced the event for stale-event gating.
	RunID uint64
}
