package tui_test

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/tui"
)

func TestTextAreaEditing(t *testing.T) {
	t.Parallel()

	area := tui.NewTextArea()
	area.InsertRune('h')
	area.InsertRune('i')
	area.MoveLeft()
	area.InsertRune('!')

	require.Equal(t, "h!i", area.Text)
	require.Equal(t, 2, area.Cursor)
}

func TestTextAreaClearReturnsPreviousText(t *testing.T) {
	t.Parallel()

	area := tui.NewTextArea()
	area.SetText("draft")

	require.Equal(t, "draft", area.Clear())
	require.True(t, area.Empty())
}

func TestClampCursor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		cursor    int
		runeCount int
		want      int
	}{
		{name: "below zero", cursor: -1, runeCount: 3, want: 0},
		{name: "inside", cursor: 2, runeCount: 3, want: 2},
		{name: "above length", cursor: 10, runeCount: 3, want: 3},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, test.want, tui.ClampCursor(test.cursor, test.runeCount))
		})
	}
}

func TestTextAreaMoveAndDeleteBackward(t *testing.T) {
	t.Parallel()

	area := tui.NewTextArea()
	area.SetText("ab")
	area.MoveLeft()
	area.MoveRight()
	require.Equal(t, 2, area.Cursor)

	area.DeleteBackward()
	require.Equal(t, "a", area.Text)
	require.Equal(t, 1, area.Cursor)
}

func TestTextAreaPrimitivesClampAndHandleBoundaries(t *testing.T) {
	t.Parallel()

	value := []rune("hello\nworld")

	next, cursor := tui.InsertRuneAt(value, -10, 'x')
	require.Equal(t, "xhello\nworld", string(next))
	require.Equal(t, 1, cursor)

	next, cursor = tui.InsertRuneAt(value, 3, 0)
	require.Equal(t, string(value), string(next))
	require.Equal(t, 3, cursor)

	require.Equal(t, 0, tui.MoveCursorLeft(value, -1))
	require.Equal(t, len(value), tui.MoveCursorRight(value, 99))

	next, cursor = tui.DeleteBackwardAt(value, 0)
	require.Equal(t, string(value), string(next))
	require.Equal(t, 0, cursor)
}

func TestTextAreaCursorPositionCountsTrailingSpaces(t *testing.T) {
	t.Parallel()

	row, column := tui.TextAreaCursorPosition([]rune("abc   "), 6, 20)
	require.Equal(t, 0, row)
	require.Equal(t, 6, column)
}

func TestTextAreaBodyLinesPreserveTrailingSpaces(t *testing.T) {
	t.Parallel()

	lines := tui.TextAreaBodyLines([]rune("abc   "), 20)
	require.Equal(t, []string{"abc   "}, lines)
}

func TestTextAreaCursorPositionUsesCellWidth(t *testing.T) {
	t.Parallel()

	_, column := tui.TextAreaCursorPosition([]rune("語 "), 2, 20)
	require.Equal(t, 3, column)
}

func TestTextAreaVisibleLinesKeepsCursorVisible(t *testing.T) {
	t.Parallel()

	visible, skipped := tui.VisibleLines([]string{"a", "b", "c", "d"}, 2, 3)
	require.Equal(t, []string{"c", "d"}, visible)
	require.Equal(t, 2, skipped)
}

func TestRenderTextArea(t *testing.T) {
	t.Parallel()

	area := tui.NewTextArea()
	area.SetText("first\nsecond")
	area.Cursor = len([]rune("first\nse"))
	rendered := area.Render(12, 2, tui.TextAreaStyles{Border: tcell.StyleDefault, Body: tcell.StyleDefault})

	require.Len(t, rendered.Lines, 4)
	require.Equal(t, 4, rendered.CursorCol)
	require.Equal(t, 2, rendered.CursorRow)
}

func TestRenderTextAreaWrapsBeforeRightBorder(t *testing.T) {
	t.Parallel()

	area := tui.NewTextArea()
	area.SetText("abcdefghijklmnopq")
	rendered := area.Render(20, 3, tui.TextAreaStyles{Border: tcell.StyleDefault, Body: tcell.StyleDefault})

	require.Equal(t, "│ abcdefghijklmnop │", rendered.Lines[1].Text)
	require.Equal(t, "│ q                │", rendered.Lines[2].Text)
	require.Equal(t, 3, rendered.CursorCol)
	require.Equal(t, 2, rendered.CursorRow)
}

func TestWrapPreserveWhitespaceHandlesNarrowWidth(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{""}, tui.WrapPreserveWhitespace("abc", 0))
}
