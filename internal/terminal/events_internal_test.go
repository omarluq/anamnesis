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
	assert.Equal(t, "query-start", string(TraceKindQueryStart))
	assert.Equal(t, "query-end", string(TraceKindQueryEnd))
	assert.Equal(t, "final", string(TraceKindFinal))
	assert.Equal(t, "usage", string(TraceKindUsage))
}

// TestTraceEventCarriesEveryField proves a TraceEvent value preserves each of the
// fields a controller stamps on it, the immutable record the UI loop reads.
func TestTraceEventCarriesEveryField(t *testing.T) {
	t.Parallel()

	event := TraceEvent{
		Kind:       TraceKindQueryStart,
		Text:       "summarize the panic backtrace",
		TokensIn:   1280,
		TokensOut:  256,
		CostMicros: 900_000,
		Depth:      2,
		RunID:      42,
	}

	assert.Equal(t, TraceKindQueryStart, event.Kind)
	assert.Equal(t, "summarize the panic backtrace", event.Text)
	assert.Equal(t, 1280, event.TokensIn)
	assert.Equal(t, 256, event.TokensOut)
	assert.Equal(t, int64(900_000), event.CostMicros)
	assert.Equal(t, 2, event.Depth)
	assert.Equal(t, uint64(42), event.RunID)
}
