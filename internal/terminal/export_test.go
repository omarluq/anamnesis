package terminal

import (
	"context"

	"github.com/gdamore/tcell/v3"

	"github.com/omarluq/anamnesis/internal/tui"
)

// DefaultTitle is the chat title applied when RunOptions.Title is empty.
const DefaultTitle = defaultTitle

// NewApp constructs a shell App bound to screen for black-box tests.
var NewApp = newApp

// SpinnerFrames exposes the spinner glyph cycle for assertions.
var SpinnerFrames = spinnerFrames

// TraceText formats a trace event for display.
func TraceText(event TraceEvent) string { return traceText(event) }

// TraceStyle returns the foreground style a kind renders with under theme.
func TraceStyle(theme Theme, kind TraceKind) tcell.Style {
	return theme.fg(traceColor(theme, kind))
}

// CostSnapshot reports a cost pane's state after applying usage deltas.
type CostSnapshot struct {
	Dollars   string
	Rows      [][]string
	TokensIn  int
	TokensOut int
}

// CostProbe accumulates the (tokensIn, tokensOut, costMicros) deltas into a fresh
// cost pane and reports its tallies, formatted dollars, and rendered row texts.
func CostProbe(deltas ...[3]int64) CostSnapshot {
	pane := newCostPane(DefaultTheme())
	for _, delta := range deltas {
		pane.applyUsage(int(delta[0]), int(delta[1]), delta[2])
	}

	rendered := pane.rows()
	rows := make([][]string, 0, len(rendered))

	for _, row := range rendered {
		cells := make([]string, 0, len(row))
		for _, cell := range row {
			cells = append(cells, cell.Text)
		}

		rows = append(rows, cells)
	}

	return CostSnapshot{
		TokensIn:  pane.tokensIn,
		TokensOut: pane.tokensOut,
		Dollars:   pane.dollars(),
		Rows:      rows,
	}
}

// Loop runs the shell select loop on the injected screen.
func (app *App) Loop(ctx context.Context) error { return app.loop(ctx) }

// Title returns the configured chat title.
func (app *App) Title() string { return app.title }

// RunID returns the active run identifier used for stale-event gating.
func (app *App) RunID() uint64 { return app.runID }

// PanesReady reports whether the chat, trace, and cost panes are wired.
func (app *App) PanesReady() bool {
	return app.chat != nil && app.trace != nil && app.cost != nil
}

// HeaderTitle returns the chat header for the current working state.
func (app *App) HeaderTitle() string { return app.headerTitle() }

// SpinnerGlyph returns the active spinner frame, empty when idle.
func (app *App) SpinnerGlyph() string { return app.spinnerGlyph() }

// SetSpinnerFrame fixes the spinner frame index.
func (app *App) SetSpinnerFrame(frame int) { app.spinnerFrame = frame }

// ApplyTrace routes a trace event into the pane that owns its kind.
func (app *App) ApplyTrace(event TraceEvent) { app.applyTrace(event) }

// TraceLines returns the trace pane's accumulated line texts.
func (app *App) TraceLines() []string {
	texts := make([]string, 0, len(app.trace.lines))
	for _, line := range app.trace.lines {
		texts = append(texts, line.Text)
	}

	return texts
}

// TraceLineStyle returns the style of the trace line at index.
func (app *App) TraceLineStyle(index int) tcell.Style { return app.trace.lines[index].Style }

// CostTokensIn returns the accumulated input-token tally.
func (app *App) CostTokensIn() int { return app.cost.tokensIn }

// CostTokensOut returns the accumulated output-token tally.
func (app *App) CostTokensOut() int { return app.cost.tokensOut }

// CostMicros returns the accumulated micro-dollar cost.
func (app *App) CostMicros() int64 { return app.cost.costMicros }

// CostDollarText returns the cost pane's formatted dollar amount.
func (app *App) CostDollarText() string { return app.cost.dollars() }

// ComposerInput feeds a printable key into the chat composer.
func (app *App) ComposerInput(key, text string) {
	app.chat.handleKey(tui.KeyEvent{Key: key, Text: text, Ctrl: false, Alt: false, Shift: false})
}

// ComposerInputCtrl feeds a ctrl-chorded key into the chat composer.
func (app *App) ComposerInputCtrl(key string) {
	app.chat.handleKey(tui.KeyEvent{Key: key, Text: "", Ctrl: true, Alt: false, Shift: false})
}

// ComposerEmpty reports whether the composer holds no text.
func (app *App) ComposerEmpty() bool { return app.chat.composerEmpty() }

// ComposerText returns the composer's current text.
func (app *App) ComposerText() string { return app.chat.composer.TextValue() }

// AnswerText returns the chat answer view's source markdown.
func (app *App) AnswerText() string { return app.chat.view.Text }

// ChatRender returns the rendered answer lines as plain text.
func (app *App) ChatRender(width, height int) []string {
	lines := app.chat.render(width, height)
	texts := make([]string, 0, len(lines))

	for _, line := range lines {
		texts = append(texts, line.Text)
	}

	return texts
}
