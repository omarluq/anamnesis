package tui_test

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	cellcolor "github.com/gdamore/tcell/v3/color"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/tui"
)

func TestTextViewRenderPlainRichAndDraw(t *testing.T) {
	t.Parallel()

	view := tui.NewTextView(testAlpha + " " + testBeta + "\ngamma")
	view.Scroll = 1
	plain := view.Render(6, 2)
	require.Equal(t, []string{testBeta, "gamma"}, lineTexts(plain))

	view.Wrap = false
	// Scroll past the content: with only 2 lines and a height of 2, the start is
	// clamped to len-height (0) so the viewport shows the full last page.
	require.Equal(t, []string{"alpha ", "gamma"}, lineTexts(view.Render(6, 2)))

	rich := tui.NewTextView("").SetLines([]tui.Line{
		{
			Text:  "red green",
			Style: tcell.StyleDefault,
			Spans: []tui.Span{
				{Text: "red", Style: tcell.StyleDefault.Foreground(cellcolor.Red)},
				{Text: " green", Style: tcell.StyleDefault.Foreground(cellcolor.Green)},
			},
		},
	})
	require.Equal(t, []string{"red", "green"}, lineTexts(rich.Render(5, 3)))

	buffer := tui.NewCellBuffer(4, 2, tcell.StyleDefault)
	rich.Draw(buffer, testRect(0, 0, 4, 2))
	require.Equal(t, "red ", bufferLine(buffer, 0))
	require.Equal(t, "gree", bufferLine(buffer, 1))

	// With wrapping disabled, rich lines are fit to width (no ellipsis), matching
	// the plain-text path instead of returning raw over-width lines.
	rich.Wrap = false
	require.Equal(t, []string{"red g"}, lineTexts(rich.Render(5, 3)))

	richText := tui.NewRichText([]tui.Line{
		tui.NewLine(tcell.StyleDefault, "a"),
		tui.NewLine(tcell.StyleDefault, "b"),
	})
	require.Equal(t, []string{"a", "b"}, lineTexts(richText.Lines))
}
