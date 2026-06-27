package terminal

import (
	"fmt"

	"github.com/dustin/go-humanize"

	"github.com/omarluq/anamnesis/internal/tui"
)

const costMicrosPerDollar = 1_000_000.0

// Row labels shown in the first column of the cost table.
const (
	labelTokensIn  = "Tokens In"
	labelTokensOut = "Tokens Out"
	labelTotal     = "Total"
	labelCost      = "Cost"
)

// costPane tallies token usage and cost and renders them as a two-column table.
type costPane struct {
	theme      Theme
	tokensIn   int
	tokensOut  int
	costMicros int64
}

// newCostPane returns a cost pane with zeroed counters.
func newCostPane(theme Theme) *costPane {
	return &costPane{theme: theme, tokensIn: 0, tokensOut: 0, costMicros: 0}
}

// Draw paints the metric/value table into rect.
func (pane *costPane) Draw(screen tui.ContentSetter, rect tui.Rect) {
	accent := pane.theme.fg(pane.theme.Accent)
	table := tui.Table{
		Style:       pane.theme.fg(pane.theme.Text),
		HeaderStyle: accent,
		BorderStyle: pane.theme.fg(pane.theme.Border),
		Headers: []tui.TableCell{
			{Style: accent, Text: "Metric"},
			{Style: accent, Text: "Value"},
		},
		Rows:       pane.rows(),
		Alignments: []tui.Alignment{tui.AlignLeft, tui.AlignRight},
		Stretch:    true,
	}
	table.Draw(screen, rect)
}

// applyUsage accumulates a usage delta into the running totals.
func (pane *costPane) applyUsage(tokensIn, tokensOut int, costMicros int64) {
	pane.tokensIn += tokensIn
	pane.tokensOut += tokensOut
	pane.costMicros += costMicros
}

// rows builds the metric/value data rows for the table.
func (pane *costPane) rows() [][]tui.TableCell {
	style := pane.theme.fg(pane.theme.Text)

	return [][]tui.TableCell{
		{{Style: style, Text: labelTokensIn}, {Style: style, Text: tokens(pane.tokensIn)}},
		{{Style: style, Text: labelTokensOut}, {Style: style, Text: tokens(pane.tokensOut)}},
		{{Style: style, Text: labelTotal}, {Style: style, Text: tokens(pane.tokensIn + pane.tokensOut)}},
		{{Style: style, Text: labelCost}, {Style: style, Text: pane.dollars()}},
	}
}

// dollars formats the accumulated micro-dollar cost as a dollar amount.
func (pane *costPane) dollars() string {
	return fmt.Sprintf("$%.4f", float64(pane.costMicros)/costMicrosPerDollar)
}

// tokens renders a token count with thousands separators (e.g. "12,345").
func tokens(count int) string {
	return humanize.Comma(int64(count))
}
