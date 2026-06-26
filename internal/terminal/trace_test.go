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

func TestTraceTextFormatsEvents(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		want  string
		event terminal.TraceEvent
	}{
		{
			name:  "turn without tokens",
			want:  "[turn] thinking",
			event: traceEvent(terminal.TraceKindTurn, "thinking", 0, 0, 0),
		},
		{
			name:  "sub-call without tokens",
			want:  "[sub-call] tool",
			event: traceEvent(terminal.TraceKindSubCall, "tool", 0, 0, 0),
		},
		{
			name:  "final with tokens",
			want:  "[final] answer (42 tok)",
			event: traceEvent(terminal.TraceKindFinal, "answer", 42, 0, 0),
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, testCase.want, terminal.TraceText(testCase.event))
		})
	}
}

func TestTraceStyleMapsKindToPaletteColor(t *testing.T) {
	t.Parallel()

	theme := terminal.DefaultTheme()

	cases := []struct {
		name string
		kind terminal.TraceKind
		want tcell.Color
	}{
		{name: "final", kind: terminal.TraceKindFinal, want: theme.Success},
		{name: "sub-call", kind: terminal.TraceKindSubCall, want: theme.Accent},
		{name: "usage", kind: terminal.TraceKindUsage, want: theme.Warning},
		{name: "turn", kind: terminal.TraceKindTurn, want: theme.Text},
		{name: "unknown", kind: terminal.TraceKind("mystery"), want: theme.Text},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, testCase.want, terminal.TraceStyle(theme, testCase.kind).GetForeground())
		})
	}
}

func TestTracePaneDrawsPlaceholderWhenEmpty(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))

	app := terminal.NewApp(screen, terminal.RunOptions{Trace: nil, Title: terminal.DefaultTitle})
	require.NoError(t, app.Loop(context.Background()))

	text := screen.contents()
	assert.Contains(t, text, "Trace", "trace box renders its title")
	assert.Contains(t, text, "Waiting for activity", "empty trace shows its placeholder")
}

func TestTracePaneAppendEventAppendsStyledLine(t *testing.T) {
	t.Parallel()

	theme := terminal.DefaultTheme()
	app := terminal.NewApp(newFakeScreen(80, 24), terminal.RunOptions{Trace: nil, Title: terminal.DefaultTitle})

	app.ApplyTrace(traceEvent(terminal.TraceKindTurn, "first", 0, 0, 0))
	app.ApplyTrace(traceEvent(terminal.TraceKindFinal, "done", 7, 0, 0))

	lines := app.TraceLines()
	require.Len(t, lines, 2)
	assert.Equal(t, "[turn] first", lines[0])
	assert.Equal(t, "[final] done (7 tok)", lines[1])

	// Final events are colored with the success token; the line carries that
	// foreground style rather than a zero style.
	assert.Equal(t, terminal.TraceStyle(theme, terminal.TraceKindFinal), app.TraceLineStyle(1))
}

func TestTracePaneDrawShowsAppendedEventText(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	traceCh := make(chan terminal.TraceEvent)

	app := terminal.NewApp(screen, terminal.RunOptions{Trace: traceCh, Title: terminal.DefaultTitle})

	done := make(chan error, 1)
	go func() { done <- app.Loop(context.Background()) }()

	sendTrace(t, traceCh, traceEvent(terminal.TraceKindSubCall, "embedding", 0, 0, 0))
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))
	require.NoError(t, awaitLoop(t, done))

	text := screen.contents()
	assert.Contains(t, text, "embedding", "appended event text is drawn into the pane")
	assert.NotContains(t, strings.TrimSpace(text), "Waiting for activity", "placeholder is replaced once events arrive")
}
