package tui_test

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/tui"
)

func TestTableRenderAlignmentClippingAndDraw(t *testing.T) {
	t.Parallel()

	table := &tui.Table{
		Style:       tcell.StyleDefault,
		HeaderStyle: tcell.StyleDefault,
		BorderStyle: tcell.StyleDefault,
		Headers:     []tui.TableCell{testTableCell("Name"), testTableCell("Count")},
		Rows: [][]tui.TableCell{
			{testTableCell(testAlpha), testTableCell("1")},
			{testTableCell("語"), testTableCell("200")},
		},
		Alignments: []tui.Alignment{tui.AlignLeft, tui.AlignRight},
	}

	lines := table.Render(24, 10)
	joined := strings.Join(lineTexts(lines), "\n")
	require.Contains(t, joined, "Name")
	require.Contains(t, joined, "Count")
	require.Contains(t, joined, testAlpha)
	require.Contains(t, joined, "語")

	for _, line := range lines {
		require.LessOrEqual(t, line.Width(), 24)
	}

	centeredTable := &tui.Table{
		Style:       tcell.StyleDefault,
		HeaderStyle: tcell.StyleDefault,
		BorderStyle: tcell.StyleDefault,
		Headers:     nil,
		Rows:        [][]tui.TableCell{{testTableCell("x")}},
		Alignments:  []tui.Alignment{tui.AlignCenter},
	}
	centered := centeredTable.Render(7, 10)
	require.Contains(t, strings.Join(lineTexts(centered), "\n"), " x ")

	buffer := tui.NewCellBuffer(24, 6, tcell.StyleDefault)
	table.Draw(buffer, testRect(0, 0, 24, 6))
	require.Equal(t, '╭', buffer.Cell(0, 0).Rune)
}

func TestTableRenderShortHeightKeepsHeaderAndBorders(t *testing.T) {
	t.Parallel()

	table := &tui.Table{
		Style:       tcell.StyleDefault,
		HeaderStyle: tcell.StyleDefault,
		BorderStyle: tcell.StyleDefault,
		Headers:     []tui.TableCell{testTableCell("Name"), testTableCell("Count")},
		Rows: [][]tui.TableCell{
			{testTableCell("a"), testTableCell("1")},
			{testTableCell("b"), testTableCell("2")},
			{testTableCell("c"), testTableCell("3")},
		},
		Alignments: nil,
	}

	// Height 5 fits top border + header + separator + one data row + bottom border.
	lines := table.Render(24, 5)
	require.Len(t, lines, 5)

	texts := lineTexts(lines)
	require.True(t, strings.HasPrefix(texts[0], "╭"), "top border must be the first line")
	require.Contains(t, texts[1], "Name")
	require.Contains(t, texts[1], "Count")
	require.True(t, strings.HasPrefix(texts[len(texts)-1], "╰"), "bottom border must be the last line")

	joined := strings.Join(texts, "\n")
	require.Contains(t, joined, " a ", "first data row must be rendered")
	require.NotContains(t, joined, " c ", "rows beyond the height budget must be clipped")

	// Height 1 keeps the top border rather than only the closing border.
	tiny := table.Render(24, 1)
	require.Len(t, tiny, 1)
	require.True(t, strings.HasPrefix(lineTexts(tiny)[0], "╭"), "single visible row must be the top border")
}
