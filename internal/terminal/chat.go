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
	view     *tui.MarkdownView
	title    string
	composer tui.TextArea
	theme    Theme
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
		composer: composer,
		title:    title,
		theme:    theme,
	}
}

// Draw paints the chat box, answer view, and composer into rect.
func (pane *chatPane) Draw(screen tui.ContentSetter, rect tui.Rect) {
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

// handleKey routes a normalized key event into the composer.
func (pane *chatPane) handleKey(keyEvent tui.KeyEvent) {
	switch keyEvent.Key {
	case "enter":
		pane.submit()
	case "backspace":
		pane.composer.DeleteBackward()
	case "left":
		pane.composer.MoveLeft()
	case "right":
		pane.composer.MoveRight()
	default:
		pane.insert(keyEvent)
	}
}

// composerEmpty reports whether the composer currently holds no text.
func (pane *chatPane) composerEmpty() bool {
	return pane.composer.Empty()
}

// drawComposer renders the bordered text area into rect.
func (pane *chatPane) drawComposer(screen tui.ContentSetter, rect tui.Rect) {
	if rect.Empty() {
		return
	}

	maxRows := max(1, rect.Height-composerBorders)
	rendered := pane.composer.Render(rect.Width, maxRows, pane.theme.TextAreaStyles())
	tui.DrawLines(screen, rect, rendered.Lines)
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

// submit echoes the composer text into the answer view and clears the composer.
func (pane *chatPane) submit() {
	text := strings.TrimSpace(pane.composer.Clear())
	if text == "" {
		return
	}

	pane.view.Text += submittedQuestion + text
}
