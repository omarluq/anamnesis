package terminal

import (
	"context"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// drainTrace collects every event a controller posts until it closes the
// channel, failing the test if the stream does not finish within loopTimeout.
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

// submitQuery types text into the composer one rune at a time and presses Enter,
// driving the shell's real submit path through the injected event channel.
func submitQuery(screen *fakeScreen, text string) {
	for _, char := range text {
		screen.inject(runeKey(string(char)))
	}

	screen.inject(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
}

func TestReplayControllerReplaysScriptStampedWithRunID(t *testing.T) {
	t.Parallel()

	controller := newReplayController([]TraceEvent{
		replayLine(TraceKindThinking, "looking", 0),
		replayUsage("meter", 10, 4, 250_000),
		replayLine(TraceKindFinal, "done", 0),
	}, 0)

	events := controller.Start(context.Background(), "ignored query", 7)
	got := drainTrace(t, events)

	require.Len(t, got, 3, "every scripted event is replayed before the channel closes")

	for _, event := range got {
		assert.Equal(t, uint64(7), event.RunID, "each replayed event is stamped with the run id")
	}

	// The kinds replay in script order, and the usage meter keeps its token and
	// cost accounting so the cost pane can tally it.
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

	// Stamping a run's events must not bleed into the shared script: the second
	// run re-stamps the same template with its own run id.
	assert.Equal(t, uint64(1), first[0].RunID)
	assert.Equal(t, uint64(2), second[0].RunID)
}

func TestReplayControllerAbandonsRunOnContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// A long pace guarantees the cancellation wins the race before any event is
	// emitted, proving the controller abandons the channel on a dead context.
	controller := newReplayController([]TraceEvent{
		replayLine(TraceKindThinking, "looking", 0),
		replayLine(TraceKindFinal, "done", 0),
	}, time.Hour)

	got := drainTrace(t, controller.Start(ctx, "q", 1))

	assert.Empty(t, got, "a canceled context abandons the run before any event lands")
}

func TestReplayControllerDeliversEventsUnderPace(t *testing.T) {
	t.Parallel()

	// A tiny real pace lets the timer fire between events, exercising the timer.C
	// success branch of wait(); drainTrace's loopTimeout bounds the test so a
	// regression that never delivered would fail fast instead of hanging.
	pace := 20 * time.Millisecond
	controller := newReplayController([]TraceEvent{
		replayLine(TraceKindThinking, "a", 0),
		replayLine(TraceKindFinal, "b", 0),
	}, pace)

	start := time.Now()
	got := drainTrace(t, controller.Start(context.Background(), "q", 1))
	elapsed := time.Since(start)

	// Pacing must actually delay delivery: one beat precedes each of the two
	// events, so the drain cannot finish in less than two paces. A regression
	// that bypassed wait() would return early and trip this lower bound.
	assert.GreaterOrEqual(t, elapsed, 2*pace, "paced delivery waits a beat before each event")
	require.Len(t, got, 2, "every paced event is delivered before the channel closes")
	assert.Equal(t, TraceKindThinking, got[0].Kind)
	assert.Equal(t, TraceKindFinal, got[1].Kind)

	for _, event := range got {
		assert.Equal(t, uint64(1), event.RunID, "each paced event is stamped with the run id")
	}
}

// TestReplayControllerDrivesAppToScriptedFinal wires the offline replay
// controller into a full App, submits a query through the real composer-and-loop
// path, and asserts the scripted thinking/query-start/query-end/usage/final events
// all land: ordered trace lines, accumulated cost totals, and the FINAL answer
// rendered into the chat pane, all without a single network call.
func TestReplayControllerDrivesAppToScriptedFinal(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(100, 32)
	controller := newReplayController(defaultReplayScript(), 0)

	app := newApp(screen, RunOptions{Trace: nil, Controller: controller, Title: defaultTitle})

	done := make(chan error, 1)
	go func() { done <- app.loop(context.Background()) }()

	submitQuery(screen, "why did it crash")

	// The FINAL signal is the last scripted event, so once its trace line renders
	// the whole thinking/query-start/query-end/usage/final sequence has drained in order.
	awaitContents(t, screen, "[final]")

	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))
	require.NoError(t, awaitLoop(t, done))

	// Trace pane: every non-usage scripted event landed, in order, with the
	// agent.Query start and result indented one level and the usage meters routed
	// away to the cost pane.
	assert.Equal(t, []string{
		"[thinking] Turn 1: inspecting the latest boot for failure signatures.",
		"  [query-start] agent.Query: summarize the panic backtrace",
		"  [query-end] the i915 GPU driver oopsed during resume",
		"[final] " + replayFinalAnswer,
	}, traceLines(app))

	// Cost pane: both usage meters accumulated into the session totals.
	assert.Equal(t, 1792, app.cost.tokensIn)
	assert.Equal(t, 384, app.cost.tokensOut)
	assert.Equal(t, "$1.5000", app.cost.dollars())

	// Chat pane: the submitted query was echoed and the FINAL answer rendered.
	assert.Contains(t, app.chat.view.Text, "why did it crash")
	assert.Contains(t, app.chat.view.Text, replayFinalAnswer)
}
