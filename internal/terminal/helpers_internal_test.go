package terminal

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/stretchr/testify/mock"

	"github.com/omarluq/anamnesis/internal/tui"
)

// loopTimeout bounds how long a test waits for the run loop to return before
// failing, so a wedged loop fails fast instead of hanging the suite.
const loopTimeout = 2 * time.Second

// fakeScreen is a minimal tcell.Screen used to drive the run loop without a real
// terminal. It embeds tcell.Screen so it satisfies the interface, but only the
// handful of methods the loop actually calls (EventQ, Size, Show, Sync,
// SetContent, ShowCursor and HideCursor) are implemented. Any other method would
// panic on the nil embedded interface, which keeps the test honest about what
// the loop touches.
//
// This stays an embed-the-interface fake rather than a testify mock on purpose:
// tcell.Screen carries ~30 methods, so a full mock would be far noisier than the
// embed, and fakeScreen is not a stub but a stateful recorder — it captures the
// rendered cell buffer plus the Show/Sync/cursor counts that assertions read
// back, which a testify mock cannot model cleanly.
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

// ShowCursor records the latest native cursor placement so tests can assert
// where the composer caret was positioned.
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

// mockController is a testify mock of the Controller seam. Both the live RLM
// adapter (internal/ana/rlm) and this mock satisfy the interface, so the shell
// can drive either a real investigation or a scripted demo through the same seam
// without knowing which it holds. Expectations script the trace channel Start
// replays, and AssertExpectations / AssertCalled confirm the shell drove Start
// with the (query, runID) the test scripted.
//
// Controller is a single-method seam, so a testify mock is a clear win over a
// hand-written stub: .On("Start", ...).Return(channel) scripts the run and the
// built-in call recorder asserts the submit lifecycle with no bespoke
// bookkeeping. The embedded mock.Mock is mutex-guarded, so the -race detector
// stays quiet while the run loop calls Start from its own goroutine.
type mockController struct {
	mock.Mock
}

// Start records the (query, runID) it was driven with and replays the scripted
// channel configured via .On("Start", ...).Return(channel), so tests can both
// assert the shell's submit lifecycle and feed scripted trace events back
// through the loop.
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

// traceEvent builds a fully-populated top-level TraceEvent, keeping table rows
// readable.
func traceEvent(
	kind TraceKind,
	text string,
	tokensIn, tokensOut int,
	micros int64,
	runID uint64,
) TraceEvent {
	return TraceEvent{
		Kind:       kind,
		Text:       text,
		TokensIn:   tokensIn,
		TokensOut:  tokensOut,
		CostMicros: micros,
		Depth:      0,
		RunID:      runID,
	}
}

// traceDepthEvent builds a token-free TraceEvent at sub-call nesting depth so
// indentation assertions bind the rendered prefix to the event's Depth.
func traceDepthEvent(kind TraceKind, text string, depth int) TraceEvent {
	return TraceEvent{
		Kind:       kind,
		Text:       text,
		TokensIn:   0,
		TokensOut:  0,
		CostMicros: 0,
		Depth:      depth,
		RunID:      0,
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

// composerInput feeds a printable key into the chat composer, mirroring the
// normalized key events the run loop routes in from the screen.
func composerInput(app *App, key, text string) {
	app.chat.handleKey(tui.KeyEvent{Key: key, Text: text, Ctrl: false, Alt: false, Shift: false})
}

// composerInputCtrl feeds a ctrl-chorded key into the chat composer.
func composerInputCtrl(app *App, key string) {
	app.chat.handleKey(tui.KeyEvent{Key: key, Text: "", Ctrl: true, Alt: false, Shift: false})
}

// traceLines returns the trace pane's accumulated line texts.
func traceLines(app *App) []string {
	texts := make([]string, 0, len(app.trace.view.Lines))
	for _, line := range app.trace.view.Lines {
		texts = append(texts, line.Text)
	}

	return texts
}

// chatRender returns the rendered answer lines as plain text.
func chatRender(app *App, width, height int) []string {
	lines := app.chat.view.Render(width, height)
	texts := make([]string, 0, len(lines))

	for _, line := range lines {
		texts = append(texts, line.Text)
	}

	return texts
}

// screenRow returns the single rendered row of text that contains label,
// failing the test when no row carries it. It binds a metric to the value on
// its own line so assertions guard token routing instead of bare substrings.
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

// awaitRender waits until the screen has flushed at least want frames so a
// throttled draw (one issued while the loop is working) completes before the
// test inspects the rendered cells.
func awaitRender(t *testing.T, screen *fakeScreen, want int) {
	t.Helper()

	deadline := time.After(loopTimeout)

	for screen.showCount() < want {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for screen render")
		case <-time.After(time.Millisecond):
		}
	}
}

// awaitContents waits until the rendered screen contains want, so a test can
// synchronize on a drained-and-drawn trace line before driving further input.
// It reads the mutex-guarded cell buffer, so polling races with the loop are
// safe under the race detector.
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

// sendTrace posts a trace event with a bounded wait so a wedged loop fails the
// test instead of blocking forever on the unbuffered channel.
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
