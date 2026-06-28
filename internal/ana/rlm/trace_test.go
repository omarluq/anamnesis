package rlm_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/rlm"
	"github.com/omarluq/anamnesis/internal/terminal"
)

func TestEmitter(t *testing.T) {
	t.Parallel()

	const runID = uint64(7)

	events := make(chan terminal.TraceEvent, 5)
	emitter := rlm.NewEmitter(context.Background(), events, runID)

	emitter.Thinking("planning")
	emitter.QueryStart("fan out boot 3", 1)
	emitter.QueryEnd("boot 3 oom-killed checkout-api", 1)
	emitter.Final("root cause: oom-kill")
	emitter.Usage(4000, 2000, 1234)
	close(events)

	got := make([]terminal.TraceEvent, 0, 5)
	for event := range events {
		got = append(got, event)
	}

	want := []terminal.TraceEvent{
		{
			Kind: terminal.TraceKindThinking, Text: "planning",
			TokensIn: 0, TokensOut: 0, CostMicros: 0, Depth: 0, RunID: runID,
		},
		{
			Kind: terminal.TraceKindQueryStart, Text: "fan out boot 3",
			TokensIn: 0, TokensOut: 0, CostMicros: 0, Depth: 1, RunID: runID,
		},
		{
			Kind: terminal.TraceKindQueryEnd, Text: "boot 3 oom-killed checkout-api",
			TokensIn: 0, TokensOut: 0, CostMicros: 0, Depth: 1, RunID: runID,
		},
		{
			Kind: terminal.TraceKindFinal, Text: "root cause: oom-kill",
			TokensIn: 0, TokensOut: 0, CostMicros: 0, Depth: 0, RunID: runID,
		},
		{
			Kind: terminal.TraceKindUsage, Text: "",
			TokensIn: 4000, TokensOut: 2000, CostMicros: 1234, Depth: 0, RunID: runID,
		},
	}

	require.Len(t, got, len(want))
	assert.Equal(t, want, got)

	for _, event := range got {
		assert.Equal(t, runID, event.RunID, "every event carries the run ID")
	}

	usage := got[4]
	assert.Equal(t, 4000, usage.TokensIn)
	assert.Equal(t, 2000, usage.TokensOut)
	assert.Equal(t, int64(1234), usage.CostMicros)
}

// TestEmitterCarriesQueryDepth proves the query lifecycle methods stamp the depth
// they are handed onto the event so nested fan-out can indent by level, while the
// turn-level methods stay pinned at depth zero.
func TestEmitterCarriesQueryDepth(t *testing.T) {
	t.Parallel()

	const runID = uint64(11)

	events := make(chan terminal.TraceEvent, 4)
	emitter := rlm.NewEmitter(context.Background(), events, runID)

	emitter.QueryStart("outer", 1)
	emitter.QueryStart("inner", 2)
	emitter.QueryEnd("inner result", 2)
	emitter.QueryEnd("outer result", 1)
	close(events)

	got := make([]terminal.TraceEvent, 0, 4)
	for event := range events {
		got = append(got, event)
	}

	require.Len(t, got, 4)
	assert.Equal(t, terminal.TraceKindQueryStart, got[0].Kind)
	assert.Equal(t, 1, got[0].Depth)
	assert.Equal(t, terminal.TraceKindQueryStart, got[1].Kind)
	assert.Equal(t, 2, got[1].Depth)
	assert.Equal(t, terminal.TraceKindQueryEnd, got[2].Kind)
	assert.Equal(t, 2, got[2].Depth)
	assert.Equal(t, terminal.TraceKindQueryEnd, got[3].Kind)
	assert.Equal(t, 1, got[3].Depth)
}

func TestEmitterTagsDistinctRunIDs(t *testing.T) {
	t.Parallel()

	events := make(chan terminal.TraceEvent, 2)
	rlm.NewEmitter(context.Background(), events, 1).Thinking("first run")
	rlm.NewEmitter(context.Background(), events, 2).Thinking("second run")
	close(events)

	got := make([]terminal.TraceEvent, 0, 2)
	for event := range events {
		got = append(got, event)
	}

	require.Len(t, got, 2)
	assert.Equal(t, uint64(1), got[0].RunID)
	assert.Equal(t, uint64(2), got[1].RunID)
	assert.Equal(t, terminal.TraceKindThinking, got[0].Kind)
}
