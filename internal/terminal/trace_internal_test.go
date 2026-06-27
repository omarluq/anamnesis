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

func TestTraceTextFormatsEvents(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		want  string
		event TraceEvent
	}{
		{
			name:  "turn without tokens",
			want:  "[turn] thinking",
			event: traceEvent(TraceKindTurn, "thinking", 0, 0, 0, 0),
		},
		{
			name:  "sub-call without tokens",
			want:  "[sub-call] tool",
			event: traceEvent(TraceKindSubCall, "tool", 0, 0, 0, 0),
		},
		{
			name:  "final sums input and output tokens",
			want:  "[final] answer (42 tok)",
			event: traceEvent(TraceKindFinal, "answer", 30, 12, 0, 0),
		},
		{
			name:  "large token counts use thousands separators",
			want:  "[final] answer (1,234 tok)",
			event: traceEvent(TraceKindFinal, "answer", 1200, 34, 0, 0),
		},
		{
			name:  "code turn renders the generated source label",
			want:  "[code] journal.Boots()",
			event: traceEvent(TraceKindCode, "journal.Boots()", 0, 0, 0, 0),
		},
		{
			name:  "stdout renders captured output",
			want:  "[stdout] 3 boots found",
			event: traceEvent(TraceKindStdout, "3 boots found", 0, 0, 0, 0),
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, testCase.want, traceText(testCase.event))
		})
	}
}

func TestTraceTextIndentsNestedSubCallLines(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		want  string
		depth int
	}{
		{name: "top level is flush left", want: "[sub-call] summarize", depth: 0},
		{name: "depth one indents two spaces", want: "  [sub-call] summarize", depth: 1},
		{name: "depth three indents six spaces", want: "      [sub-call] summarize", depth: 3},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			event := traceDepthEvent(TraceKindSubCall, "summarize", testCase.depth)
			assert.Equal(t, testCase.want, traceText(event))
		})
	}
}

func TestTraceStyleMapsKindToPaletteColor(t *testing.T) {
	t.Parallel()

	theme := DefaultTheme()

	cases := []struct {
		name string
		kind TraceKind
		want tcell.Color
	}{
		{name: "final", kind: TraceKindFinal, want: theme.Success},
		{name: "sub-call", kind: TraceKindSubCall, want: theme.Accent},
		{name: "usage", kind: TraceKindUsage, want: theme.Warning},
		{name: "code", kind: TraceKindCode, want: theme.Dim},
		{name: "stdout", kind: TraceKindStdout, want: theme.Muted},
		{name: "turn", kind: TraceKindTurn, want: theme.Text},
		{name: "unknown", kind: TraceKind("mystery"), want: theme.Text},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, testCase.want, traceColor(theme, testCase.kind))
		})
	}
}

func TestTracePaneDrawsPlaceholderWhenEmpty(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))

	app := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})
	require.NoError(t, app.loop(context.Background()))

	text := screen.contents()
	assert.Contains(t, text, "Trace", "trace box renders its title")
	assert.Contains(t, text, "Waiting for activity", "empty trace shows its placeholder")
}

func TestTracePaneAppendEventAppendsStyledLine(t *testing.T) {
	t.Parallel()

	theme := DefaultTheme()
	app := newApp(newFakeScreen(80, 24), RunOptions{
		Trace:      nil,
		Controller: nil,
		Title:      defaultTitle,
	})

	app.applyTrace(traceEvent(TraceKindTurn, "first", 0, 0, 0, 0))
	app.applyTrace(traceEvent(TraceKindFinal, "done", 3, 4, 0, 0))

	lines := traceLines(app)
	require.Len(t, lines, 2)
	assert.Equal(t, "[turn] first", lines[0])
	assert.Equal(t, "[final] done (7 tok)", lines[1])

	// Final events are colored with the success token; the line carries that
	// foreground style rather than a zero style.
	assert.Equal(t, theme.fg(traceColor(theme, TraceKindFinal)), app.trace.view.Lines[1].Style)
}

func TestStartRunResetsTracePaneButRetainsSessionCost(t *testing.T) {
	t.Parallel()

	ctrl := new(mockController)
	ctrl.On("Start", mock.Anything, "first", uint64(1)).Return(scriptedTrace(1)).Once()
	ctrl.On("Start", mock.Anything, "second", uint64(2)).Return(scriptedTrace(2)).Once()

	app := newApp(newFakeScreen(80, 24), RunOptions{
		Trace:      nil,
		Controller: ctrl,
		Title:      defaultTitle,
	})

	// Run #1: a turn and a final populate the trace pane, and a usage event tallies
	// into the session cost before the final answer clears the working state.
	app.startRun(context.Background(), "first")
	require.Equal(t, uint64(1), app.runID)

	app.applyTrace(traceEvent(TraceKindTurn, "looking", 0, 0, 0, 1))
	app.applyTrace(traceEvent(TraceKindUsage, "spend", 40, 60, 1_500_000, 1))
	app.applyTrace(traceEvent(TraceKindFinal, "done", 0, 0, 0, 1))

	require.Len(t, traceLines(app), 2, "run #1 left turn and final lines in the trace pane")
	require.False(t, app.working, "the final answer cleared the working state so a new run may begin")

	// Run #2 resets the trace pane to its placeholder but leaves the session cost
	// totals from run #1 untouched.
	app.startRun(context.Background(), "second")
	require.Equal(t, uint64(2), app.runID)

	assert.Empty(t, traceLines(app), "starting a new run clears the trace pane lines")
	assert.Equal(t, tracePlaceholder, app.trace.view.Text, "the cleared trace pane shows its placeholder again")
	assert.Equal(t, 40, app.cost.tokensIn, "cost tokens in survive the per-run trace reset")
	assert.Equal(t, 60, app.cost.tokensOut, "cost tokens out survive the per-run trace reset")
	assert.Equal(t, "$1.5000", app.cost.dollars(), "cost dollars survive the per-run trace reset")

	// A leftover event still tagged with run #1's RunID is dropped by the loop's run
	// gating now that run #2 is active.
	app.handleTrace(traceEvent(TraceKindFinal, "stale", 0, 0, 0, 1), true)
	assert.Empty(t, traceLines(app), "a stale run #1 event does not append to run #2's trace pane")

	// A fresh run #2 event still lands normally through the same gate.
	app.handleTrace(traceEvent(TraceKindTurn, "again", 0, 0, 0, 2), true)
	require.Len(t, traceLines(app), 1)
	assert.Equal(t, "[turn] again", traceLines(app)[0])

	ctrl.AssertExpectations(t)
}

func TestTracePaneDrawShowsAppendedEventText(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	traceCh := make(chan TraceEvent)

	app := newApp(screen, RunOptions{Trace: traceCh, Controller: nil, Title: defaultTitle})

	done := make(chan error, 1)
	go func() { done <- app.loop(context.Background()) }()

	sendTrace(t, traceCh, traceEvent(TraceKindSubCall, "embedding", 0, 0, 0, 0))
	// A sub-call marks the loop working, so its draw is throttled to the frame
	// ticker; wait for that frame before quitting so the assertion is not racy.
	awaitRender(t, screen, 2)
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))
	require.NoError(t, awaitLoop(t, done))

	text := screen.contents()
	assert.Contains(t, text, "embedding", "appended event text is drawn into the pane")
	assert.NotContains(t, strings.TrimSpace(text), "Waiting for activity", "placeholder is replaced once events arrive")
}
