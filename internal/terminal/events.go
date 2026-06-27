package terminal

// TraceKind labels the category of a TraceEvent emitted by a controller.
type TraceKind string

const (
	// TraceKindTurn marks the start of a reasoning turn in the RLM loop.
	TraceKindTurn TraceKind = "turn"
	// TraceKindCode marks the generated Go source a turn evaluated in the REPL.
	TraceKindCode TraceKind = "code"
	// TraceKindStdout marks captured stdout from a turn's evaluated code.
	TraceKindStdout TraceKind = "stdout"
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
