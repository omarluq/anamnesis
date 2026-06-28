package terminal

import (
	"fmt"
	"strings"

	"github.com/dustin/go-humanize"

	"github.com/omarluq/anamnesis/internal/tui"
)

const (
	// costMicrosPerDollar converts the accumulated micro-dollar cost to dollars.
	costMicrosPerDollar = 1_000_000.0
	// footerSeparator joins the footer's title, usage, and key-hint segments.
	footerSeparator = "  ·  "
	// footerKeyHints lists the shell's key bindings in the status footer.
	footerKeyHints = "ctrl+o queries · enter send · ctrl+c quit"
)

// footerLine builds the single status-footer row: the title (with a spinner while
// a run is in flight), the session token-and-cost usage, and the key hints, all
// truncated to width.
func (app *App) footerLine(width int) tui.Line {
	segments := []string{app.footerTitle(), app.usageSummary(), footerKeyHints}
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

// usageSummary renders the accumulated session token counts and dollar cost.
func (app *App) usageSummary() string {
	return tokens(app.tokensIn) + " in / " + tokens(app.tokensOut) + " out · " + app.dollars()
}

// dollars formats the accumulated micro-dollar cost as a dollar amount.
func (app *App) dollars() string {
	return fmt.Sprintf("$%.4f", float64(app.costMicros)/costMicrosPerDollar)
}

// tokens renders a token count with thousands separators (e.g. "12,345").
func tokens(count int) string {
	return humanize.Comma(int64(count))
}
