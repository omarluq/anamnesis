package terminal

import (
	"context"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/transcript"
)

func TestNewAppDefaultsTitleAndCollapsedState(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)

	app := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: ""})
	require.NotNil(t, app)
	assert.Equal(t, defaultTitle, app.title)
	assert.Empty(t, app.history, "the transcript starts empty")
	assert.True(t, app.composer.Empty(), "the composer starts empty")
	assert.False(t, app.toolsExpanded, "query blocks start unexpanded")

	custom := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: "custom"})
	assert.Equal(t, "custom", custom.title)
}

func TestFooterTitleAppendsSpinnerOnlyWhileWorking(t *testing.T) {
	t.Parallel()

	app := newApp(newFakeScreen(80, 24), RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})

	assert.Equal(t, defaultTitle, app.footerTitle())
	assert.Empty(t, app.spinnerGlyph())

	// A thinking event marks the loop working, so the footer gains the spinner glyph.
	app.applyTrace(traceEvent(TraceKindThinking, "thinking", 0, 0, 0, 0))
	app.spinnerFrame = 0
	assert.Equal(t, defaultTitle+" "+string(spinnerFrames[0]), app.footerTitle())
	assert.NotEmpty(t, app.spinnerGlyph())

	// A final event clears the working state and retires the spinner.
	app.applyTrace(traceEvent(TraceKindFinal, "done", 0, 0, 0, 0))
	assert.Equal(t, defaultTitle, app.footerTitle())
	assert.Empty(t, app.spinnerGlyph())
}

func TestApplyTraceBuildsTranscriptAcrossKinds(t *testing.T) {
	t.Parallel()

	app := newApp(newFakeScreen(80, 24), RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})

	app.applyTrace(traceDepthEvent(TraceKindThinking, "planning the search", 0))
	app.applyTrace(traceDepthEvent(TraceKindQueryStart, "summarize the backtrace", 1))

	require.Len(t, app.history, 2)
	pending := app.history[1]
	assert.Equal(t, transcript.RoleToolResult, pending.Role, "a query start opens a tool-result block")
	assert.True(t, pending.Pending, "the query block is pending until its end event")
	assert.Equal(t, 1, pending.Depth, "the query block records its recursion depth")

	app.applyTrace(traceDepthEvent(TraceKindQueryEnd, "the i915 driver oopsed", 1))
	assert.False(t, app.history[1].Pending, "the query end settles the pending block")

	app.applyTrace(traceEvent(TraceKindFinal, "**root cause:** firmware", 0, 0, 0, 0))

	assert.Equal(t,
		[]transcript.Role{transcript.RoleThinking, transcript.RoleToolResult, transcript.RoleAssistant},
		historyRoles(app),
		"the transcript holds the thinking, query, and assistant messages in order")
}

func TestApplyTraceUsageAccumulatesIntoFooterTotals(t *testing.T) {
	t.Parallel()

	app := newApp(newFakeScreen(80, 24), RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})

	app.applyTrace(traceEvent(TraceKindUsage, "", 2, 5, 250_000, 0))
	app.applyTrace(traceEvent(TraceKindUsage, "", 4, 1, 250_000, 0))

	assert.Equal(t, 6, app.tokensIn, "usage events tally input tokens")
	assert.Equal(t, 6, app.tokensOut, "usage events tally output tokens")
	assert.Equal(t, "$0.5000", app.dollars(), "usage events tally cost into the footer")
	assert.Empty(t, app.history, "usage events never append a transcript message")
}

func TestApplyTraceFinalAppendsAssistantAndClearsSpinner(t *testing.T) {
	t.Parallel()

	app := newApp(newFakeScreen(80, 24), RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})

	app.applyTrace(traceEvent(TraceKindThinking, "investigating", 0, 0, 0, 0))
	require.True(t, app.working)

	app.applyTrace(traceEvent(TraceKindFinal, "**root cause:** disk full", 0, 0, 0, 0))

	assert.False(t, app.working, "a final answer clears the working state")
	assert.Equal(t, transcript.RoleAssistant, app.history[len(app.history)-1].Role)
	assert.Contains(t, transcriptText(app, 70), "root cause:", "the final answer markdown lands in the transcript")
}

// TestApplyTraceQueryEventsKeepWorkingUntilFinal pins the spinner semantics of the
// query lifecycle: a query start marks the loop working, a query end arrives
// mid-turn and must leave the spinner running, and only the final answer clears it.
func TestApplyTraceQueryEventsKeepWorkingUntilFinal(t *testing.T) {
	t.Parallel()

	app := newApp(newFakeScreen(80, 24), RunOptions{
		Trace:      nil,
		Controller: nil,
		Title:      defaultTitle,
	})

	app.applyTrace(traceEvent(TraceKindQueryStart, "query start", 0, 0, 0, 0))
	require.True(t, app.working, "a query start marks the shell working")

	app.applyTrace(traceEvent(TraceKindQueryEnd, "query end", 0, 0, 0, 0))
	assert.True(t, app.working, "a query end is mid-turn and should not clear the spinner")

	app.applyTrace(traceEvent(TraceKindFinal, "done", 0, 0, 0, 0))
	assert.False(t, app.working, "the final answer clears the working state")
}

func TestQueryToggleRevealsContentWithThinkingAlwaysShown(t *testing.T) {
	t.Parallel()

	app := newApp(newFakeScreen(80, 24), RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})

	app.applyTrace(traceDepthEvent(TraceKindThinking, "weighing the boots", 0))
	app.applyTrace(traceDepthEvent(TraceKindQueryStart, "inspect boot 3", 1))
	app.applyTrace(traceDepthEvent(TraceKindQueryEnd, "boot 3 panicked", 1))

	collapsed := transcriptText(app, 80)
	assert.Contains(t, collapsed, "weighing the boots", "thinking is always shown in full, never collapsed")
	assert.Contains(t, collapsed, "agent.Query", "the query block header always shows")
	assert.Contains(t, collapsed, "boot 3 panicked", "the collapsed query block previews its output")
	assert.NotContains(t, collapsed, labelArgs+":", "a collapsed query block hides its args section")

	require.True(t, toggleKey(app, "ctrl+o"), "ctrl+o toggles query expansion")

	expanded := transcriptText(app, 80)
	assert.Contains(t, expanded, "weighing the boots", "thinking stays visible after the query toggle")
	assert.Contains(t, expanded, labelArgs+":", "an expanded query block shows its args section")
	assert.Contains(t, expanded, labelOutput+":", "an expanded query block shows its output section")
}

func TestStartRunBeginsControllerRunAndIgnoresSubmitWhileWorking(t *testing.T) {
	t.Parallel()

	ctrl := new(mockController)
	ctrl.On("Start", mock.Anything, "first question", uint64(1)).
		Return(scriptedTrace(1, traceEvent(TraceKindThinking, "looking", 0, 0, 0, 0))).
		Once()

	app := newApp(newFakeScreen(80, 24), RunOptions{Trace: nil, Controller: ctrl, Title: defaultTitle})

	require.Equal(t, uint64(0), app.runID)
	require.False(t, app.working)

	app.startRun(context.Background(), "first question")

	assert.Equal(t, uint64(1), app.runID, "starting a run bumps the run id")
	assert.True(t, app.working, "an in-flight run marks the shell working")
	assert.NotNil(t, app.traceCh, "the controller's channel becomes the active trace channel")

	app.startRun(context.Background(), "second question")

	assert.Equal(t, uint64(1), app.runID, "a submit while working does not start a new run")

	ctrl.AssertExpectations(t)
	ctrl.AssertNotCalled(t, "Start", mock.Anything, "second question", mock.Anything)
}

func TestStartRunWithoutControllerStaysIdle(t *testing.T) {
	t.Parallel()

	app := newApp(newFakeScreen(80, 24), RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})

	app.startRun(context.Background(), "question")

	assert.Equal(t, uint64(0), app.runID, "a nil controller cannot start a run")
	assert.False(t, app.working)
	assert.Nil(t, app.traceCh)
}

func TestStartRunCancelsRunContextOnLoopExit(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)

	var stream <-chan TraceEvent = make(chan TraceEvent)

	capturedCtx := make(chan context.Context, 1)

	ctrl := new(mockController)
	ctrl.On("Start", mock.Anything, "why did it crash", uint64(1)).
		Run(func(args mock.Arguments) {
			if runCtx, ok := args.Get(0).(context.Context); ok {
				capturedCtx <- runCtx
			}
		}).
		Return(stream).
		Once()

	app := newApp(screen, RunOptions{Trace: nil, Controller: ctrl, Title: defaultTitle})

	done := make(chan error, 1)
	go func() { done <- app.loop(context.Background()) }()

	submitQuery(screen, "why did it crash")
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))
	require.NoError(t, awaitLoop(t, done))

	ctrl.AssertExpectations(t)
	require.Len(t, capturedCtx, 1, "the controller run received a per-run context")
	runCtx := <-capturedCtx
	require.ErrorIs(t, runCtx.Err(), context.Canceled,
		"the run's child context is canceled when the loop exits on the quit-key path")
}

func TestLoopQuitsOnCtrlC(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))

	app := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})
	require.NoError(t, app.loop(context.Background()))

	assert.GreaterOrEqual(t, screen.showCount(), 1, "the loop drew at least one frame before quitting")
	assert.Contains(t, screen.contents(), defaultTitle, "the rendered footer shows the title")
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
	screen.inject(runeKey("h"))
	screen.inject(runeKey("q"))
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))

	app := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})
	require.NoError(t, app.loop(context.Background()))

	assert.False(t, app.composer.Empty())
	assert.Equal(t, "hq", app.composer.TextValue(), "q was inserted as text, not consumed as a quit key")
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
	screen.inject(tcell.NewEventKey(tcell.KeyF1, "", tcell.ModNone))
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))

	app := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})

	require.NoError(t, app.loop(context.Background()))
	assert.True(t, app.composer.Empty())
}

func TestLoopAppliesMatchingTraceAndIgnoresStaleRunID(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	traceCh := make(chan TraceEvent)

	app := newApp(screen, RunOptions{Trace: traceCh, Controller: nil, Title: defaultTitle})
	require.Equal(t, uint64(0), app.runID)

	done := make(chan error, 1)
	go func() { done <- app.loop(context.Background()) }()

	// Matching RunID (0): the final answer appends and the usage event tallies.
	sendTrace(t, traceCh, traceEvent(TraceKindFinal, "answer", 0, 0, 0, 0))
	sendTrace(t, traceCh, traceEvent(TraceKindUsage, "", 4, 10, 1_500_000, 0))

	// Stale RunID: both events must be dropped by the loop's run gating.
	sendTrace(t, traceCh, traceEvent(TraceKindFinal, "stale", 0, 0, 0, 99))
	sendTrace(t, traceCh, traceEvent(TraceKindUsage, "", 7, 8, 9_000_000, 99))

	awaitContents(t, screen, "$1.5000")
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))
	require.NoError(t, awaitLoop(t, done))

	require.Len(t, app.history, 1, "only the matching final event appended a message")
	assert.Equal(t, 4, app.tokensIn)
	assert.Equal(t, 10, app.tokensOut)
	assert.Equal(t, "$1.5000", app.dollars())

	contents := screen.contents()
	assert.Contains(t, contents, "answer")
	assert.Contains(t, contents, "$1.5000")
	assert.NotContains(t, contents, "stale", "the stale-RunID answer never rendered")
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

	cancel()
	require.NoError(t, awaitLoop(t, done))
}

func TestAppRendersTranscriptComposerFooterAndQuits(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(100, 32)
	traceCh := make(chan TraceEvent)

	app := newApp(screen, RunOptions{Trace: traceCh, Controller: nil, Title: defaultTitle})

	done := make(chan error, 1)
	go func() { done <- app.loop(context.Background()) }()

	sendTrace(t, traceCh, traceEvent(TraceKindFinal, "final answer", 0, 0, 0, 0))
	sendTrace(t, traceCh, traceEvent(TraceKindUsage, "", 5, 7, 2_000_000, 0))
	awaitContents(t, screen, "$2.0000")

	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))
	require.NoError(t, awaitLoop(t, done), "the run loop returns cleanly on quit")

	contents := screen.contents()
	require.NotEmpty(t, strings.TrimSpace(contents))

	// Transcript: the appended assistant answer.
	assert.Contains(t, contents, "final answer", "the transcript renders the assistant answer")
	// Footer: the title, the usage totals, and the key hints — no side panes.
	footer := screenRow(t, contents, "anamnesis")
	assert.Contains(t, footer, "$2.0000", "the footer renders the accumulated cost")
	assert.Contains(t, footer, "ctrl+t thinking", "the footer renders the key hints")
	assert.NotContains(t, contents, "Trace", "no trace pane is rendered")
	assert.NotContains(t, contents, "Metric", "no cost pane is rendered")
}
