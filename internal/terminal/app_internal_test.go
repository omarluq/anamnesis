package terminal

import (
	"context"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewAppDefaultsTitleAndWiresPanes(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)

	app := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: ""})
	require.NotNil(t, app)
	assert.Equal(t, defaultTitle, app.title)
	assert.NotNil(t, app.chat, "chat pane is wired")
	assert.NotNil(t, app.trace, "trace pane is wired")
	assert.NotNil(t, app.cost, "cost pane is wired")

	custom := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: "custom"})
	assert.Equal(t, "custom", custom.title)
}

func TestHeaderTitleAppendsSpinnerOnlyWhileWorking(t *testing.T) {
	t.Parallel()

	app := newApp(newFakeScreen(80, 24), RunOptions{
		Trace:      nil,
		Controller: nil,
		Title:      defaultTitle,
	})

	assert.Equal(t, defaultTitle, app.headerTitle())
	assert.Empty(t, app.spinnerGlyph())

	// A turn event marks the loop working, so the header gains the spinner glyph.
	app.applyTrace(traceEvent(TraceKindTurn, "thinking", 0, 0, 0, 0))
	app.spinnerFrame = 0
	assert.Equal(t, defaultTitle+" "+string(spinnerFrames[0]), app.headerTitle())
	assert.NotEmpty(t, app.spinnerGlyph())

	// A final event clears the working state and retires the spinner.
	app.applyTrace(traceEvent(TraceKindFinal, "done", 0, 0, 0, 0))
	assert.Equal(t, defaultTitle, app.headerTitle())
	assert.Empty(t, app.spinnerGlyph())
}

func TestStartRunBeginsControllerRunAndIgnoresSubmitWhileWorking(t *testing.T) {
	t.Parallel()

	ctrl := new(mockController)
	ctrl.On("Start", mock.Anything, "first question", uint64(1)).
		Return(scriptedTrace(1, traceEvent(TraceKindTurn, "looking", 0, 0, 0, 0))).
		Once()

	app := newApp(newFakeScreen(80, 24), RunOptions{
		Trace:      nil,
		Controller: ctrl,
		Title:      defaultTitle,
	})

	require.Equal(t, uint64(0), app.runID)
	require.False(t, app.working)
	require.Nil(t, app.traceCh, "no run has swapped in a trace channel yet")

	app.startRun(context.Background(), "first question")

	assert.Equal(t, uint64(1), app.runID, "starting a run bumps the run id")
	assert.True(t, app.working, "an in-flight run marks the shell working")
	assert.NotNil(t, app.traceCh, "the controller's channel becomes the active trace channel")

	app.startRun(context.Background(), "second question")

	assert.Equal(t, uint64(1), app.runID, "a submit while working does not start a new run")
	assert.True(t, app.working)

	// The controller was driven exactly once, for the first query only; the
	// second submit was dropped while a run was in flight.
	ctrl.AssertExpectations(t)
	ctrl.AssertCalled(t, "Start", mock.Anything, "first question", uint64(1))
	ctrl.AssertNotCalled(t, "Start", mock.Anything, "second question", mock.Anything)
}

func TestStartRunWithoutControllerStaysIdle(t *testing.T) {
	t.Parallel()

	app := newApp(newFakeScreen(80, 24), RunOptions{
		Trace:      nil,
		Controller: nil,
		Title:      defaultTitle,
	})

	app.startRun(context.Background(), "question")

	assert.Equal(t, uint64(0), app.runID, "a nil controller cannot start a run")
	assert.False(t, app.working)
	assert.Nil(t, app.traceCh)
}

func TestLoopQuitsOnCtrlC(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))

	app := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})
	err := app.loop(context.Background())

	require.NoError(t, err)
	assert.GreaterOrEqual(t, screen.showCount(), 1, "the loop drew at least one frame before quitting")
	assert.Contains(t, screen.contents(), defaultTitle, "the rendered screen shows the chat title")
}

func TestLoopQuitsOnEscape(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	screen.inject(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))

	app := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})

	require.NoError(t, app.loop(context.Background()))
}

func TestLoopQuitsOnQWhenComposerEmpty(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	screen.inject(runeKey("q"))

	app := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})

	require.NoError(t, app.loop(context.Background()))
}

func TestLoopTreatsQAsTextWhenComposerHasContent(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	// Type "h", then "q": with a non-empty composer "q" is literal text, not a
	// quit key. Ctrl-C then terminates the loop.
	screen.inject(runeKey("h"))
	screen.inject(runeKey("q"))
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))

	app := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})
	require.NoError(t, app.loop(context.Background()))

	assert.False(t, app.chat.composerEmpty())
	assert.Equal(t, "hq", app.chat.composer.TextValue(), "q was inserted as text, not consumed as a quit key")
}

func TestLoopReturnsOnContextCancel(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	app := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})

	require.NoError(t, app.loop(ctx))
}

func TestLoopQuitsWhenEventChannelCloses(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	close(screen.events)

	app := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})

	// A nil event (closed channel yields the zero Event) terminates the loop.
	require.NoError(t, app.loop(context.Background()))
}

func TestLoopHandlesResizeWithoutPanicThenQuits(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	screen.inject(tcell.NewEventResize(120, 40))
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))

	app := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})

	require.NotPanics(t, func() {
		require.NoError(t, app.loop(context.Background()))
	})
	assert.GreaterOrEqual(t, screen.syncCount(), 1, "resize re-synchronizes the screen")
	assert.GreaterOrEqual(t, screen.showCount(), 2, "the loop redraws after the resize")
}

func TestLoopIgnoresUnknownKeyAndKeepsRunning(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	// F1 is not mapped by NewKeyEvent; the loop must ignore it and keep going
	// until Ctrl-C arrives.
	screen.inject(tcell.NewEventKey(tcell.KeyF1, "", tcell.ModNone))
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))

	app := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})

	require.NoError(t, app.loop(context.Background()))
	assert.True(t, app.chat.composerEmpty())
}

func TestLoopAppliesMatchingTraceAndIgnoresStaleRunID(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	traceCh := make(chan TraceEvent)

	app := newApp(screen, RunOptions{Trace: traceCh, Controller: nil, Title: defaultTitle})
	require.Equal(t, uint64(0), app.runID)

	done := make(chan error, 1)
	go func() { done <- app.loop(context.Background()) }()

	// Matching RunID (0): a final event lands in the trace pane and a usage event
	// tallies into the cost pane.
	sendTrace(t, traceCh, traceEvent(TraceKindFinal, "answer", 0, 0, 0, 0))
	sendTrace(t, traceCh, traceEvent(TraceKindUsage, "tally", 4, 10, 1_500_000, 0))

	// Stale RunID: both events must be dropped by the loop's run gating.
	sendTrace(t, traceCh, traceEvent(TraceKindFinal, "stale", 0, 0, 0, 99))
	sendTrace(t, traceCh, traceEvent(TraceKindUsage, "stale", 7, 8, 9_000_000, 99))

	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))
	require.NoError(t, awaitLoop(t, done))

	// Trace pane reflects only the matching final event.
	require.Len(t, traceLines(app), 1)
	assert.Equal(t, "[final] answer", traceLines(app)[0])

	// Cost pane reflects only the matching usage event; its input and output token
	// counts route to their own columns.
	assert.Equal(t, 4, app.cost.tokensIn)
	assert.Equal(t, 10, app.cost.tokensOut)
	assert.Equal(t, int64(1_500_000), app.cost.costMicros)
	assert.Equal(t, "$1.5000", app.cost.dollars())

	// The drawn frame surfaces the trace and cost content.
	contents := screen.contents()
	assert.Contains(t, contents, "answer")
	assert.Contains(t, contents, "$1.5000")
}

func TestLoopContinuesAfterTraceChannelCloses(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	traceCh := make(chan TraceEvent)
	close(traceCh)

	ctx, cancel := context.WithCancel(context.Background())

	app := newApp(screen, RunOptions{Trace: traceCh, Controller: nil, Title: defaultTitle})

	done := make(chan error, 1)
	go func() { done <- app.loop(ctx) }()

	// The closed trace channel must be detached (not spin the loop) so the loop
	// keeps running until the context is canceled.
	cancel()
	require.NoError(t, awaitLoop(t, done))
}

func TestApplyTraceRoutesUsageToCostAndRestToTrace(t *testing.T) {
	t.Parallel()

	app := newApp(newFakeScreen(80, 24), RunOptions{
		Trace:      nil,
		Controller: nil,
		Title:      defaultTitle,
	})

	app.applyTrace(traceEvent(TraceKindTurn, "thinking", 0, 0, 0, 0))
	app.applyTrace(traceEvent(TraceKindUsage, "meter", 2, 5, 250_000, 0))

	require.Len(t, traceLines(app), 1, "non-usage events append to the trace pane")
	assert.Equal(t, "[turn] thinking", traceLines(app)[0])

	assert.Equal(t, 2, app.cost.tokensIn, "usage events tally input tokens into the cost pane")
	assert.Equal(t, 5, app.cost.tokensOut, "usage events tally output tokens into the cost pane")
	assert.Equal(t, int64(250_000), app.cost.costMicros)
}

// TestAppRendersThreePanesAndQuits drives the full shell headlessly through a
// recording tcell.Screen: it feeds one TraceEvent, confirms the chat, trace,
// and cost panes all render non-empty content into a single frame, and that a
// quit key cleanly returns the run loop without panicking or leaking the loop
// goroutine.
func TestAppRendersThreePanesAndQuits(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(100, 32)
	traceCh := make(chan TraceEvent)

	app := newApp(screen, RunOptions{Trace: traceCh, Controller: nil, Title: defaultTitle})

	done := make(chan error, 1)
	go func() { done <- app.loop(context.Background()) }()

	// One live event flows through the channel into the trace pane.
	sendTrace(t, traceCh, traceEvent(TraceKindFinal, "final answer", 5, 7, 2_000_000, 0))

	// A quit key must terminate the loop with no error and no panic.
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))
	require.NoError(t, awaitLoop(t, done), "the run loop returns cleanly on quit")

	contents := screen.contents()
	require.NotEmpty(t, strings.TrimSpace(contents), "the shell rendered a non-empty frame")

	// Chat pane: bordered title plus the welcome body.
	assert.Contains(t, contents, defaultTitle, "chat pane renders its title")
	assert.Contains(t, contents, "Type a message", "chat pane renders the welcome body")
	// Trace pane: box title plus the fed event.
	assert.Contains(t, contents, "Trace", "trace pane renders its box title")
	assert.Contains(t, contents, "final answer", "trace pane renders the fed event")
	// Cost pane: the metric table headers.
	assert.Contains(t, contents, "Metric", "cost pane renders its table header")
	assert.Contains(t, contents, "Value", "cost pane renders its value column")

	// awaitLoop above already proved the loop goroutine returned, so nothing leaks.
	assert.GreaterOrEqual(t, screen.showCount(), 1, "the shell flushed at least one frame")
}
