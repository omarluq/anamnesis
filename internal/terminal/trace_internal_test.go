package terminal

import (
	"context"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/stretchr/testify/assert"
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

			assert.Equal(t, testCase.want, theme.fg(traceColor(theme, testCase.kind)).GetForeground())
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
	assert.Equal(t, theme.fg(traceColor(theme, TraceKindFinal)), app.trace.lines[1].Style)
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
