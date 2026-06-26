package terminal_test

import (
	"context"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/terminal"
)

func TestNewAppDefaultsTitleAndWiresPanes(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)

	app := terminal.NewApp(screen, terminal.RunOptions{Trace: nil, Title: ""})
	require.NotNil(t, app)
	assert.Equal(t, terminal.DefaultTitle, app.Title())
	assert.True(t, app.PanesReady())

	custom := terminal.NewApp(screen, terminal.RunOptions{Trace: nil, Title: "custom"})
	assert.Equal(t, "custom", custom.Title())
}

func TestHeaderTitleAppendsSpinnerOnlyWhileWorking(t *testing.T) {
	t.Parallel()

	app := terminal.NewApp(newFakeScreen(80, 24), terminal.RunOptions{Trace: nil, Title: terminal.DefaultTitle})

	assert.Equal(t, terminal.DefaultTitle, app.HeaderTitle())
	assert.Empty(t, app.SpinnerGlyph())

	app.SetWorking(true)
	app.SetSpinnerFrame(0)
	assert.Equal(t, terminal.DefaultTitle+" "+string(terminal.SpinnerFrames[0]), app.HeaderTitle())
	assert.NotEmpty(t, app.SpinnerGlyph())
}

func TestLoopQuitsOnCtrlC(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))

	app := terminal.NewApp(screen, terminal.RunOptions{Trace: nil, Title: terminal.DefaultTitle})
	err := app.Loop(context.Background())

	require.NoError(t, err)
	assert.GreaterOrEqual(t, screen.showCount(), 1, "the loop drew at least one frame before quitting")
	assert.Contains(t, screen.contents(), terminal.DefaultTitle, "the rendered screen shows the chat title")
}

func TestLoopQuitsOnEscape(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	screen.inject(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))

	app := terminal.NewApp(screen, terminal.RunOptions{Trace: nil, Title: terminal.DefaultTitle})

	require.NoError(t, app.Loop(context.Background()))
}

func TestLoopQuitsOnQWhenComposerEmpty(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	screen.inject(runeKey("q"))

	app := terminal.NewApp(screen, terminal.RunOptions{Trace: nil, Title: terminal.DefaultTitle})

	require.NoError(t, app.Loop(context.Background()))
}

func TestLoopTreatsQAsTextWhenComposerHasContent(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	// Type "h", then "q": with a non-empty composer "q" is literal text, not a
	// quit key. Ctrl-C then terminates the loop.
	screen.inject(runeKey("h"))
	screen.inject(runeKey("q"))
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))

	app := terminal.NewApp(screen, terminal.RunOptions{Trace: nil, Title: terminal.DefaultTitle})
	require.NoError(t, app.Loop(context.Background()))

	assert.False(t, app.ComposerEmpty())
	assert.Equal(t, "hq", app.ComposerText(), "q was inserted as text, not consumed as a quit key")
}

func TestLoopReturnsOnContextCancel(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	app := terminal.NewApp(screen, terminal.RunOptions{Trace: nil, Title: terminal.DefaultTitle})

	require.NoError(t, app.Loop(ctx))
}

func TestLoopQuitsWhenEventChannelCloses(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	close(screen.events)

	app := terminal.NewApp(screen, terminal.RunOptions{Trace: nil, Title: terminal.DefaultTitle})

	// A nil event (closed channel yields the zero Event) terminates the loop.
	require.NoError(t, app.Loop(context.Background()))
}

func TestLoopHandlesResizeWithoutPanicThenQuits(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	screen.inject(tcell.NewEventResize(120, 40))
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))

	app := terminal.NewApp(screen, terminal.RunOptions{Trace: nil, Title: terminal.DefaultTitle})

	require.NotPanics(t, func() {
		require.NoError(t, app.Loop(context.Background()))
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

	app := terminal.NewApp(screen, terminal.RunOptions{Trace: nil, Title: terminal.DefaultTitle})

	require.NoError(t, app.Loop(context.Background()))
	assert.True(t, app.ComposerEmpty())
}

func TestLoopAppliesMatchingTraceAndIgnoresStaleRunID(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	traceCh := make(chan terminal.TraceEvent)

	app := terminal.NewApp(screen, terminal.RunOptions{Trace: traceCh, Title: terminal.DefaultTitle})
	require.Equal(t, uint64(0), app.RunID())

	done := make(chan error, 1)
	go func() { done <- app.Loop(context.Background()) }()

	// Matching RunID (0): a final event lands in the trace pane and a usage event
	// tallies into the cost pane.
	sendTrace(t, traceCh, traceEvent(terminal.TraceKindFinal, "answer", 0, 0, 0))
	sendTrace(t, traceCh, traceEvent(terminal.TraceKindUsage, "tally", 10, 1_500_000, 0))

	// Stale RunID: both events must be dropped by the loop's run gating.
	sendTrace(t, traceCh, traceEvent(terminal.TraceKindFinal, "stale", 0, 0, 99))
	sendTrace(t, traceCh, traceEvent(terminal.TraceKindUsage, "stale", 7, 9_000_000, 99))

	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))
	require.NoError(t, awaitLoop(t, done))

	// Trace pane reflects only the matching final event.
	require.Len(t, app.TraceLines(), 1)
	assert.Equal(t, "[final] answer", app.TraceLines()[0])

	// Cost pane reflects only the matching usage event (usage routes tokens to the
	// "out" column).
	assert.Equal(t, 0, app.CostTokensIn())
	assert.Equal(t, 10, app.CostTokensOut())
	assert.Equal(t, int64(1_500_000), app.CostMicros())
	assert.Equal(t, "$1.5000", app.CostDollarText())

	// The drawn frame surfaces the trace and cost content.
	contents := screen.contents()
	assert.Contains(t, contents, "answer")
	assert.Contains(t, contents, "$1.5000")
}

func TestLoopContinuesAfterTraceChannelCloses(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	traceCh := make(chan terminal.TraceEvent)
	close(traceCh)

	ctx, cancel := context.WithCancel(context.Background())

	app := terminal.NewApp(screen, terminal.RunOptions{Trace: traceCh, Title: terminal.DefaultTitle})

	done := make(chan error, 1)
	go func() { done <- app.Loop(ctx) }()

	// The closed trace channel must be detached (not spin the loop) so the loop
	// keeps running until the context is canceled.
	cancel()
	require.NoError(t, awaitLoop(t, done))
}

func TestApplyTraceRoutesUsageToCostAndRestToTrace(t *testing.T) {
	t.Parallel()

	app := terminal.NewApp(newFakeScreen(80, 24), terminal.RunOptions{Trace: nil, Title: terminal.DefaultTitle})

	app.ApplyTrace(traceEvent(terminal.TraceKindTurn, "thinking", 0, 0, 0))
	app.ApplyTrace(traceEvent(terminal.TraceKindUsage, "meter", 5, 250_000, 0))

	require.Len(t, app.TraceLines(), 1, "non-usage events append to the trace pane")
	assert.Equal(t, "[turn] thinking", app.TraceLines()[0])

	assert.Equal(t, 5, app.CostTokensOut(), "usage events tally into the cost pane only")
	assert.Equal(t, int64(250_000), app.CostMicros())
}

// TestAppRendersThreePanesAndQuits drives the full shell headlessly through a
// recording tcell.Screen: it feeds one TraceEvent, confirms the chat, trace,
// and cost panes all render non-empty content into a single frame, and that a
// quit key cleanly returns the run loop without panicking or leaking the loop
// goroutine.
func TestAppRendersThreePanesAndQuits(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(100, 32)
	traceCh := make(chan terminal.TraceEvent)

	app := terminal.NewApp(screen, terminal.RunOptions{Trace: traceCh, Title: terminal.DefaultTitle})

	done := make(chan error, 1)
	go func() { done <- app.Loop(context.Background()) }()

	// One live event flows through the channel into the trace pane.
	sendTrace(t, traceCh, traceEvent(terminal.TraceKindFinal, "final answer", 12, 2_000_000, 0))

	// A quit key must terminate the loop with no error and no panic.
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))
	require.NoError(t, awaitLoop(t, done), "the run loop returns cleanly on quit")

	contents := screen.contents()
	require.NotEmpty(t, strings.TrimSpace(contents), "the shell rendered a non-empty frame")

	// Chat pane: bordered title plus the welcome body.
	assert.Contains(t, contents, terminal.DefaultTitle, "chat pane renders its title")
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
