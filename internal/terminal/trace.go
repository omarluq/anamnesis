package terminal

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v3"

	"github.com/omarluq/anamnesis/internal/tui"
)

const (
	tracePlaceholder = "Waiting for activity…"
	traceIndentUnit  = "  "
)

// tracePane is an append-only, color-coded view of controller trace events. The
// view's Lines slice is the single source of truth for the accumulated events.
type tracePane struct {
	view  *tui.TextView
	theme Theme
}

// newTracePane returns an empty trace pane showing a placeholder line.
func newTracePane(theme Theme) *tracePane {
	view := tui.NewTextView(tracePlaceholder)
	view.Style = theme.fg(theme.Muted)

	return &tracePane{view: view, theme: theme}
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

// appendEvent styles event and appends it as a new trailing line directly onto
// the view's Lines slice, clearing the placeholder Text so the events render.
func (pane *tracePane) appendEvent(event TraceEvent) {
	line := tui.NewLine(pane.theme.fg(traceColor(pane.theme, event.Kind)), traceText(event))
	pane.view.Lines = append(pane.view.Lines, line)
	pane.view.Text = ""
}

// reset clears the accumulated trace lines and re-asserts the muted placeholder
// state newTracePane establishes, so each new run begins with an empty pane. The
// session cost pane is intentionally left untouched, so its token and dollar
// totals accumulate across runs.
func (pane *tracePane) reset() {
	pane.view.Lines = nil
	pane.view.Text = tracePlaceholder
	pane.view.Style = pane.theme.fg(pane.theme.Muted)
}

// traceText formats an event into a single display string, indenting nested
// sub-call lines so fan-out reads as a tree.
func traceText(event TraceEvent) string {
	indent := traceIndent(event.Depth)
	total := event.TokensIn + event.TokensOut

	if total > 0 {
		return fmt.Sprintf("%s[%s] %s (%s tok)", indent, event.Kind, event.Text, tokens(total))
	}

	return fmt.Sprintf("%s[%s] %s", indent, event.Kind, event.Text)
}

// traceIndent returns the leading indentation for an event at depth: two spaces
// per nesting level, and none at the top level.
func traceIndent(depth int) string {
	if depth <= 0 {
		return ""
	}

	return strings.Repeat(traceIndentUnit, depth)
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
	case TraceKindCode:
		return theme.Dim
	case TraceKindStdout:
		return theme.Muted
	case TraceKindTurn:
		return theme.Text
	default:
		return theme.Text
	}
}
