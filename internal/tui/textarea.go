package tui

import (
	"strings"

	"github.com/gdamore/tcell/v3"
)

// TextArea is an editable multiline text buffer.
type TextArea struct {
	Text   string
	Cursor int
}

// NewTextArea returns an initialized empty text area.
func NewTextArea() TextArea {
	return TextArea{
		Text:   "",
		Cursor: 0,
	}
}

// Empty reports whether the text is empty.
func (area *TextArea) Empty() bool {
	return area == nil || area.Text == ""
}

// SetRunes replaces the content and clamps the cursor.
func (area *TextArea) SetRunes(value []rune, cursor int) {
	if area == nil {
		return
	}

	area.Text = string(value)
	area.Cursor = ClampCursor(cursor, len(value))
}

// SetText replaces the content and moves the cursor to the end.
func (area *TextArea) SetText(text string) {
	area.SetRunes([]rune(text), len([]rune(text)))
}

// Clear empties the text area and returns the previous text.
func (area *TextArea) Clear() string {
	if area == nil {
		return ""
	}

	text := area.Text
	area.SetText("")

	return text
}

// Update applies a text/cursor mutation to the current state.
func (area *TextArea) Update(mutator func([]rune, int) ([]rune, int)) {
	if area == nil || mutator == nil {
		return
	}

	value := []rune(area.Text)
	cursor := ClampCursor(area.Cursor, len(value))
	nextValue, nextCursor := mutator(value, cursor)
	area.SetRunes(nextValue, nextCursor)
}

// InsertRune inserts char at the cursor.
func (area *TextArea) InsertRune(char rune) {
	area.Update(func(value []rune, cursor int) ([]rune, int) { return InsertRuneAt(value, cursor, char) })
}

// MoveLeft moves the cursor left by one rune.
func (area *TextArea) MoveLeft() {
	area.Update(func(value []rune, cursor int) ([]rune, int) { return value, MoveCursorLeft(value, cursor) })
}

// MoveRight moves the cursor right by one rune.
func (area *TextArea) MoveRight() {
	area.Update(func(value []rune, cursor int) ([]rune, int) { return value, MoveCursorRight(value, cursor) })
}

// DeleteBackward deletes one rune before the cursor.
func (area *TextArea) DeleteBackward() { area.Update(DeleteBackwardAt) }

// TextAreaRender describes rendered editor lines and cursor position.
type TextAreaRender struct {
	Lines     []Line
	CursorCol int
	CursorRow int
}

// TextAreaStyles configures text area rendering.
type TextAreaStyles struct {
	Border tcell.Style
	Body   tcell.Style
}

const (
	textAreaBorderPadding      = 4
	textAreaBorderRows         = 2
	textAreaCursorColumnOffset = 2
)

// Render renders this text area with a border.
func (area *TextArea) Render(width, maxRows int, styles TextAreaStyles) TextAreaRender {
	if area == nil {
		return renderTextArea(nil, 0, width, maxRows, styles)
	}

	return renderTextArea([]rune(area.Text), area.Cursor, width, maxRows, styles)
}

func renderTextArea(value []rune, cursor, width, maxRows int, styles TextAreaStyles) TextAreaRender {
	innerWidth := max(1, width-textAreaBorderPadding)
	bodyLines := TextAreaBodyLines(value, innerWidth)
	cursorRow, cursorColumn := TextAreaCursorPosition(value, cursor, innerWidth)
	visibleLines, skippedRows := VisibleLines(bodyLines, maxRows, cursorRow)
	lines := make([]Line, 0, len(visibleLines)+textAreaBorderRows)
	lines = append(lines, NewLine(styles.Border, TopBorder(width, "")))

	for _, bodyLine := range visibleLines {
		bodyText := PadRight(bodyLine, innerWidth)
		lines = append(lines, Line{
			Text:  "│ " + bodyText + " │",
			Style: styles.Body,
			Spans: []Span{
				{Text: "│", Style: styles.Border},
				{Text: " " + bodyText + " ", Style: styles.Body},
				{Text: "│", Style: styles.Border},
			},
		})
	}

	lines = append(lines, NewLine(styles.Border, BottomBorder(width)))

	return TextAreaRender{
		Lines:     lines,
		CursorCol: textAreaCursorColumnOffset + cursorColumn,
		CursorRow: 1 + cursorRow - skippedRows,
	}
}

// TextAreaBodyLines wraps the body text into display lines.
func TextAreaBodyLines(value []rune, width int) []string {
	if len(value) == 0 {
		return []string{""}
	}

	return WrapPreserveWhitespace(string(value), width)
}

// TextAreaCursorPosition returns the display row/column for cursor.
func TextAreaCursorPosition(value []rune, cursor, width int) (row, column int) {
	cursor = ClampCursor(cursor, len(value))
	prefix := string(value[:cursor])

	lines := WrapPreserveWhitespace(prefix, width)
	if len(lines) == 0 {
		return 0, 0
	}

	lastLine := lines[len(lines)-1]
	if strings.HasSuffix(prefix, "\n") {
		return len(lines) - 1, 0
	}

	return len(lines) - 1, Width(lastLine)
}

// VisibleLines returns the visible viewport for lines and cursor position.
func VisibleLines(lines []string, maxRows, cursorRow int) (visible []string, skippedRows int) {
	if maxRows < 1 || len(lines) <= maxRows {
		return lines, 0
	}

	start := max(0, cursorRow-maxRows+1)
	if start+maxRows > len(lines) {
		start = len(lines) - maxRows
	}

	return lines[start : start+maxRows], start
}

// ClampCursor clamps a rune cursor to the text length.
func ClampCursor(cursor, runeCount int) int {
	if cursor < 0 {
		return 0
	}

	if cursor > runeCount {
		return runeCount
	}

	return cursor
}

// InsertRuneAt inserts char at cursor and returns the next value/cursor.
func InsertRuneAt(value []rune, cursor int, char rune) (next []rune, nextCursor int) {
	if char == 0 {
		return value, cursor
	}

	cursor = ClampCursor(cursor, len(value))
	next = append([]rune{}, value[:cursor]...)
	next = append(next, char)
	next = append(next, value[cursor:]...)

	return next, cursor + 1
}

// MoveCursorLeft moves the cursor one rune left.
func MoveCursorLeft(value []rune, cursor int) int {
	cursor = ClampCursor(cursor, len(value))
	if cursor > 0 {
		return cursor - 1
	}

	return cursor
}

// MoveCursorRight moves the cursor one rune right.
func MoveCursorRight(value []rune, cursor int) int {
	cursor = ClampCursor(cursor, len(value))
	if cursor < len(value) {
		return cursor + 1
	}

	return cursor
}

// DeleteBackwardAt deletes the rune before cursor.
func DeleteBackwardAt(value []rune, cursor int) (next []rune, nextCursor int) {
	cursor = ClampCursor(cursor, len(value))
	if cursor == 0 {
		return value, cursor
	}

	next = append([]rune{}, value[:cursor-1]...)
	next = append(next, value[cursor:]...)

	return next, cursor - 1
}
