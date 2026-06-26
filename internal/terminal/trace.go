package terminal

import (
	"fmt"

	"github.com/gdamore/tcell/v3"

	"github.com/omarluq/anamnesis/internal/tui"
)

const tracePlaceholder = "Waiting for activity…"

// tracePane is an append-only, color-coded view of controller trace events.
type tracePane struct {
	view  *tui.TextView
	lines []tui.Line
	theme Theme
}

// newTracePane returns an empty trace pane showing a placeholder line.
func newTracePane(theme Theme) *tracePane {
	view := tui.NewTextView(tracePlaceholder)
	view.Style = theme.fg(theme.Muted)

	return &tracePane{view: view, lines: nil, theme: theme}
}

// Draw paints the trace box and its lines into rect.
func (pane *tracePane) Draw(screen tui.ContentSetter, rect tui.Rect) {
	box := tui.NewBox("Trace")
	box.Style = pane.theme.fg(pane.theme.Border)
	box.Draw(screen, rect)

	inner := rect.Inner(1)
	if inner.Empty() {
		return
	}

	pane.view.Draw(screen, inner)
}

// appendEvent styles event and appends it as a new trailing line.
func (pane *tracePane) appendEvent(event TraceEvent) {
	line := tui.NewLine(pane.theme.fg(traceColor(pane.theme, event.Kind)), traceText(event))
	pane.lines = append(pane.lines, line)
	pane.view.SetLines(pane.lines)
}

// traceText formats an event into a single display string.
func traceText(event TraceEvent) string {
	tokens := event.TokensIn + event.TokensOut
	if tokens > 0 {
		return fmt.Sprintf("[%s] %s (%d tok)", event.Kind, event.Text, tokens)
	}

	return fmt.Sprintf("[%s] %s", event.Kind, event.Text)
}

// traceColor selects the foreground color used to render an event kind.
func traceColor(theme Theme, kind TraceKind) tcell.Color {
	switch kind {
	case TraceKindFinal:
		return theme.Success
	case TraceKindSubCall:
		return theme.Accent
	case TraceKindUsage:
		return theme.Warning
	case TraceKindTurn:
		return theme.Text
	default:
		return theme.Text
	}
}
