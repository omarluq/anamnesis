package rlm_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/rlm"
	"github.com/omarluq/anamnesis/internal/terminal"
)

func TestEmitter(t *testing.T) {
	t.Parallel()

	const runID = uint64(7)

	events := make(chan terminal.TraceEvent, 4)
	emitter := rlm.NewEmitter(events, runID)

	emitter.Turn("planning")
	emitter.SubCall("fan out boot 3")
	emitter.Final("root cause: oom-kill")
	emitter.Usage(4000, 2000, 1234)
	close(events)

	got := make([]terminal.TraceEvent, 0, 4)
	for event := range events {
		got = append(got, event)
	}

	want := []terminal.TraceEvent{
		{
			Kind: terminal.TraceKindTurn, Text: "planning",
			TokensIn: 0, TokensOut: 0, CostMicros: 0, RunID: runID,
		},
		{
			Kind: terminal.TraceKindSubCall, Text: "fan out boot 3",
			TokensIn: 0, TokensOut: 0, CostMicros: 0, RunID: runID,
		},
		{
			Kind: terminal.TraceKindFinal, Text: "root cause: oom-kill",
			TokensIn: 0, TokensOut: 0, CostMicros: 0, RunID: runID,
		},
		{
			Kind: terminal.TraceKindUsage, Text: "",
			TokensIn: 4000, TokensOut: 2000, CostMicros: 1234, RunID: runID,
		},
	}

	require.Len(t, got, len(want))
	assert.Equal(t, want, got)

	for _, event := range got {
		assert.Equal(t, runID, event.RunID, "every event carries the run ID")
	}

	usage := got[3]
	assert.Equal(t, 4000, usage.TokensIn)
	assert.Equal(t, 2000, usage.TokensOut)
	assert.Equal(t, int64(1234), usage.CostMicros)
}

func TestEmitterTagsDistinctRunIDs(t *testing.T) {
	t.Parallel()

	events := make(chan terminal.TraceEvent, 2)
	rlm.NewEmitter(events, 1).Turn("first run")
	rlm.NewEmitter(events, 2).Turn("second run")
	close(events)

	got := make([]terminal.TraceEvent, 0, 2)
	for event := range events {
		got = append(got, event)
	}

	require.Len(t, got, 2)
	assert.Equal(t, uint64(1), got[0].RunID)
	assert.Equal(t, uint64(2), got[1].RunID)
	assert.Equal(t, terminal.TraceKindTurn, got[0].Kind)
}
