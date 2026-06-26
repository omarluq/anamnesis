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
	assert.Contains(t, text, "anamnesis")
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
	assert.Contains(t, text, terminal.ComposerLabel, "composer renders its label")
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
