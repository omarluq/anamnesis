package terminal_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"

	"github.com/omarluq/anamnesis/internal/terminal"
)

// loopTimeout bounds how long a test waits for the run loop to return before
// failing, so a wedged loop fails fast instead of hanging the suite.
const loopTimeout = 2 * time.Second

// fakeScreen is a minimal tcell.Screen used to drive the run loop without a real
// terminal. It embeds tcell.Screen so it satisfies the interface, but only the
// handful of methods the loop actually calls (EventQ, Size, Show, Sync and
// SetContent) are implemented. Any other method would panic on the nil embedded
// interface, which keeps the test honest about what the loop touches.
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

// runeKey constructs a tcell printable-rune key event.
func runeKey(text string) *tcell.EventKey {
	return tcell.NewEventKey(tcell.KeyRune, text, tcell.ModNone)
}

// traceEvent builds a fully-populated TraceEvent, keeping table rows readable.
func traceEvent(
	kind terminal.TraceKind,
	text string,
	tokensIn, tokensOut int,
	micros int64,
	runID uint64,
) terminal.TraceEvent {
	return terminal.TraceEvent{
		Kind:       kind,
		Text:       text,
		TokensIn:   tokensIn,
		TokensOut:  tokensOut,
		CostMicros: micros,
		RunID:      runID,
	}
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

// sendTrace posts a trace event with a bounded wait so a wedged loop fails the
// test instead of blocking forever on the unbuffered channel.
func sendTrace(t *testing.T, channel chan<- terminal.TraceEvent, event terminal.TraceEvent) {
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
