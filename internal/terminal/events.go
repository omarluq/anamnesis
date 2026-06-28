package terminal

// TraceKind labels the category of a TraceEvent emitted by a controller.
type TraceKind string

const (
	// TraceKindThinking marks a controller reasoning turn's thinking, rendered as
	// the dim/italic thinking block in the chat transcript.
	TraceKindThinking TraceKind = "thinking"
	// TraceKindQueryStart marks the start of an agent.Query sub-call, opening a
	// depth-indented query block whose args carry the sub-call prompt.
	TraceKindQueryStart TraceKind = "query-start"
	// TraceKindQueryEnd marks an agent.Query sub-call returning, completing the
	// query block opened at the same depth with the sub-call's result.
	TraceKindQueryEnd TraceKind = "query-end"
	// TraceKindFinal marks the final answer produced by the loop.
	TraceKindFinal TraceKind = "final"
	// TraceKindUsage carries token and cost accounting for the footer totals.
	TraceKindUsage TraceKind = "usage"
)

// TraceEvent is a typed, immutable message a background controller posts onto
// the shell's trace channel. The single UI loop goroutine owns all mutation, so
// events are values only. RunID tags the originating run; the loop drops events
// whose RunID does not match the active run to ignore stale work.
type TraceEvent struct {
	// Kind selects how the event is routed and styled.
	Kind TraceKind
	// Text is the human-readable line shown in the trace pane.
	Text string
	// TokensIn is the input (prompt) token count for this step, when known.
	TokensIn int
	// TokensOut is the output (completion) token count for this step, when known.
	TokensOut int
	// CostMicros is the cost of this step in millionths of a US dollar.
	CostMicros int64
	// Depth is the sub-call nesting level: 0 for a top-level controller turn and
	// higher for events emitted from nested fan-out, indenting the trace line.
	Depth int
	// RunID identifies the run that produced the event for stale-event gating.
	RunID uint64
}
