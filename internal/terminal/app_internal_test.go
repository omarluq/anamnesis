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

	app := newTestApp()

	assert.Equal(t, defaultTitle, app.footerTitle())
	assert.Empty(t, app.spinnerGlyph())

	// A thinking event marks the loop working, so the footer gains the spinner glyph.
	app.applyTrace(traceEvent(TraceKindThinking, "thinking", 0))
	app.spinnerFrame = 0
	assert.Equal(t, defaultTitle+" "+string(spinnerFrames[0]), app.footerTitle())
	assert.NotEmpty(t, app.spinnerGlyph())

	// A final event clears the working state and retires the spinner.
	app.applyTrace(traceEvent(TraceKindFinal, "done", 0))
	assert.Equal(t, defaultTitle, app.footerTitle())
	assert.Empty(t, app.spinnerGlyph())
}

func TestApplyTraceBuildsTranscriptAcrossKinds(t *testing.T) {
	t.Parallel()

	app := newTestApp()

	app.applyTrace(traceDepthEvent(TraceKindThinking, "planning the search", 0))
	app.applyTrace(traceDepthEvent(TraceKindQueryStart, "summarize the backtrace", 1))

	require.Len(t, app.history, 2)
	pending := app.history[1]
	assert.Equal(t, transcript.RoleToolResult, pending.Role, "a query start opens a tool-result block")
	assert.True(t, pending.Pending, "the query block is pending until its end event")

	app.applyTrace(traceDepthEvent(TraceKindQueryEnd, "the i915 driver oopsed", 1))
	assert.False(t, app.history[1].Pending, "the query end settles the pending block")

	app.applyTrace(traceEvent(TraceKindFinal, "**root cause:** firmware", 0))

	assert.Equal(t,
		[]transcript.Role{transcript.RoleThinking, transcript.RoleToolResult, transcript.RoleAssistant},
		historyRoles(app),
		"the transcript holds the thinking, query, and assistant messages in order")
}

func TestApplyTraceStreamsThinkingDeltasThenSettles(t *testing.T) {
	t.Parallel()

	app := newTestApp()

	// Reasoning streams in as deltas, growing one pending thinking block in place.
	app.applyTrace(traceEvent(TraceKindThinkingDelta, "Listing the ", 0))
	app.applyTrace(traceEvent(TraceKindThinkingDelta, "recent boots.", 0))

	require.Len(t, app.history, 1, "deltas grow a single block, not one block per delta")
	require.Equal(t, transcript.RoleThinking, app.history[0].Role)
	require.True(t, app.history[0].Pending, "the thinking block stays pending while streaming")
	require.True(t, app.working, "streaming thinking marks the shell working")
	assert.Equal(t, "Listing the recent boots.", app.history[0].Content, "deltas concatenate")

	// The authoritative final thinking settles the streamed block in place — no duplicate.
	app.applyTrace(traceEvent(TraceKindThinking, "Listed the recent boots to find the failure.", 0))

	require.Len(t, app.history, 1, "the final thinking settles the pending block rather than appending a duplicate")
	assert.False(t, app.history[0].Pending, "the block is settled")
	assert.Equal(t, "Listed the recent boots to find the failure.", app.history[0].Content,
		"the authoritative summary replaces the streamed text")
}

func TestApplyTraceThinkingWithoutDeltasAppendsFreshBlock(t *testing.T) {
	t.Parallel()

	app := newTestApp()

	// No deltas streamed (the model returned no reasoning summary) → the terse fallback
	// thinking appends as a fresh, already-settled block.
	app.applyTrace(traceEvent(TraceKindThinking, "terse fallback rationale", 0))

	require.Len(t, app.history, 1)
	assert.Equal(t, transcript.RoleThinking, app.history[0].Role)
	assert.False(t, app.history[0].Pending, "a non-streamed thinking block is settled immediately")
	assert.Equal(t, "terse fallback rationale", app.history[0].Content)
}

func TestApplyTraceCodeEventsRenderAsToolBlock(t *testing.T) {
	t.Parallel()

	app := newTestApp()

	code := "boots := journal.Boots()\nfmt.Println(len(boots))"
	app.applyTrace(traceEvent(TraceKindCodeStart, code, 0))

	require.Len(t, app.history, 1)
	require.True(t, app.working, "a code start marks the shell working")
	require.True(t, app.history[0].Pending, "a code start opens a pending block")
	assert.Equal(t, transcript.RoleBashExecution, app.history[0].Role, "a code block is a bash-execution message")

	app.applyTrace(traceEvent(TraceKindCodeEnd, "3", 0))

	require.Len(t, app.history, 1, "the code end settles the block in place rather than appending a new one")
	assert.False(t, app.history[0].Pending, "the code end settles the pending block")

	parsed := parseQueryContent(app.history[0].Content)
	assert.Equal(t, codeName, parsed.Name, "the settled block is labeled as a code block")
	assert.Contains(t, parsed.Args, "journal.Boots()", "the block carries the turn's Go source")
	assert.Equal(t, "3", parsed.Output, "the block carries the captured output")

	// A code block renders through the same tool-block path as a query block.
	app.toolsExpanded = true
	lines := app.renderMessage(80, app.history[0])
	assert.NotEmpty(t, lines, "a code block renders through the tool-block path")
}

func TestApplyTraceCodeErrorRendersRedToolBlock(t *testing.T) {
	t.Parallel()

	app := newTestApp()

	app.applyTrace(traceEvent(TraceKindCodeStart, "journal.Query(\"bad syntax\")", 0))
	require.True(t, app.history[0].Pending, "a code start opens a pending block")

	// A CodeEnd carrying an Err settles the block, routing the failure into its
	// error: section rather than folding it into the output text.
	codeErr := "syntax error: unexpected EOF"
	app.applyTrace(TraceEvent{
		Kind:    TraceKindCodeEnd,
		Text:    "partial stdout",
		Err:     codeErr,
		Depth:   0,
		RunID:   0,
		QueryID: 0,
	})

	require.Len(t, app.history, 1, "the code end settles the block in place")
	require.False(t, app.history[0].Pending, "the code end settles the pending block")

	parsed := parseQueryContent(app.history[0].Content)
	assert.Equal(t, codeErr, parsed.Error, "the error text routes into the block's error section")

	// A non-empty error section paints the block red through the shared tool-block
	// style path that query blocks and code blocks both render through.
	style := queryBlockStyle(app.theme, app.history[0], parsed)
	assert.Equal(t, app.theme.bg(app.theme.ToolErrorBg), style, "an errored code block paints red")
}

func TestApplyTraceFinalAppendsAssistantAndClearsSpinner(t *testing.T) {
	t.Parallel()

	app := newTestApp()

	app.applyTrace(traceEvent(TraceKindThinking, "investigating", 0))
	require.True(t, app.working)

	app.applyTrace(traceEvent(TraceKindFinal, "**root cause:** disk full", 0))

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

	app.applyTrace(traceEvent(TraceKindQueryStart, "query start", 0))
	require.True(t, app.working, "a query start marks the shell working")

	app.applyTrace(traceEvent(TraceKindQueryEnd, "query end", 0))
	assert.True(t, app.working, "a query end is mid-turn and should not clear the spinner")

	app.applyTrace(traceEvent(TraceKindFinal, "done", 0))
	assert.False(t, app.working, "the final answer clears the working state")
}

// TestApplyTraceQueryEndsCorrelateByQueryID is the fan-out correlation regression:
// two sibling sub-calls open at the same depth, then their ends arrive in completion
// order (A then B). Matching by QueryID settles A's result onto A's own block — the
// OLDER pending block — rather than onto the newest pending block at the depth, which
// is the wrong-pairing bug the depth-and-recency rule produced for parallel fan-out.
func TestApplyTraceQueryEndsCorrelateByQueryID(t *testing.T) {
	t.Parallel()

	app := newTestApp()

	// Sibling sub-calls fan out at one depth, each carrying its own correlation id.
	queryEvent := func(kind TraceKind, text string, queryID uint64) TraceEvent {
		return TraceEvent{Kind: kind, Text: text, Err: "", Depth: 1, RunID: 0, QueryID: queryID}
	}

	// Two sibling sub-calls fan out at the same depth, B opened most recently.
	app.applyTrace(queryEvent(TraceKindQueryStart, "prompt A", 1))
	app.applyTrace(queryEvent(TraceKindQueryStart, "prompt B", 2))
	require.Len(t, app.history, 2)

	// A's end arrives first while B is still the newest pending block at the depth.
	// The old depth-and-recency rule would settle B here; the QueryID match settles A.
	app.applyTrace(queryEvent(TraceKindQueryEnd, "answer A", 1))

	blockA, blockB := app.history[0], app.history[1]
	require.False(t, blockA.Pending, "A's end settles A even though B is the newer pending block")
	assert.True(t, blockB.Pending, "B stays pending until its own end arrives")

	parsedA := parseQueryContent(blockA.Content)
	assert.Equal(t, "prompt A", parsedA.Args, "block A keeps its own prompt")
	assert.Equal(t, "answer A", parsedA.Output, "A's result lands on A's block, not the newest pending block")

	// B's end then settles B onto its own prompt.
	app.applyTrace(queryEvent(TraceKindQueryEnd, "answer B", 2))

	parsedB := parseQueryContent(app.history[1].Content)
	require.False(t, app.history[1].Pending)
	assert.Equal(t, "prompt B", parsedB.Args)
	assert.Equal(t, "answer B", parsedB.Output, "B's result lands on B's block")
}

// TestApplyTraceJudgeApprovalRendersGreen drives the §16 judge block through an
// approval: a JudgeStart opens a pending agent.Judge block, and an empty JudgeEnd
// settles it to the standing approved line, painted with the green success
// background a settled query shares.
func TestApplyTraceJudgeApprovalRendersGreen(t *testing.T) {
	t.Parallel()

	app := newTestApp()

	app.applyTrace(traceEvent(TraceKindJudgeStart, "the resolved answer", 0))

	require.Len(t, app.history, 1)
	pending := app.history[0]
	assert.Equal(t, transcript.RoleToolResult, pending.Role, "a judge start opens a tool-result block")
	require.True(t, pending.Pending, "the judge block is pending until its end")
	assert.True(t, app.working, "a judge start marks the shell working")
	assert.Equal(t, judgeName, parseQueryContent(pending.Content).Name, "the block is labeled agent.Judge")

	app.applyTrace(traceEvent(TraceKindJudgeEnd, "", 0))

	settled := app.history[0]
	require.False(t, settled.Pending, "an empty critique settles the judge block")
	parsed := parseQueryContent(settled.Content)
	assert.Equal(t, judgeApprovedOutput, parsed.Output, "approval renders the standing approved line")
	assert.Equal(t, app.theme.bg(app.theme.ToolSuccessBg), queryBlockStyle(app.theme, settled, parsed),
		"an approving judge block paints green")
}

// TestApplyTraceJudgeCritiqueRendersAmberNotRed proves a judge critique settles the
// block to the critique text in amber — a revision directive, distinct from the red
// of an outright failure.
func TestApplyTraceJudgeCritiqueRendersAmberNotRed(t *testing.T) {
	t.Parallel()

	app := newTestApp()

	app.applyTrace(traceEvent(TraceKindJudgeStart, "the resolved answer", 0))
	app.applyTrace(traceEvent(TraceKindJudgeEnd, "cite the boot the panic came from", 0))

	settled := app.history[0]
	require.False(t, settled.Pending)
	parsed := parseQueryContent(settled.Content)
	assert.Equal(t, "cite the boot the panic came from", parsed.Output, "the critique text settles the block")

	style := queryBlockStyle(app.theme, settled, parsed)
	assert.Equal(t, app.theme.bg(app.theme.ToolReviseBg), style, "a critique paints amber, a revision directive")
	assert.NotEqual(t, app.theme.bg(app.theme.ToolErrorBg), style, "a critique is not a failure, so never red")
}

func TestQueryToggleRevealsContentWithThinkingAlwaysShown(t *testing.T) {
	t.Parallel()

	app := newTestApp()

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
		Return(scriptedTrace(1, traceEvent(TraceKindThinking, "looking", 0))).
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

	app := newTestApp()

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

	done := startLoop(app)

	submitQuery(screen, "why did it crash")
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))
	require.NoError(t, awaitLoop(t, done))

	ctrl.AssertExpectations(t)
	require.Len(t, capturedCtx, 1, "the controller run received a per-run context")
	runCtx := <-capturedCtx
	require.ErrorIs(t, runCtx.Err(), context.Canceled,
		"the run's child context is canceled when the loop exits on the quit-key path")
}

func TestLoopQuitsOnKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		event tcell.Event
		name  string
	}{
		{event: tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone), name: "ctrl+c"},
		{event: tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone), name: "escape"},
		{event: runeKey("q"), name: "q on an empty composer"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			screen := newFakeScreen(80, 24)
			screen.inject(test.event)

			app := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})
			require.NoError(t, app.loop(context.Background()))

			assert.GreaterOrEqual(t, screen.showCount(), 1, "the loop drew at least one frame before quitting")
			assert.Contains(t, screen.contents(), defaultTitle, "the rendered footer shows the title")
		})
	}
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
	assert.Equal(t, "hq", app.composer.Text, "q was inserted as text, not consumed as a quit key")
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

	done := startLoop(app)

	// Matching RunID (0): the final answer appends to the transcript.
	sendTrace(t, traceCh, traceEvent(TraceKindFinal, "answer", 0))

	// Stale RunID: the event must be dropped by the loop's run gating.
	sendTrace(t, traceCh, traceEvent(TraceKindFinal, "stale", 99))

	awaitContents(t, screen, "answer")
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))
	require.NoError(t, awaitLoop(t, done))

	require.Len(t, app.history, 1, "only the matching final event appended a message")

	contents := screen.contents()
	assert.Contains(t, contents, "answer")
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

	done := startLoop(app)

	sendTrace(t, traceCh, traceEvent(TraceKindFinal, "final answer", 0))
	awaitContents(t, screen, "final answer")

	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))
	require.NoError(t, awaitLoop(t, done), "the run loop returns cleanly on quit")

	contents := screen.contents()
	require.NotEmpty(t, strings.TrimSpace(contents))

	// Transcript: the appended assistant answer.
	assert.Contains(t, contents, "final answer", "the transcript renders the assistant answer")
	// Footer: the title and the key hints — no side panes, no usage totals.
	footer := screenRow(t, contents, "anamnesis")
	assert.Contains(t, footer, "ctrl+o expand", "the footer renders the key hints")
	assert.NotContains(t, contents, "Trace", "no trace pane is rendered")
	assert.NotContains(t, contents, "Metric", "no cost pane is rendered")
}
