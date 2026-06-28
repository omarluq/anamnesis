package terminal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTraceKindStringValues pins the wire strings of the trace-kind set so the
// emitter, the renderer, and any replay fixtures stay in lockstep on the exact
// labels the contract names.
func TestTraceKindStringValues(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "thinking", string(TraceKindThinking))
	assert.Equal(t, "thinking-delta", string(TraceKindThinkingDelta))
	assert.Equal(t, "code-start", string(TraceKindCodeStart))
	assert.Equal(t, "code-end", string(TraceKindCodeEnd))
	assert.Equal(t, "query-start", string(TraceKindQueryStart))
	assert.Equal(t, "query-end", string(TraceKindQueryEnd))
	assert.Equal(t, "final", string(TraceKindFinal))
}

// TestTraceEventCarriesEveryField proves a TraceEvent value preserves each of the
// fields a controller stamps on it, the immutable record the UI loop reads.
func TestTraceEventCarriesEveryField(t *testing.T) {
	t.Parallel()

	event := TraceEvent{
		Kind:    TraceKindQueryStart,
		Text:    "summarize the panic backtrace",
		Err:     "syntax error: unexpected EOF",
		Depth:   2,
		RunID:   42,
		QueryID: 9,
	}

	assert.Equal(t, TraceKindQueryStart, event.Kind)
	assert.Equal(t, "summarize the panic backtrace", event.Text)
	assert.Equal(t, "syntax error: unexpected EOF", event.Err)
	assert.Equal(t, 2, event.Depth)
	assert.Equal(t, uint64(42), event.RunID)
	assert.Equal(t, uint64(9), event.QueryID)
}
