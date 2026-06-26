package terminal_test

import (
	"context"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/terminal"
)

func newChatApp() *terminal.App {
	return terminal.NewApp(newFakeScreen(80, 24), terminal.RunOptions{Trace: nil, Title: terminal.DefaultTitle})
}

func TestChatPaneRenderShowsWelcomeMarkdown(t *testing.T) {
	t.Parallel()

	app := newChatApp()

	lines := app.ChatRender(60, 20)
	require.NotEmpty(t, lines)

	text := strings.Join(lines, "\n")
	assert.Contains(t, text, "Type a message")
}

func TestChatPaneInsertsTypedRunesAndReportsEmptiness(t *testing.T) {
	t.Parallel()

	app := newChatApp()
	require.True(t, app.ComposerEmpty())

	for _, char := range "hello" {
		app.ComposerInput(string(char), string(char))
	}

	assert.False(t, app.ComposerEmpty())
	assert.Equal(t, "hello", app.ComposerText())
}

func TestChatPaneIgnoresControlAndEmptyKeys(t *testing.T) {
	t.Parallel()

	app := newChatApp()

	// A ctrl-chord and an empty-text key must not mutate the composer.
	app.ComposerInputCtrl("ctrl+a")
	app.ComposerInput("f1", "")

	assert.True(t, app.ComposerEmpty())
}

func TestChatPaneEditingKeysMoveAndDelete(t *testing.T) {
	t.Parallel()

	app := newChatApp()
	for _, char := range "abc" {
		app.ComposerInput(string(char), string(char))
	}

	// Move the cursor left once then delete backward: "abc" -> "ac".
	app.ComposerInput("left", "")
	app.ComposerInput("backspace", "")
	assert.Equal(t, "ac", app.ComposerText())

	// Move right back to the end and delete the trailing rune: "ac" -> "a".
	app.ComposerInput("right", "")
	app.ComposerInput("backspace", "")
	assert.Equal(t, "a", app.ComposerText())
}

func TestChatPaneSubmitEchoesMessageAndClearsComposer(t *testing.T) {
	t.Parallel()

	app := newChatApp()
	for _, char := range "hello world" {
		app.ComposerInput(string(char), string(char))
	}

	app.ComposerInput("enter", "")

	assert.True(t, app.ComposerEmpty(), "composer is cleared after submit")

	text := strings.Join(app.ChatRender(60, 40), "\n")
	assert.Contains(t, text, "hello world", "submitted message echoes into the answer view")
	assert.Contains(t, text, "you:", "submitted message is attributed to the user")
}

func TestChatPaneSubmitOfWhitespaceIsNoop(t *testing.T) {
	t.Parallel()

	app := newChatApp()
	before := app.AnswerText()

	app.ComposerInput("enter", "")

	for _, char := range "   " {
		app.ComposerInput(string(char), string(char))
	}

	app.ComposerInput("enter", "")

	assert.Equal(t, before, app.AnswerText(), "blank submissions do not append to the answer view")
}

func TestChatPaneDrawRendersBorderTitleAndComposer(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(40, 16)
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))

	app := terminal.NewApp(screen, terminal.RunOptions{Trace: nil, Title: terminal.DefaultTitle})
	require.NoError(t, app.Loop(context.Background()))

	text := screen.contents()
	assert.Contains(t, text, terminal.DefaultTitle, "chat box renders its title in the border")
}

func TestChatComposerCursorTracksCaret(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	app := terminal.NewApp(screen, terminal.RunOptions{Trace: nil, Title: terminal.DefaultTitle})

	app.Draw()

	startColumn, startRow, startShown := screen.cursor()
	require.True(t, startShown, "composer caret is shown when the composer is drawn")

	wantColumn, wantRow, wantVisible := app.CursorPosition()
	require.True(t, wantVisible)
	assert.Equal(t, wantColumn, startColumn, "native cursor matches the pane's caret column")
	assert.Equal(t, wantRow, startRow, "native cursor matches the pane's caret row")

	for _, char := range "hi" {
		app.ComposerInput(string(char), string(char))
	}

	app.Draw()

	nextColumn, nextRow, nextShown := screen.cursor()
	assert.True(t, nextShown)
	assert.Equal(t, startRow, nextRow, "caret stays on the composer row while typing")
	assert.Equal(t, startColumn+2, nextColumn, "caret advances one column per typed rune")
}

func TestChatComposerCursorHiddenWhenComposerNotDrawn(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(1, 1)
	app := terminal.NewApp(screen, terminal.RunOptions{Trace: nil, Title: terminal.DefaultTitle})

	app.Draw()

	_, _, shown := screen.cursor()
	assert.False(t, shown, "native cursor is hidden when the composer has no drawable area")
}

func TestChatShellDrawsIntoTinyScreenWithoutPanic(t *testing.T) {
	t.Parallel()

	assert.NotPanics(t, func() {
		for _, size := range [][2]int{{1, 1}, {3, 2}, {6, 4}} {
			screen := newFakeScreen(size[0], size[1])
			screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))

			app := terminal.NewApp(screen, terminal.RunOptions{Trace: nil, Title: terminal.DefaultTitle})
			require.NoError(t, app.Loop(context.Background()))
		}
	})
}
