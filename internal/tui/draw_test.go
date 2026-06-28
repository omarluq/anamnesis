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
