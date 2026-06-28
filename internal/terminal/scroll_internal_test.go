package terminal

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/tui"
)

// appendUserMessages appends count user messages carrying distinctive MSG## tokens,
// stacking enough rendered lines to overflow a short transcript window.
func appendUserMessages(app *App, count int) {
	for index := range count {
		app.appendUser(fmt.Sprintf("MSG%02d body text", index))
	}
}

// renderedFrameText joins every cell rune of frame into newline-separated rows so a
// test can scan a rendered transcript window as a single string.
func renderedFrameText(frame *tui.CellBuffer) string {
	var builder strings.Builder

	for row := range frame.Height() {
		for column := range frame.Width() {
			builder.WriteRune(frame.Cell(column, row).Rune)
		}

		builder.WriteByte('\n')
	}

	return builder.String()
}

// TestDrawTranscriptFollowsTailAtZeroScroll pins the follow-mode default: with the
// offset at zero the window shows the last rect.Height lines (the newest message),
// exactly as the previous tui.Tail anchoring did.
func TestDrawTranscriptFollowsTailAtZeroScroll(t *testing.T) {
	t.Parallel()

	const (
		width  = 40
		height = 5
	)

	app := newApp(newFakeScreen(width, 24), RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})
	appendUserMessages(app, 12)
	require.Greater(t, len(app.transcriptLines(width)), height, "the transcript must overflow the window")

	app.scroll = 0

	frame := tui.NewCellBuffer(width, height, app.theme.fg(app.theme.Text))
	app.drawTranscript(frame, tui.Rect{X: 0, Y: 0, Width: width, Height: height})

	rendered := renderedFrameText(frame)
	assert.Contains(t, rendered, "MSG11", "scroll 0 follows the tail and shows the newest message")
	assert.NotContains(t, rendered, "MSG00", "scroll 0 hides the oldest message above the window")
	assert.Zero(t, app.scroll, "follow mode keeps the offset pinned at zero")
}

// TestDrawTranscriptClampsScrollAndRevealsTop pins the per-frame clamp: an
// overshooting offset is capped at totalLines-height, which lifts the window all the
// way to the oldest message.
func TestDrawTranscriptClampsScrollAndRevealsTop(t *testing.T) {
	t.Parallel()

	const (
		width  = 40
		height = 5
	)

	app := newApp(newFakeScreen(width, 24), RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})
	appendUserMessages(app, 12)

	maxOffset := len(app.transcriptLines(width)) - height
	require.Positive(t, maxOffset, "the transcript must overflow so there is room to scroll")

	app.scroll = 1 << 20

	frame := tui.NewCellBuffer(width, height, app.theme.fg(app.theme.Text))
	app.drawTranscript(frame, tui.Rect{X: 0, Y: 0, Width: width, Height: height})

	assert.Equal(t, maxOffset, app.scroll, "the per-frame clamp caps the offset at totalLines-height")
	assert.Contains(t, renderedFrameText(frame), "MSG00", "fully scrolled up reveals the oldest message")
}

// TestDrawTranscriptHoldsViewportWhileStreaming pins that appending new lines while
// the user is scrolled up holds their viewport in place instead of letting it drift
// toward the tail: the offset grows by the transcript's line growth so the same
// window stays in view.
func TestDrawTranscriptHoldsViewportWhileStreaming(t *testing.T) {
	t.Parallel()

	const (
		width  = 40
		height = 5
	)

	app := newApp(newFakeScreen(width, 24), RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})
	appendUserMessages(app, 12)

	rect := tui.Rect{X: 0, Y: 0, Width: width, Height: height}

	before := tui.NewCellBuffer(width, height, app.theme.fg(app.theme.Text))
	app.scroll = 4
	app.drawTranscript(before, rect)
	linesBefore := app.prevTotalLines

	appendUserMessages(app, 6)

	after := tui.NewCellBuffer(width, height, app.theme.fg(app.theme.Text))
	app.drawTranscript(after, rect)

	grown := app.prevTotalLines - linesBefore
	require.Positive(t, grown, "appending messages grows the transcript")
	assert.Equal(t, 4+grown, app.scroll, "the offset grows by the transcript growth so the viewport holds")
	assert.Equal(t, renderedFrameText(before), renderedFrameText(after),
		"the same window stays in view while new lines stream in")
}

// TestScrollByLiftsTheWindow pins that a positive delta lifts the window and that
// deltas accumulate.
func TestScrollByLiftsTheWindow(t *testing.T) {
	t.Parallel()

	app := newTestApp()

	app.scrollBy(3)
	assert.Equal(t, 3, app.scroll, "a positive delta lifts the window")

	app.scrollBy(transcriptScrollPage)
	assert.Equal(t, 3+transcriptScrollPage, app.scroll, "deltas accumulate")
	assert.True(t, app.dirty, "a scroll marks the frame dirty so it redraws")
}

// TestScrollByCannotGoBelowZero pins the bottom stop: scrolling down past the tail
// clamps the offset at zero rather than going negative.
func TestScrollByCannotGoBelowZero(t *testing.T) {
	t.Parallel()

	app := newTestApp()
	app.dirty = false

	app.scrollBy(-transcriptScrollPage)
	assert.Zero(t, app.scroll, "scrolling down past the tail clamps at zero")
	assert.True(t, app.dirty, "a scroll marks the frame dirty so it redraws")
}

// TestSubmitSnapsScrollToBottom pins that starting a new turn snaps the transcript
// back to follow mode so the user sees their fresh prompt.
func TestSubmitSnapsScrollToBottom(t *testing.T) {
	t.Parallel()

	app := newTestApp()
	app.scroll = 9

	for _, char := range "hello" {
		app.composer.InsertRune(char)
	}

	query := app.submit()
	require.Equal(t, "hello", query)
	assert.Zero(t, app.scroll, "submitting a new turn snaps the transcript back to follow mode")
}

// TestScrollKeysAdjustOffset drives the real handleKey path to prove the key.go
// PageUp/PageDown mapping is wired and that the arrows nudge the offset by one line.
func TestScrollKeysAdjustOffset(t *testing.T) {
	t.Parallel()

	app := newTestApp()
	ctx := context.Background()

	require.False(t, app.handleKey(ctx, tcell.NewEventKey(tcell.KeyPgUp, "", tcell.ModNone)))
	assert.Equal(t, transcriptScrollPage, app.scroll, "PageUp lifts the window a page (proves the key.go mapping)")

	require.False(t, app.handleKey(ctx, tcell.NewEventKey(tcell.KeyUp, "", tcell.ModNone)))
	assert.Equal(t, transcriptScrollPage+1, app.scroll, "the up arrow nudges one line")

	require.False(t, app.handleKey(ctx, tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone)))
	assert.Equal(t, transcriptScrollPage, app.scroll, "the down arrow lowers one line")

	require.False(t, app.handleKey(ctx, tcell.NewEventKey(tcell.KeyPgDn, "", tcell.ModNone)))
	assert.Zero(t, app.scroll, "PageDown lowers a page back to the tail")
}

// TestWheelEventsScrollTranscript drives the real handleEvent path to prove the
// EventMouse case is wired and that wheel motion scrolls the transcript.
func TestWheelEventsScrollTranscript(t *testing.T) {
	t.Parallel()

	app := newTestApp()
	ctx := context.Background()

	require.False(t, app.handleEvent(ctx, tcell.NewEventMouse(0, 0, tcell.WheelUp, tcell.ModNone)))
	assert.Equal(t, transcriptWheelStep, app.scroll, "a wheel-up event lifts the transcript")

	require.False(t, app.handleEvent(ctx, tcell.NewEventMouse(0, 0, tcell.WheelDown, tcell.ModNone)))
	assert.Zero(t, app.scroll, "a wheel-down event lowers it back toward the tail")
}
