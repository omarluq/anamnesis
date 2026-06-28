package terminal

import (
	"context"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/transcript"
)

func newChatApp() *App {
	return newApp(newFakeScreen(80, 24), RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})
}

func TestTranscriptShowsWelcomeWhenEmpty(t *testing.T) {
	t.Parallel()

	app := newChatApp()

	assert.Contains(t, transcriptText(app, 60), "Type a message", "the empty transcript shows the welcome line")
}

func TestComposerInsertsTypedRunesAndReportsEmptiness(t *testing.T) {
	t.Parallel()

	app := newChatApp()
	require.True(t, app.composer.Empty())

	for _, char := range "hello" {
		composerInput(app, string(char), string(char))
	}

	assert.False(t, app.composer.Empty())
	assert.Equal(t, "hello", app.composer.TextValue())
}

func TestComposerIgnoresControlAndEmptyKeys(t *testing.T) {
	t.Parallel()

	app := newChatApp()

	composerInputCtrl(app, "ctrl+a")
	composerInput(app, "f1", "")

	assert.True(t, app.composer.Empty())
}

func TestComposerEditingKeysMoveAndDelete(t *testing.T) {
	t.Parallel()

	app := newChatApp()
	for _, char := range "abc" {
		composerInput(app, string(char), string(char))
	}

	composerInput(app, "left", "")
	composerInput(app, "backspace", "")
	assert.Equal(t, "ac", app.composer.TextValue())

	composerInput(app, "right", "")
	composerInput(app, "backspace", "")
	assert.Equal(t, "a", app.composer.TextValue())
}

func TestSubmitEchoesUserMessageAndClearsComposer(t *testing.T) {
	t.Parallel()

	app := newChatApp()
	for _, char := range "hello world" {
		composerInput(app, string(char), string(char))
	}

	composerInput(app, "enter", "")

	assert.True(t, app.composer.Empty(), "composer is cleared after submit")
	assert.Equal(t, []transcript.Role{transcript.RoleUser}, historyRoles(app), "submit appends a user message")
	assert.Contains(t, transcriptText(app, 60), "hello world", "the submitted message echoes into the transcript")
}

func TestSubmitOfWhitespaceIsNoop(t *testing.T) {
	t.Parallel()

	app := newChatApp()

	composerInput(app, "enter", "")

	for _, char := range "   " {
		composerInput(app, string(char), string(char))
	}

	composerInput(app, "enter", "")

	assert.Empty(t, app.history, "blank submissions do not append a message")
}

func TestComposerSubmitDrivesControllerRunThroughLoop(t *testing.T) {
	t.Parallel()

	const query = "why did it crash"

	screen := newFakeScreen(80, 24)
	ctrl := new(mockController)
	ctrl.On("Start", mock.Anything, query, uint64(1)).
		Return(scriptedTrace(1,
			traceEvent(TraceKindThinking, "investigating", 0, 0, 0, 0),
			traceEvent(TraceKindFinal, "all clear", 6, 9, 1_000_000, 0),
		)).
		Once()

	app := newApp(screen, RunOptions{Trace: nil, Controller: ctrl, Title: defaultTitle})

	done := make(chan error, 1)
	go func() { done <- app.loop(context.Background()) }()

	submitQuery(screen, query)
	awaitContents(t, screen, "all clear")
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))
	require.NoError(t, awaitLoop(t, done))

	ctrl.AssertExpectations(t)
	ctrl.AssertCalled(t, "Start", mock.Anything, query, uint64(1))
	assert.Equal(t, uint64(1), app.runID, "submitting starts run #1")
	assert.True(t, app.composer.Empty(), "the composer clears once the query is submitted")
	assert.Equal(t,
		[]transcript.Role{transcript.RoleUser, transcript.RoleThinking, transcript.RoleAssistant},
		historyRoles(app),
		"the submit, the thinking turn, and the answer build the transcript")
	assert.Contains(t, transcriptText(app, 80), query, "the submitted question echoes into the transcript")
}

func TestDrawRendersFooterTitle(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(40, 16)
	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))

	app := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})
	require.NoError(t, app.loop(context.Background()))

	assert.Contains(t, screen.contents(), defaultTitle, "the footer renders the title")
}

func TestComposerCursorTracksCaret(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(80, 24)
	app := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})

	app.draw()

	startColumn, startRow, startShown := screen.cursor()
	require.True(t, startShown, "the composer caret is shown when the composer is drawn")
	assert.Equal(t, app.caretColumn, startColumn, "the native cursor matches the recorded caret column")
	assert.Equal(t, app.caretRow, startRow, "the native cursor matches the recorded caret row")

	for _, char := range "hi" {
		composerInput(app, string(char), string(char))
	}

	app.draw()

	nextColumn, nextRow, nextShown := screen.cursor()
	assert.True(t, nextShown)
	assert.Equal(t, startRow, nextRow, "the caret stays on the composer row while typing")
	assert.Equal(t, startColumn+2, nextColumn, "the caret advances one column per typed rune")
}

func TestComposerCursorHiddenWhenFrameHasNoArea(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(0, 10)
	app := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})

	app.draw()

	_, _, shown := screen.cursor()
	assert.False(t, shown, "the native cursor is hidden when the frame has no drawable area")
}

func TestShellDrawsIntoTinyScreenWithoutPanic(t *testing.T) {
	t.Parallel()

	assert.NotPanics(t, func() {
		for _, size := range [][2]int{{1, 1}, {3, 2}, {6, 4}} {
			screen := newFakeScreen(size[0], size[1])
			screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))

			app := newApp(screen, RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})
			require.NoError(t, app.loop(context.Background()))
		}
	})
}
