package rlm_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/rlm"
	"github.com/omarluq/anamnesis/internal/terminal"
)

func TestEmitter(t *testing.T) {
	t.Parallel()

	const runID = uint64(7)

	events := make(chan terminal.TraceEvent, 4)
	emitter := rlm.NewEmitter(context.Background(), events, runID)

	emitter.Thinking("planning")
	emitter.QueryStart(1, "fan out boot 3", 1)
	emitter.QueryEnd(1, "boot 3 oom-killed checkout-api", 1)
	emitter.Final("root cause: oom-kill")
	close(events)

	got := make([]terminal.TraceEvent, 0, 4)
	for event := range events {
		got = append(got, event)
	}

	want := []terminal.TraceEvent{
		{Kind: terminal.TraceKindThinking, Text: "planning", Err: "", Depth: 0, RunID: runID, QueryID: 0},
		{Kind: terminal.TraceKindQueryStart, Text: "fan out boot 3", Err: "", Depth: 1, RunID: runID, QueryID: 1},
		{
			Kind: terminal.TraceKindQueryEnd, Text: "boot 3 oom-killed checkout-api",
			Err: "", Depth: 1, RunID: runID, QueryID: 1,
		},
		{Kind: terminal.TraceKindFinal, Text: "root cause: oom-kill", Err: "", Depth: 0, RunID: runID, QueryID: 0},
	}

	require.Len(t, got, len(want))
	assert.Equal(t, want, got)

	for _, event := range got {
		assert.Equal(t, runID, event.RunID, "every event carries the run ID")
	}
}

// TestEmitterCarriesQueryDepth proves the query lifecycle methods stamp the depth
// they are handed onto the event so nested fan-out can indent by level, while the
// turn-level methods stay pinned at depth zero.
func TestEmitterCarriesQueryDepth(t *testing.T) {
	t.Parallel()

	const runID = uint64(11)

	events := make(chan terminal.TraceEvent, 4)
	emitter := rlm.NewEmitter(context.Background(), events, runID)

	emitter.QueryStart(1, "outer", 1)
	emitter.QueryStart(2, "inner", 2)
	emitter.QueryEnd(2, "inner result", 2)
	emitter.QueryEnd(1, "outer result", 1)
	close(events)

	got := make([]terminal.TraceEvent, 0, 4)
	for event := range events {
		got = append(got, event)
	}

	require.Len(t, got, 4)
	assert.Equal(t, terminal.TraceKindQueryStart, got[0].Kind)
	assert.Equal(t, 1, got[0].Depth)
	assert.Equal(t, uint64(1), got[0].QueryID, "the emitter stamps the start's id")
	assert.Equal(t, terminal.TraceKindQueryStart, got[1].Kind)
	assert.Equal(t, 2, got[1].Depth)
	assert.Equal(t, uint64(2), got[1].QueryID)
	assert.Equal(t, terminal.TraceKindQueryEnd, got[2].Kind)
	assert.Equal(t, 2, got[2].Depth)
	assert.Equal(t, uint64(2), got[2].QueryID, "the inner end carries the inner start's id")
	assert.Equal(t, terminal.TraceKindQueryEnd, got[3].Kind)
	assert.Equal(t, 1, got[3].Depth)
	assert.Equal(t, uint64(1), got[3].QueryID, "the outer end carries the outer start's id")
}

// TestEmitterJudgeLifecycle proves the judge lifecycle methods emit the §16 judge
// kinds at depth 0 with a zero QueryID: JudgeStart carries the answer under review
// and JudgeEnd carries the critique (empty on approval).
func TestEmitterJudgeLifecycle(t *testing.T) {
	t.Parallel()

	const runID = uint64(13)

	events := make(chan terminal.TraceEvent, 2)
	emitter := rlm.NewEmitter(context.Background(), events, runID)

	emitter.JudgeStart("the resolved answer")
	emitter.JudgeEnd("cite the originating boot")
	close(events)

	got := make([]terminal.TraceEvent, 0, 2)
	for event := range events {
		got = append(got, event)
	}

	require.Len(t, got, 2)
	assert.Equal(t, terminal.TraceKindJudgeStart, got[0].Kind)
	assert.Equal(t, "the resolved answer", got[0].Text)
	assert.Equal(t, 0, got[0].Depth)
	assert.Equal(t, uint64(0), got[0].QueryID, "a judge event carries no query-correlation id")
	assert.Equal(t, terminal.TraceKindJudgeEnd, got[1].Kind)
	assert.Equal(t, "cite the originating boot", got[1].Text)
	assert.Equal(t, runID, got[1].RunID)
	assert.Equal(t, 0, got[1].Depth)
	assert.Equal(t, uint64(0), got[1].QueryID, "a judge event carries no query-correlation id")
}

// TestEmitterAbandonsBlockedSendOnCancel exercises the cancel-safe send path: a
// send that blocks on an undrained trace channel must return once the run context
// is canceled, so a stalled UI consumer (or the §6 wall-clock deadline) unblocks
// the emitter instead of wedging the run on a full channel.
func TestEmitterAbandonsBlockedSendOnCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan terminal.TraceEvent)
	emitter := rlm.NewEmitter(ctx, events, 1)

	done := make(chan struct{})

	go func() {
		defer close(done)

		emitter.Thinking("blocked")
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("emitter send stayed blocked after context cancellation")
	}
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
