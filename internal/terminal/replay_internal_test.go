package terminal

import (
	"context"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// drainTrace collects every event a controller posts until it closes the channel,
// failing the test if the stream does not finish within loopTimeout.
func drainTrace(t *testing.T, events <-chan TraceEvent) []TraceEvent {
	t.Helper()

	collected := make([]TraceEvent, 0)
	deadline := time.After(loopTimeout)

	for {
		select {
		case event, ok := <-events:
			if !ok {
				return collected
			}

			collected = append(collected, event)
		case <-deadline:
			t.Fatal("timed out draining replay trace channel")

			return collected
		}
	}
}

func TestReplayControllerReplaysScriptStampedWithRunID(t *testing.T) {
	t.Parallel()

	controller := newReplayController([]TraceEvent{
		replayLine(TraceKindThinking, "looking", 0),
		replayUsage(10, 4, 250_000),
		replayLine(TraceKindFinal, "done", 0),
	}, 0)

	events := controller.Start(context.Background(), "ignored query", 7)
	got := drainTrace(t, events)

	require.Len(t, got, 3, "every scripted event is replayed before the channel closes")

	for _, event := range got {
		assert.Equal(t, uint64(7), event.RunID, "each replayed event is stamped with the run id")
	}

	assert.Equal(t, TraceKindThinking, got[0].Kind)
	assert.Equal(t, TraceKindUsage, got[1].Kind)
	assert.Equal(t, 10, got[1].TokensIn)
	assert.Equal(t, 4, got[1].TokensOut)
	assert.Equal(t, int64(250_000), got[1].CostMicros)
	assert.Equal(t, TraceKindFinal, got[2].Kind)
}

func TestReplayControllerLeavesScriptUnmutatedAcrossRuns(t *testing.T) {
	t.Parallel()

	controller := newReplayController([]TraceEvent{
		replayLine(TraceKindFinal, "done", 0),
	}, 0)

	first := drainTrace(t, controller.Start(context.Background(), "q", 1))
	second := drainTrace(t, controller.Start(context.Background(), "q", 2))

	require.Len(t, first, 1)
	require.Len(t, second, 1)

	assert.Equal(t, uint64(1), first[0].RunID)
	assert.Equal(t, uint64(2), second[0].RunID)
}

func TestReplayControllerAbandonsRunOnContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	controller := newReplayController([]TraceEvent{
		replayLine(TraceKindThinking, "looking", 0),
		replayLine(TraceKindFinal, "done", 0),
	}, time.Hour)

	got := drainTrace(t, controller.Start(ctx, "q", 1))

	assert.Empty(t, got, "a canceled context abandons the run before any event lands")
}

func TestReplayControllerDeliversEventsUnderPace(t *testing.T) {
	t.Parallel()

	pace := 20 * time.Millisecond
	controller := newReplayController([]TraceEvent{
		replayLine(TraceKindThinking, "a", 0),
		replayLine(TraceKindFinal, "b", 0),
	}, pace)

	start := time.Now()
	got := drainTrace(t, controller.Start(context.Background(), "q", 1))
	elapsed := time.Since(start)

	assert.GreaterOrEqual(t, elapsed, 2*pace, "paced delivery waits a beat before each event")
	require.Len(t, got, 2, "every paced event is delivered before the channel closes")
	assert.Equal(t, TraceKindThinking, got[0].Kind)
	assert.Equal(t, TraceKindFinal, got[1].Kind)
}

func TestDefaultReplayScriptShowsThinkingAndNestedQuery(t *testing.T) {
	t.Parallel()

	script := defaultReplayScript()

	kinds := lo.Map(script, func(event TraceEvent, _ int) TraceKind { return event.Kind })

	assert.Equal(t, []TraceKind{
		TraceKindThinking,
		TraceKindUsage,
		TraceKindQueryStart,
		TraceKindQueryEnd,
		TraceKindUsage,
		TraceKindFinal,
	}, kinds, "the demo script shows a thinking turn and a nested query lifecycle")

	for _, event := range script {
		if event.Kind == TraceKindQueryStart || event.Kind == TraceKindQueryEnd {
			assert.Equal(t, 1, event.Depth, "the demo query is nested one level deep")
		}
	}
}
