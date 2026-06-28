package terminal

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/stretchr/testify/mock"

	"github.com/omarluq/anamnesis/internal/transcript"
	"github.com/omarluq/anamnesis/internal/tui"
)

// loopTimeout bounds how long a test waits for the run loop to return before
// failing, so a wedged loop fails fast instead of hanging the suite.
const loopTimeout = 2 * time.Second

// fakeScreen is a minimal tcell.Screen used to drive the run loop without a real
// terminal. It embeds tcell.Screen so it satisfies the interface, but only the
// handful of methods the loop actually calls (EventQ, Size, Show, Sync, SetContent,
// ShowCursor and HideCursor) are implemented. Any other method would panic on the
// nil embedded interface, which keeps the test honest about what the loop touches.
type fakeScreen struct {
	tcell.Screen

	events       chan tcell.Event
	cells        map[[2]int]rune
	mu           sync.Mutex
	width        int
	height       int
	shows        int
	syncs        int
	cursorColumn int
	cursorRow    int
	cursorShown  bool
}

// newFakeScreen returns a fake screen sized width x height with a buffered event
// channel so tests can inject events without blocking.
func newFakeScreen(width, height int) *fakeScreen {
	return &fakeScreen{
		Screen:       nil,
		events:       make(chan tcell.Event, 32),
		cells:        map[[2]int]rune{},
		mu:           sync.Mutex{},
		width:        width,
		height:       height,
		shows:        0,
		syncs:        0,
		cursorColumn: 0,
		cursorRow:    0,
		cursorShown:  false,
	}
}

// EventQ returns the screen's event channel, just like a real tcell screen.
func (screen *fakeScreen) EventQ() chan tcell.Event {
	return screen.events
}

// Size reports the fixed dimensions of the fake screen.
func (screen *fakeScreen) Size() (width, height int) {
	return screen.width, screen.height
}

// Show records a frame flush.
func (screen *fakeScreen) Show() {
	screen.mu.Lock()
	screen.shows++
	screen.mu.Unlock()
}

// Sync records a full re-synchronization (used by resize handling).
func (screen *fakeScreen) Sync() {
	screen.mu.Lock()
	screen.syncs++
	screen.mu.Unlock()
}

// SetContent stores the primary rune written at a cell so tests can read back the
// rendered frame, mirroring GetContents on a real simulation screen.
func (screen *fakeScreen) SetContent(column, row int, primary rune, _ []rune, _ tcell.Style) {
	if primary == 0 {
		primary = ' '
	}

	screen.mu.Lock()
	screen.cells[[2]int{column, row}] = primary
	screen.mu.Unlock()
}

// ShowCursor records the latest native cursor placement so tests can assert where
// the composer caret was positioned.
func (screen *fakeScreen) ShowCursor(column, row int) {
	screen.mu.Lock()
	screen.cursorColumn = column
	screen.cursorRow = row
	screen.cursorShown = true
	screen.mu.Unlock()
}

// HideCursor records that the native cursor was hidden this frame.
func (screen *fakeScreen) HideCursor() {
	screen.mu.Lock()
	screen.cursorShown = false
	screen.mu.Unlock()
}

// cursor returns the last recorded native cursor placement and visibility.
func (screen *fakeScreen) cursor() (column, row int, shown bool) {
	screen.mu.Lock()
	defer screen.mu.Unlock()

	return screen.cursorColumn, screen.cursorRow, screen.cursorShown
}

// inject pushes an event onto the screen's event channel.
func (screen *fakeScreen) inject(event tcell.Event) {
	screen.events <- event
}

// showCount returns how many times Show was called.
func (screen *fakeScreen) showCount() int {
	screen.mu.Lock()
	defer screen.mu.Unlock()

	return screen.shows
}

// syncCount returns how many times Sync was called.
func (screen *fakeScreen) syncCount() int {
	screen.mu.Lock()
	defer screen.mu.Unlock()

	return screen.syncs
}

// contents returns the rendered screen as newline-joined rows, defaulting unset
// cells to spaces.
func (screen *fakeScreen) contents() string {
	screen.mu.Lock()
	defer screen.mu.Unlock()

	var builder strings.Builder

	for row := range screen.height {
		for column := range screen.width {
			glyph := screen.cells[[2]int{column, row}]
			if glyph == 0 {
				glyph = ' '
			}

			builder.WriteRune(glyph)
		}

		builder.WriteByte('\n')
	}

	return builder.String()
}

// mockController is a testify mock of the Controller seam. Expectations script the
// trace channel Start replays, and AssertExpectations / AssertCalled confirm the
// shell drove Start with the (query, runID) the test scripted.
type mockController struct {
	mock.Mock
}

// Start records the (query, runID) it was driven with and replays the scripted
// channel configured via .On("Start", ...).Return(channel).
func (m *mockController) Start(ctx context.Context, query string, runID uint64) <-chan TraceEvent {
	args := m.Called(ctx, query, runID)

	channel, ok := args.Get(0).(<-chan TraceEvent)
	if !ok {
		return nil
	}

	return channel
}

// compile-time assertion that mockController satisfies the Controller seam.
var _ Controller = (*mockController)(nil)

// runeKey constructs a tcell printable-rune key event.
func runeKey(text string) *tcell.EventKey {
	return tcell.NewEventKey(tcell.KeyRune, text, tcell.ModNone)
}

// traceEvent builds a top-level TraceEvent stamped with runID, keeping table rows
// readable.
func traceEvent(kind TraceKind, text string, runID uint64) TraceEvent {
	return TraceEvent{
		Kind:    kind,
		Text:    text,
		Err:     "",
		Depth:   0,
		RunID:   runID,
		QueryID: 0,
	}
}

// traceDepthEvent builds a TraceEvent at the given recursion depth so indentation
// assertions bind the rendered prefix to the event's Depth.
func traceDepthEvent(kind TraceKind, text string, depth int) TraceEvent {
	return TraceEvent{
		Kind:    kind,
		Text:    text,
		Err:     "",
		Depth:   depth,
		RunID:   0,
		QueryID: 0,
	}
}

// scriptedTrace returns a closed, buffered channel replaying events stamped with
// runID, standing in for a controller run's trace stream in mockController setup.
func scriptedTrace(runID uint64, events ...TraceEvent) <-chan TraceEvent {
	out := make(chan TraceEvent, len(events))
	for _, event := range events {
		event.RunID = runID
		out <- event
	}

	close(out)

	return out
}

// composerInput feeds a printable or editing key into the composer, mirroring the
// normalized key events the run loop routes in from the screen.
func composerInput(app *App, key, text string) {
	app.composerKey(tui.KeyEvent{Key: key, Text: text, Ctrl: false, Alt: false, Shift: false})
}

// composerInputCtrl feeds a ctrl-chorded key into the composer; the composer
// ignores ctrl chords, so this proves they do not mutate the buffer.
func composerInputCtrl(app *App, key string) {
	app.composerKey(tui.KeyEvent{Key: key, Text: "", Ctrl: true, Alt: false, Shift: false})
}

// toggleKey routes the ctrl+o query-block toggle through the real key dispatch — the
// same handleKey path the run loop uses — and reports whether it flipped query-block
// expansion, proving the dispatch consumes the toggle before the composer sees it.
func toggleKey(app *App, key string) bool {
	expandBefore := app.toolsExpanded
	app.handleKey(context.Background(), ctrlKey(key))

	return app.toolsExpanded != expandBefore
}

// ctrlKey builds the tcell event for the ctrl+o query-block toggle.
func ctrlKey(key string) *tcell.EventKey {
	if key == "ctrl+o" {
		return tcell.NewEventKey(tcell.KeyCtrlO, "", tcell.ModNone)
	}

	return tcell.NewEventKey(tcell.KeyRune, key, tcell.ModNone)
}

// transcriptText renders the full transcript at width and joins every line's visible
// text, so assertions can scan the rendered conversation as a single string. A line
// carries its text either in Text or, once syntax-highlighted/styled, split across
// Spans — concatenate the spans so highlighted output is not invisible to asserts.
func transcriptText(app *App, width int) string {
	var builder strings.Builder

	for _, line := range app.transcriptLines(width) {
		if len(line.Spans) == 0 {
			builder.WriteString(line.Text)
		} else {
			for _, span := range line.Spans {
				builder.WriteString(span.Text)
			}
		}

		builder.WriteByte('\n')
	}

	return builder.String()
}

// historyRoles returns the role of each transcript message in order, so structural
// assertions read the conversation shape rather than rendered substrings.
func historyRoles(app *App) []transcript.Role {
	roles := make([]transcript.Role, 0, len(app.history))
	for _, message := range app.history {
		roles = append(roles, message.Role)
	}

	return roles
}

// submitQuery types text into the composer one rune at a time and presses Enter,
// driving the shell's real submit path through the injected event channel.
func submitQuery(screen *fakeScreen, text string) {
	for _, char := range text {
		screen.inject(runeKey(string(char)))
	}

	screen.inject(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
}

// screenRow returns the single rendered row of text that contains label, failing
// the test when no row carries it.
func screenRow(t *testing.T, text, label string) string {
	t.Helper()

	for row := range strings.SplitSeq(text, "\n") {
		if strings.Contains(row, label) {
			return row
		}
	}

	t.Fatalf("no rendered row contains %q", label)

	return ""
}

// awaitContents waits until the rendered screen contains want, so a test can
// synchronize on a drained-and-drawn transcript line before driving further input.
func awaitContents(t *testing.T, screen *fakeScreen, want string) {
	t.Helper()

	deadline := time.After(loopTimeout)

	for !strings.Contains(screen.contents(), want) {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %q to render", want)
		case <-time.After(time.Millisecond):
		}
	}
}

// sendTrace posts a trace event with a bounded wait so a wedged loop fails the test
// instead of blocking forever on the unbuffered channel.
func sendTrace(t *testing.T, channel chan<- TraceEvent, event TraceEvent) {
	t.Helper()

	select {
	case channel <- event:
	case <-time.After(loopTimeout):
		t.Fatal("timed out posting trace event to run loop")
	}
}

// awaitLoop waits for the loop's result with a bounded timeout.
func awaitLoop(t *testing.T, done <-chan error) error {
	t.Helper()

	select {
	case err := <-done:
		return err
	case <-time.After(loopTimeout):
		t.Fatal("run loop did not return")

		return nil
	}
}
