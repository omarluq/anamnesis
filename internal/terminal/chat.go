package terminal

import (
	"strings"

	"github.com/omarluq/anamnesis/internal/tui"
)

const (
	composerHeight    = 3
	composerBorders   = 2
	welcomeText       = "Type a message and press Enter to begin.\n"
	submittedQuestion = "\n\n**you:** "
)

// chatPane renders rendered markdown answers above an editable composer.
type chatPane struct {
	view         *tui.MarkdownView
	title        string
	composer     tui.TextArea
	caretColumn  int
	caretRow     int
	theme        Theme
	caretVisible bool
}

// newChatPane returns a chat pane seeded with a welcome message.
func newChatPane(theme Theme, title string) *chatPane {
	composer := tui.NewTextArea()

	return &chatPane{
		view: &tui.MarkdownView{
			Engine: nil,
			Lexer:  nil,
			Text:   welcomeText,
			Styles: theme.MarkdownStyles(),
		},
		composer:     composer,
		title:        title,
		theme:        theme,
		caretColumn:  0,
		caretRow:     0,
		caretVisible: false,
	}
}

// Draw paints the chat box, answer view, and composer into rect.
func (pane *chatPane) Draw(screen tui.ContentSetter, rect tui.Rect) {
	pane.caretVisible = false

	box := tui.NewBox(pane.title)
	box.Style = pane.theme.fg(pane.theme.Border)
	box.Draw(screen, rect)

	inner := rect.Inner(1)
	if inner.Empty() {
		return
	}

	composerRows := min(composerHeight, inner.Height)
	answerHeight := max(0, inner.Height-composerRows)
	answerRect := tui.Rect{X: inner.X, Y: inner.Y, Width: inner.Width, Height: answerHeight}
	composerRect := tui.Rect{X: inner.X, Y: inner.Y + answerHeight, Width: inner.Width, Height: composerRows}

	pane.view.Draw(screen, answerRect)
	pane.drawComposer(screen, composerRect)
}

// render returns the rendered answer lines.
func (pane *chatPane) render(width, height int) []tui.Line {
	return pane.view.Render(width, height)
}

// handleKey routes a normalized key event into the composer and returns the
// query submitted on Enter, or an empty string for any other key.
func (pane *chatPane) handleKey(keyEvent tui.KeyEvent) string {
	switch keyEvent.Key {
	case "enter":
		return pane.submit()
	case "backspace":
		pane.composer.DeleteBackward()
	case "left":
		pane.composer.MoveLeft()
	case "right":
		pane.composer.MoveRight()
	default:
		pane.insert(keyEvent)
	}

	return ""
}

// composerEmpty reports whether the composer currently holds no text.
func (pane *chatPane) composerEmpty() bool {
	return pane.composer.Empty()
}

// drawComposer renders the bordered text area into rect and records where the
// caret landed so the run loop can place the native terminal cursor there.
func (pane *chatPane) drawComposer(screen tui.ContentSetter, rect tui.Rect) {
	if rect.Empty() {
		return
	}

	maxRows := max(1, rect.Height-composerBorders)
	rendered := pane.composer.Render(rect.Width, maxRows, pane.theme.TextAreaStyles())
	tui.DrawLines(screen, rect, rendered.Lines)

	pane.caretColumn = rect.X + min(rendered.CursorCol, rect.Width-1)
	pane.caretRow = rect.Y + min(rendered.CursorRow, rect.Height-1)
	pane.caretVisible = true
}

// cursorPosition reports the absolute screen coordinates of the composer caret
// and whether the composer was drawn this frame, so the run loop can place the
// native terminal cursor there.
func (pane *chatPane) cursorPosition() (column, row int, visible bool) {
	return pane.caretColumn, pane.caretRow, pane.caretVisible
}

// insert appends the printable text of keyEvent to the composer.
func (pane *chatPane) insert(keyEvent tui.KeyEvent) {
	if keyEvent.Ctrl || keyEvent.Text == "" {
		return
	}

	for _, char := range keyEvent.Text {
		pane.composer.InsertRune(char)
	}
}

// submit echoes the trimmed composer text into the answer view, clears the
// composer, and returns the submitted query, or an empty string when the
// composer held only whitespace.
func (pane *chatPane) submit() string {
	text := strings.TrimSpace(pane.composer.Clear())
	if text == "" {
		return ""
	}

	pane.view.Text += submittedQuestion + text

	return text
}
