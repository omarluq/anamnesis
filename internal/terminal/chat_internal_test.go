package terminal

import (
	"context"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newChatApp() *App {
	return newApp(newFakeScreen(80, 24), RunOptions{
		Trace:      nil,
		Controller: nil,
		Title:      defaultTitle,
	})
}

func TestChatPaneRenderShowsWelcomeMarkdown(t *testing.T) {
	t.Parallel()

	app := newChatApp()

	lines := chatRender(app, 60, 20)
	require.NotEmpty(t, lines)

	text := strings.Join(lines, "\n")
	assert.Contains(t, text, "Type a message")
}

func TestChatPaneInsertsTypedRunesAndReportsEmptiness(t *testing.T) {
	t.Parallel()

	app := newChatApp()
	require.True(t, app.chat.composerEmpty())

	for _, char := range "hello" {
		composerInput(app, string(char), string(char))
	}

	assert.False(t, app.chat.composerEmpty())
	assert.Equal(t, "hello", app.chat.composer.TextValue())
}

func TestChatPaneIgnoresControlAndEmptyKeys(t *testing.T) {
	t.Parallel()

	app := newChatApp()

	// A ctrl-chord and an empty-text key must not mutate the composer.
	composerInputCtrl(app, "ctrl+a")
	composerInput(app, "f1", "")

	assert.True(t, app.chat.composerEmpty())
}

func TestChatPaneEditingKeysMoveAndDelete(t *testing.T) {
	t.Parallel()

	app := newChatApp()
	for _, char := range "abc" {
		composerInput(app, string(char), string(char))
	}

	// Move the cursor left once then delete backward: "abc" -> "ac".
	composerInput(app, "left", "")
	composerInput(app, "backspace", "")
	assert.Equal(t, "ac", app.chat.composer.TextValue())

	// Move right back to the end and delete the trailing rune: "ac" -> "a".
	composerInput(app, "right", "")
	composerInput(app, "backspace", "")
	assert.Equal(t, "a", app.chat.composer.TextValue())
}

func TestChatPaneSubmitEchoesMessageAndClearsComposer(t *testing.T) {
	t.Parallel()

	app := newChatApp()
	for _, char := range "hello world" {
		composerInput(app, string(char), string(char))
	}

	composerInput(app, "enter", "")

	assert.True(t, app.chat.composerEmpty(), "composer is cleared after submit")

	text := strings.Join(chatRender(app, 60, 40), "\n")
	assert.Contains(t, text, "hello world", "submitted message echoes into the answer view")
	assert.Contains(t, text, "you:", "submitted message is attributed to the user")
}

func TestComposerSubmitDrivesControllerRunThroughLoop(t *testing.T) {
	t.Parallel()

	const query = "why did it crash"

	screen := newFakeScreen(80, 24)
	ctrl := new(mockController)
	ctrl.On("Start", mock.Anything, query, uint64(1)).
		Return(scriptedTrace(1,
			traceEvent(TraceKindTurn, "investigating", 0, 0, 0, 0),
			traceEvent(TraceKindFinal, "all clear", 6, 9, 1_000_000, 0),
		)).
		Once()

	app := newApp(screen, RunOptions{Trace: nil, Controller: ctrl, Title: defaultTitle})

	done := make(chan error, 1)
	go func() { done <- app.loop(context.Background()) }()

	for _, char := range query {
		screen.inject(runeKey(string(char)))
	}

	screen.inject(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	// The run's scripted events flow through the swapped-in channel; wait until the
	// turn line has drained and drawn before quitting so the assertion is not racy.
	awaitContents(t, screen, "investigating")
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))
	require.NoError(t, awaitLoop(t, done))

	ctrl.AssertExpectations(t)
	ctrl.AssertCalled(t, "Start", mock.Anything, query, uint64(1))
	assert.Equal(t, uint64(1), app.runID, "submitting starts run #1")
	assert.True(t, app.chat.composerEmpty(), "the composer clears once the query is submitted")
	assert.Contains(t, traceLines(app), "[turn] investigating",
		"controller trace events reach the trace pane over the swapped channel")
	assert.Contains(t, app.chat.view.Text, query, "the submitted question echoes into the answer view")
}

func TestChatPaneSubmitOfWhitespaceIsNoop(t *testing.T) {
	t.Parallel()

	app := newChatApp()
	before := app.chat.view.Text

	composerInput(app, "enter", "")

	for _, char := range "   " {
		composerInput(app, string(char), string(char))
	}

	composerInput(app, "enter", "")

	assert.Equal(t, before, app.chat.view.Text, "blank submissions do not append to the answer view")
}

func TestChatPaneDrawRendersBorderTitleAndComposer(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(40, 16)
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))

	app := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})
	require.NoError(t, app.loop(context.Background()))

	text := screen.contents()
	assert.Contains(t, text, defaultTitle, "chat box renders its title in the border")
}

func TestChatComposerCursorTracksCaret(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	app := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})

	app.draw()

	startColumn, startRow, startShown := screen.cursor()
	require.True(t, startShown, "composer caret is shown when the composer is drawn")

	wantColumn, wantRow, wantVisible := app.chat.cursorPosition()
	require.True(t, wantVisible)
	assert.Equal(t, wantColumn, startColumn, "native cursor matches the pane's caret column")
	assert.Equal(t, wantRow, startRow, "native cursor matches the pane's caret row")

	for _, char := range "hi" {
		composerInput(app, string(char), string(char))
	}

	app.draw()

	nextColumn, nextRow, nextShown := screen.cursor()
	assert.True(t, nextShown)
	assert.Equal(t, startRow, nextRow, "caret stays on the composer row while typing")
	assert.Equal(t, startColumn+2, nextColumn, "caret advances one column per typed rune")
}

func TestChatComposerCursorHiddenWhenComposerNotDrawn(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(1, 1)
	app := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})

	app.draw()

	_, _, shown := screen.cursor()
	assert.False(t, shown, "native cursor is hidden when the composer has no drawable area")
}

func TestChatShellDrawsIntoTinyScreenWithoutPanic(t *testing.T) {
	t.Parallel()

	assert.NotPanics(t, func() {
		for _, size := range [][2]int{{1, 1}, {3, 2}, {6, 4}} {
			screen := newFakeScreen(size[0], size[1])
			screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))

			app := newApp(screen, RunOptions{
				Trace:      nil,
				Controller: nil,
				Title:      defaultTitle,
			})
			require.NoError(t, app.loop(context.Background()))
		}
	})
}
