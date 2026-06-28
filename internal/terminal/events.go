package terminal

// TraceKind labels the category of a TraceEvent emitted by a controller. The set
// is the shared contract the RLM emitter (internal/ana/rlm) produces and the chat
// transcript consumes: thinking turns, the start and completion of a recursive
// agent.Query sub-call, the final answer, and token/cost usage.
type TraceKind string

const (
	// TraceKindThinking marks a reasoning turn the controller produced; the
	// transcript renders it as a collapsible dim/italic thinking block.
	TraceKindThinking TraceKind = "thinking"
	// TraceKindQueryStart marks the start of a recursive agent.Query sub-call; the
	// transcript opens a pending query block, indented by Depth.
	TraceKindQueryStart TraceKind = "query-start"
	// TraceKindQueryEnd marks the completion of the matching agent.Query sub-call;
	// the transcript fills the pending block's output section.
	TraceKindQueryEnd TraceKind = "query-end"
	// TraceKindFinal marks the final answer produced by the loop; the transcript
	// renders it as assistant markdown.
	TraceKindFinal TraceKind = "final"
	// TraceKindUsage carries token and cost accounting for the status footer.
	TraceKindUsage TraceKind = "usage"
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
	// answer. Usage events carry no text.
	Text string
	// Depth is the recursive sub-call nesting level: 0 for a top-level turn and
	// higher for nested fan-out, indenting the rendered query block.
	Depth int
	// TokensIn is the input (prompt) token count for this step, when known.
	TokensIn int
	// TokensOut is the output (completion) token count for this step, when known.
	TokensOut int
	// CostMicros is the cost of this step in millionths of a US dollar.
	CostMicros int64
	// RunID identifies the run that produced the event for stale-event gating.
	RunID uint64
}
