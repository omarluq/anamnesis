package tui_test

import (
	"strings"
	"testing"

	cellcolor "github.com/gdamore/tcell/v3/color"

	"github.com/omarluq/anamnesis/internal/tui"
)

func lineText(lines []tui.Line) string {
	texts := make([]string, 0, len(lines))
	for _, line := range lines {
		texts = append(texts, line.Text)
	}

	return strings.Join(texts, "\n")
}

func assertContains(t *testing.T, text, want string) {
	t.Helper()

	if !strings.Contains(text, want) {
		t.Fatalf("rendered text missing %q:\n%s", want, text)
	}
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
