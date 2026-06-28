// Package terminal implements the anamnesis interactive chat shell: a single
// mutable App struct driven by one select loop. Background controllers post typed
// TraceEvents onto a channel and the loop goroutine owns all UI mutation,
// translating each event into a transcript message and rendering each frame into a
// fresh tui.CellBuffer that the renderer diffs against the previous frame. The
// shell is a single scrolling chat transcript over a composer and a status footer:
// user prompts, assistant markdown, collapsible thinking, and color-coded
// recursive query blocks.
package terminal

import (
	"context"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/samber/mo"
	"github.com/samber/oops"

	"github.com/omarluq/anamnesis/internal/tui"
)

const (
	frameInterval   = 8 * time.Millisecond
	spinnerInterval = 120 * time.Millisecond

	defaultTitle = "anamnesis"
)

// spinnerFrames is the immutable braille spinner rune cycle shown while a run is
// in flight.
var spinnerFrames = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

// RunOptions configures a shell run.
type RunOptions struct {
	// Trace is an optional channel of controller events the transcript consumes.
	// When nil the shell starts with an empty transcript and no live run wired.
	Trace <-chan TraceEvent
	// Controller is the optional submit seam the composer drives to start an
	// investigation: submitting a query calls Controller.Start for the new run.
	// When nil the shell echoes the prompt but starts no run.
	Controller Controller
	// Title is the label shown in the status footer.
	Title string
}

// App is the mutable shell state owned by the single loop goroutine.
type App struct {
	screen     tcell.Screen
	renderer   *tui.Renderer
	controller Controller
	cancel     context.CancelFunc
	traceCh    <-chan TraceEvent
	title      string
	history    []chatMessage
	composer   tui.TextArea
	theme      Theme

	caretColumn  int
	caretRow     int
	tokensIn     int
	tokensOut    int
	costMicros   int64
	runID        uint64
	spinnerFrame int

	caretVisible  bool
	toolsExpanded bool
	dirty         bool
	working       bool
}

// newApp builds an App bound to screen with the default theme. Query blocks start
// unexpanded, matching the chat's low-noise default; thinking is always shown.
func newApp(screen tcell.Screen, opts RunOptions) *App {
	title := mo.EmptyableToOption(opts.Title).OrElse(defaultTitle)

	return &App{
		screen:        screen,
		renderer:      tui.NewRenderer(screen),
		controller:    opts.Controller,
		cancel:        nil,
		traceCh:       opts.Trace,
		title:         title,
		history:       nil,
		composer:      tui.NewTextArea(),
		theme:         DefaultTheme(),
		caretColumn:   0,
		caretRow:      0,
		tokensIn:      0,
		tokensOut:     0,
		costMicros:    0,
		runID:         0,
		spinnerFrame:  0,
		caretVisible:  false,
		toolsExpanded: false,
		dirty:         false,
		working:       false,
	}
}

// Run creates a real terminal screen and drives the shell until the user quits or
// ctx is canceled.
func Run(ctx context.Context, opts RunOptions) error {
	screen, err := tcell.NewScreen()
	if err != nil {
		return oops.In("terminal").Code("screen_create").Wrapf(err, "create screen")
	}

	if initErr := screen.Init(); initErr != nil {
		return oops.In("terminal").Code("screen_init").Wrapf(initErr, "init screen")
	}

	defer screen.Fini()

	return run(ctx, screen, opts)
}

// loop is the single select loop that owns all UI mutation.
func (app *App) loop(ctx context.Context) error {
	spinner := time.NewTicker(spinnerInterval)
	defer spinner.Stop()

	frame := time.NewTicker(frameInterval)
	defer frame.Stop()

	defer app.cancelRun()

	app.dirty = true

	for {
		app.drawIfDirty()

		select {
		case <-ctx.Done():
			return nil
		case event := <-app.screen.EventQ():
			if event == nil || app.handleEvent(ctx, event) {
				return nil
			}

			app.dirty = true
		case event, ok := <-app.traceCh:
			app.handleTrace(event, ok)
		case <-tickWhen(app.working, spinner.C):
			app.spinnerFrame++
			app.dirty = true
		case <-tickWhen(app.dirty, frame.C):
			app.draw()
			app.dirty = false
		}
	}
}

// drawIfDirty renders a pending frame unless a working controller has handed frame
// throttling to the frame ticker.
func (app *App) drawIfDirty() {
	if app.dirty && !app.working {
		app.draw()
		app.dirty = false
	}
}

// handleTrace applies a trace event, detaching the channel and clearing the
// working state when it closes, and dropping events whose RunID does not match the
// active run.
func (app *App) handleTrace(event TraceEvent, ok bool) {
	if !ok {
		app.traceCh = nil
		app.working = false
		app.dirty = true

		return
	}

	if event.RunID == app.runID {
		app.applyTrace(event)
		app.dirty = true
	}
}

// applyTrace translates a trace event into a transcript mutation: thinking turns
// and query starts mark the loop busy and append their blocks, a query end
// completes its pending block, a final answer clears the busy state and appends the
// assistant markdown, and usage accumulates into the footer totals.
func (app *App) applyTrace(event TraceEvent) {
	switch event.Kind {
	case TraceKindUsage:
		app.tokensIn += event.TokensIn
		app.tokensOut += event.TokensOut
		app.costMicros += event.CostMicros
	case TraceKindThinking:
		app.working = true

		app.appendThinking(event.Text)
	case TraceKindQueryStart:
		app.working = true

		app.appendQueryStart(event.Text, event.Depth)
	case TraceKindQueryEnd:
		app.completeQuery(event.Text, event.Depth)
	case TraceKindFinal:
		app.working = false

		app.appendAssistant(event.Text)
	}
}

// draw renders one frame into a fresh buffer and flushes the diff to screen.
func (app *App) draw() {
	width, height := app.screen.Size()
	frame := tui.NewCellBuffer(width, height, app.theme.fg(app.theme.Text))
	app.renderFrame(frame, width, height)
	app.renderer.Flush(frame)
	app.placeCursor()
	app.screen.Show()
}

// renderFrame lays out the chat-only shell — transcript, composer, status footer —
// top to bottom into frame, recording where the composer caret landed.
func (app *App) renderFrame(frame *tui.CellBuffer, width, height int) {
	app.caretVisible = false

	if width <= 0 || height <= 0 {
		return
	}

	editor := app.composer.Render(width, app.composerBody(height), app.theme.TextAreaStyles())
	footerHeight := footerHeightFor(height, len(editor.Lines))
	composerHeight := min(len(editor.Lines), height-footerHeight)
	transcriptHeight := height - footerHeight - composerHeight

	app.drawTranscript(frame, tui.Rect{X: 0, Y: 0, Width: width, Height: transcriptHeight})
	app.drawComposer(frame, editor, tui.Rect{
		X: 0, Y: transcriptHeight, Width: width, Height: composerHeight,
	})
	app.drawFooter(frame, tui.Rect{X: 0, Y: height - footerHeight, Width: width, Height: footerHeight})
}

// drawTranscript renders the message history (or the welcome line) bottom-anchored
// into rect.
func (app *App) drawTranscript(frame *tui.CellBuffer, rect tui.Rect) {
	if rect.Empty() {
		return
	}

	tui.DrawLines(frame, rect, tui.Tail(app.transcriptLines(rect.Width), rect.Height))
}

// transcriptLines renders every history message into one stacked line slice, or
// the welcome line when the transcript is empty.
func (app *App) transcriptLines(width int) []tui.Line {
	if len(app.history) == 0 {
		return app.renderMarkdown(welcomeText, width)
	}

	lines := make([]tui.Line, 0, len(app.history)*messageMetadataRows)
	for _, message := range app.history {
		lines = append(lines, app.renderMessage(width, message)...)
	}

	return lines
}

// drawComposer renders the bordered composer into rect and records the caret.
func (app *App) drawComposer(frame *tui.CellBuffer, editor tui.TextAreaRender, rect tui.Rect) {
	if rect.Empty() {
		return
	}

	tui.DrawLines(frame, rect, editor.Lines)

	app.caretColumn = rect.X + min(editor.CursorCol, max(0, rect.Width-1))
	app.caretRow = rect.Y + min(editor.CursorRow, max(0, rect.Height-1))
	app.caretVisible = true
}

// drawFooter renders the status footer into rect.
func (app *App) drawFooter(frame *tui.CellBuffer, rect tui.Rect) {
	if rect.Empty() {
		return
	}

	tui.DrawLine(frame, rect, app.footerLine(rect.Width))
}

// composerBody is the editable body height of the composer, clamped so the
// composer and footer still fit on a short screen.
func (app *App) composerBody(height int) int {
	available := height - footerRows - composerBorders

	return max(1, min(composerBodyRows, available))
}

// placeCursor moves the native terminal cursor to the composer caret, hiding it
// when the composer was not drawn this frame.
func (app *App) placeCursor() {
	if !app.caretVisible {
		app.screen.HideCursor()

		return
	}

	app.screen.ShowCursor(app.caretColumn, app.caretRow)
}

// handleEvent dispatches a screen event and reports whether the shell quits.
func (app *App) handleEvent(ctx context.Context, event tcell.Event) bool {
	switch typed := event.(type) {
	case *tcell.EventResize:
		app.onResize()

		return false
	case *tcell.EventKey:
		return app.handleKey(ctx, typed)
	default:
		return false
	}
}

// handleKey applies the priority chain: quit keys first, then the collapse
// toggles, then the composer, starting a controller run for any submitted query.
func (app *App) handleKey(ctx context.Context, event *tcell.EventKey) bool {
	keyEvent, ok := tui.NewKeyEvent(event)
	if !ok {
		return false
	}

	if app.isQuitKey(keyEvent) {
		return true
	}

	if app.applyToggle(keyEvent) {
		return false
	}

	if query := app.composerKey(keyEvent); query != "" {
		app.startRun(ctx, query)
	}

	return false
}

// isQuitKey reports whether keyEvent should terminate the shell.
func (app *App) isQuitKey(keyEvent tui.KeyEvent) bool {
	switch keyEvent.Key {
	case "ctrl+c", "escape":
		return true
	case "q":
		return app.composer.Empty()
	default:
		return false
	}
}

// applyToggle handles the query-block collapse toggle and reports whether it
// consumed the key: ctrl+o flips query-block expansion. Thinking is always shown in
// full, so there is no thinking toggle.
func (app *App) applyToggle(keyEvent tui.KeyEvent) bool {
	if keyEvent.Key == "ctrl+o" {
		app.toolsExpanded = !app.toolsExpanded

		return true
	}

	return false
}

// composerKey routes a normalized key into the composer and returns the query
// submitted on Enter, or an empty string for any other key.
func (app *App) composerKey(keyEvent tui.KeyEvent) string {
	switch keyEvent.Key {
	case "enter":
		return app.submit()
	case "backspace":
		app.composer.DeleteBackward()
	case "left":
		app.composer.MoveLeft()
	case "right":
		app.composer.MoveRight()
	default:
		app.insert(keyEvent)
	}

	return ""
}

// submit appends the trimmed composer text as a user message, clears the composer,
// and returns the submitted query, or an empty string when it held only whitespace
// or a run is already active. Enter is ignored mid-run so it neither clears the
// composer nor echoes a phantom user message for a request startRun would refuse.
func (app *App) submit() string {
	if app.working {
		return ""
	}

	return app.appendUser(app.composer.Clear())
}

// insert appends the printable text of keyEvent to the composer.
func (app *App) insert(keyEvent tui.KeyEvent) {
	if keyEvent.Ctrl || keyEvent.Text == "" {
		return
	}

	for _, char := range keyEvent.Text {
		app.composer.InsertRune(char)
	}
}

// startRun begins a controller investigation for query: it bumps the run ID, marks
// the shell working, and swaps the active trace channel to the new run's. The
// transcript and session usage totals persist across runs. A submit arriving while
// a run is in flight is ignored, and a nil controller leaves the shell idle.
func (app *App) startRun(ctx context.Context, query string) {
	if app.working || app.controller == nil {
		return
	}

	app.cancelRun()

	runCtx, cancel := context.WithCancel(ctx)
	app.cancel = cancel
	app.runID++
	app.working = true
	app.traceCh = app.controller.Start(runCtx, query, app.runID)
}

// cancelRun cancels the active run's context, if any, releasing its controller.
func (app *App) cancelRun() {
	if app.cancel != nil {
		app.cancel()
	}
}

// onResize re-synchronizes the screen after a terminal resize.
func (app *App) onResize() {
	app.screen.Sync()
}

// spinnerGlyph returns the current spinner frame, or empty when idle.
func (app *App) spinnerGlyph() string {
	if !app.working {
		return ""
	}

	return string(spinnerFrames[app.spinnerFrame%len(spinnerFrames)])
}

// footerHeightFor returns the footer height given the screen height and the
// composer's rendered line count, yielding the footer only when there is room
// beyond the composer.
func footerHeightFor(height, composerLines int) int {
	if height <= composerLines {
		return 0
	}

	return min(footerRows, height-composerLines)
}

// run drives an App on an injected screen; tests call this with a fake screen.
func run(ctx context.Context, screen tcell.Screen, opts RunOptions) error {
	return newApp(screen, opts).loop(ctx)
}

// tickWhen returns ticks only when enabled, otherwise a nil channel that disables
// its select case.
func tickWhen(enabled bool, ticks <-chan time.Time) <-chan time.Time {
	if enabled {
		return ticks
	}

	return nil
}
