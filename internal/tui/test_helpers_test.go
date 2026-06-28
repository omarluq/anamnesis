package tui_test

import (
	"strings"

	"github.com/gdamore/tcell/v3"

	"github.com/omarluq/anamnesis/internal/tui"
)

const (
	testAlpha = "alpha"
	testBeta  = "beta"
	testHello = "hello"
)

func testRect(y, width, height int) tui.Rect {
	return tui.Rect{X: 0, Y: y, Width: width, Height: height}
}

func testTableCell(text string) tui.TableCell {
	return tui.TableCell{Style: tcell.StyleDefault, Text: text}
}

func testLine(text string) tui.Line {
	return tui.Line{Style: tcell.StyleDefault, Text: text, Spans: nil}
}

func bufferLine(buffer *tui.CellBuffer, row int) string {
	var builder strings.Builder
	for column := range buffer.Width() {
		builder.WriteRune(buffer.Cell(column, row).Rune)
	}

	return builder.String()
}

func lineTexts(lines []tui.Line) []string {
	texts := make([]string, 0, len(lines))
	for _, line := range lines {
		texts = append(texts, line.Text)
	}

	return texts
}

type cellRecordingScreen struct {
	calls []cellWrite
}

type cellWrite struct {
	combining []rune
	primary   rune
}

func (screen *cellRecordingScreen) SetContent(_, _ int, primary rune, combining []rune, _ tcell.Style) {
	screen.calls = append(screen.calls, cellWrite{primary: primary, combining: append([]rune(nil), combining...)})
}

type recordingScreen struct {
	cells map[[2]int]rune
}

func (screen *recordingScreen) SetContent(x, y int, primary rune, _ []rune, _ tcell.Style) {
	screen.cells[[2]int{x, y}] = primary
}
