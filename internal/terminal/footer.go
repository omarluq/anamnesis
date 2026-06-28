package terminal

import (
	"strings"

	"github.com/omarluq/anamnesis/internal/tui"
)

const (
	// footerSeparator joins the footer's title and key-hint segments.
	footerSeparator = "  ·  "
	// footerKeyHints lists the shell's key bindings in the status footer.
	footerKeyHints = "ctrl+o expand · enter send · ctrl+c quit"
)

// footerLine builds the single status-footer row: the title (with a spinner while
// a run is in flight) and the key hints, truncated to width.
func (app *App) footerLine(width int) tui.Line {
	segments := []string{app.footerTitle(), footerKeyHints}
	text := strings.Join(segments, footerSeparator)

	return tui.NewLine(app.theme.fg(app.theme.Dim), tui.Truncate(text, width))
}

// footerTitle is the shell title, gaining a spinner glyph while working.
func (app *App) footerTitle() string {
	if app.working {
		return app.title + " " + app.spinnerGlyph()
	}

	return app.title
}
