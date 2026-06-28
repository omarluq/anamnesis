package tui_test

import (
	"strings"
	"testing"

	cellcolor "github.com/gdamore/tcell/v3/color"

	"github.com/omarluq/anamnesis/internal/tui"
)

func lineText(lines []tui.Line) string {
	return strings.Join(lineTexts(lines), "\n")
}

func assertNoLineWiderThan(t *testing.T, lines []tui.Line, width int) {
	t.Helper()

	for _, line := range lines {
		if line.Width() > width {
			t.Fatalf("line width = %d, want <= %d: %q", line.Width(), width, line.Text)
		}
	}
}

func testCodeTheme() tui.CodeTheme {
	return tui.CodeTheme{
		Text:    cellcolor.White,
		Accent:  cellcolor.Blue,
		Success: cellcolor.Green,
		Warning: cellcolor.Yellow,
		Dim:     cellcolor.Gray,
		Muted:   cellcolor.Gray,
		DiffAdd: cellcolor.Green,
		DiffDel: cellcolor.Red,
	}
}
