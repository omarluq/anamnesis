package tui_test

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/tui"
)

func TestDrawLineAndLines(t *testing.T) {
	t.Parallel()

	buffer := tui.NewCellBuffer(8, 3, tcell.StyleDefault)
	tui.DrawLine(buffer, testRect(1, 4, 1), tui.Line{
		Text:  "abcd",
		Style: tcell.StyleDefault,
		Spans: []tui.Span{
			{Text: "ab", Style: tcell.StyleDefault},
			{Text: "cd", Style: tcell.StyleDefault},
		},
	})
	tui.DrawLines(buffer, testRect(2, 8, 1), []tui.Line{tui.NewLine(tcell.StyleDefault, "line")})

	require.Equal(t, "abcd    ", bufferLine(buffer, 1))
	require.Equal(t, "line    ", bufferLine(buffer, 2))
}

// TestDrawLineWideRune keeps regression coverage for multi-cell glyph drawing: a
// wide rune fills its own cell plus a blank continuation cell, so the trailing
// text lands two columns over rather than one.
func TestDrawLineWideRune(t *testing.T) {
	t.Parallel()

	buffer := tui.NewCellBuffer(8, 1, tcell.StyleDefault)
	tui.DrawLine(buffer, testRect(0, 8, 1), tui.NewLine(tcell.StyleDefault, "語x"))

	require.Equal(t, "語 x     ", bufferLine(buffer, 0))
}

// TestDrawLinesTabFill keeps regression coverage for tab drawing: DrawLines
// expands a \t to four spaces, pushing the trailing text to the next tab stop.
func TestDrawLinesTabFill(t *testing.T) {
	t.Parallel()

	buffer := tui.NewCellBuffer(8, 1, tcell.StyleDefault)
	tui.DrawLines(buffer, testRect(0, 8, 1), []tui.Line{tui.NewLine(tcell.StyleDefault, "a\tb")})

	require.Equal(t, "a    b  ", bufferLine(buffer, 0))
}
