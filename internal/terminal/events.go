package terminal

// TraceKind labels the category of a TraceEvent emitted by a controller.
type TraceKind string

const (
	// TraceKindTurn marks the start of a reasoning turn in the RLM loop.
	TraceKindTurn TraceKind = "turn"
	// TraceKindSubCall marks a nested sub-call fanned out from a turn.
	TraceKindSubCall TraceKind = "sub-call"
	// TraceKindFinal marks the final answer produced by the loop.
	TraceKindFinal TraceKind = "final"
	// TraceKindUsage carries token and cost accounting for the cost pane.
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
	// Tokens is the token count associated with this step, when known.
	Tokens int
	// CostMicros is the cost of this step in millionths of a US dollar.
	CostMicros int64
	// RunID identifies the run that produced the event for stale-event gating.
	RunID uint64
}
