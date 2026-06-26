// Package terminal implements the anamnesis interactive chat shell: a single
// mutable App struct driven by one select loop. Background controllers post
// typed TraceEvents onto a channel and the loop goroutine owns all UI mutation,
// rendering each frame into a fresh tui.CellBuffer that the renderer diffs
// against the previous frame.
package terminal

import (
	"context"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/samber/oops"

	"github.com/omarluq/anamnesis/internal/tui"
)

const (
	frameInterval   = 8 * time.Millisecond
	spinnerInterval = 120 * time.Millisecond

	chatWeight  = 2
	traceWeight = 3
	sideWeight  = 1
	costHeight  = 8

	defaultTitle = "anamnesis"
)

var spinnerFrames = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

// App is the mutable shell state owned by the single loop goroutine.
type App struct {
	screen       tcell.Screen
	renderer     *tui.Renderer
	chat         *chatPane
	trace        *tracePane
	cost         *costPane
	traceCh      <-chan TraceEvent
	title        string
	runID        uint64
	spinnerFrame int
	theme        Theme
	dirty        bool
	working      bool
}

// RunOptions configures a shell run.
type RunOptions struct {
	// Trace is an optional channel of controller events. When nil the trace and
	// cost panes render their placeholders.
	Trace <-chan TraceEvent
	// Title is the label shown in the chat pane header.
	Title string
}

// Run creates a real terminal screen and drives the shell until the user quits
// or ctx is canceled.
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

// newApp builds an App bound to screen with the default theme.
func newApp(screen tcell.Screen, opts RunOptions) *App {
	theme := DefaultTheme()

	title := opts.Title
	if title == "" {
		title = defaultTitle
	}

	return &App{
		screen:       screen,
		renderer:     tui.NewRenderer(screen),
		chat:         newChatPane(theme, title),
		trace:        newTracePane(theme),
		cost:         newCostPane(theme),
		traceCh:      opts.Trace,
		title:        title,
		theme:        theme,
		runID:        0,
		spinnerFrame: 0,
		dirty:        false,
		working:      false,
	}
}

// loop is the single select loop that owns all UI mutation.
func (app *App) loop(ctx context.Context) error {
	spinner := time.NewTicker(spinnerInterval)
	defer spinner.Stop()

	frame := time.NewTicker(frameInterval)
	defer frame.Stop()

	app.dirty = true

	for {
		app.drawIfDirty()

		select {
		case <-ctx.Done():
			return nil
		case event := <-app.screen.EventQ():
			if event == nil || app.handleEvent(event) {
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

// drawIfDirty renders a pending frame unless a working controller has handed
// frame throttling to the frame ticker.
func (app *App) drawIfDirty() {
	if app.dirty && !app.working {
		app.draw()
		app.dirty = false
	}
}

// handleTrace applies a trace event, detaching the channel and clearing the
// working state once it closes, and dropping events whose RunID does not match
// the active run.
func (app *App) handleTrace(event TraceEvent, ok bool) {
	if !ok {
		app.traceCh = nil
		app.working = false

		return
	}

	if event.RunID == app.runID {
		app.applyTrace(event)
		app.dirty = true
	}
}

// draw renders one frame into a fresh buffer and flushes the diff to screen.
func (app *App) draw() {
	app.chat.title = app.headerTitle()

	width, height := app.screen.Size()
	frame := tui.NewCellBuffer(width, height, app.theme.fg(app.theme.Text))
	app.layout().Draw(frame, tui.Rect{X: 0, Y: 0, Width: width, Height: height})
	app.renderer.Flush(frame)
	app.placeCursor()
	app.screen.Show()
}

// placeCursor moves the native terminal cursor to the composer caret, hiding it
// when the composer was not drawn this frame.
func (app *App) placeCursor() {
	column, row, visible := app.chat.cursorPosition()
	if !visible {
		app.screen.HideCursor()

		return
	}

	app.screen.ShowCursor(column, row)
}

// layout composes the chat | [trace / cost] flex tree.
func (app *App) layout() *tui.Flex {
	outer := &tui.Flex{Items: nil, Direction: tui.FlexRow}
	outer.AddItem(app.chat, 0, chatWeight)

	side := &tui.Flex{Items: nil, Direction: tui.FlexColumn}
	side.AddItem(app.trace, 0, traceWeight)
	side.AddItem(app.cost, costHeight, 0)

	outer.AddItem(side, 0, sideWeight)

	return outer
}

// handleEvent dispatches a screen event and reports whether the shell quits.
func (app *App) handleEvent(event tcell.Event) bool {
	switch typed := event.(type) {
	case *tcell.EventResize:
		app.onResize()

		return false
	case *tcell.EventKey:
		return app.handleKey(typed)
	default:
		return false
	}
}

// handleKey applies the priority chain: quit keys first, else the composer.
func (app *App) handleKey(event *tcell.EventKey) bool {
	keyEvent, ok := tui.NewKeyEvent(event)
	if !ok {
		return false
	}

	if app.isQuitKey(keyEvent) {
		return true
	}

	app.chat.handleKey(keyEvent)

	return false
}

// isQuitKey reports whether keyEvent should terminate the shell.
func (app *App) isQuitKey(keyEvent tui.KeyEvent) bool {
	switch keyEvent.Key {
	case "ctrl+c", "escape":
		return true
	case "q":
		return app.chat.composerEmpty()
	default:
		return false
	}
}

// applyTrace routes a trace event to the pane that owns its kind and tracks the
// working state that drives the header spinner: turns and sub-calls mark the
// loop busy, a final answer clears it, and usage events leave it unchanged.
func (app *App) applyTrace(event TraceEvent) {
	switch event.Kind {
	case TraceKindUsage:
		app.cost.applyUsage(event.TokensIn, event.TokensOut, event.CostMicros)

		return
	case TraceKindTurn, TraceKindSubCall:
		app.working = true
	case TraceKindFinal:
		app.working = false
	}

	app.trace.appendEvent(event)
}

// onResize re-synchronizes the screen after a terminal resize.
func (app *App) onResize() {
	app.screen.Sync()
}

// headerTitle returns the chat header, appending a spinner while working.
func (app *App) headerTitle() string {
	if app.working {
		return app.title + " " + app.spinnerGlyph()
	}

	return app.title
}

// spinnerGlyph returns the current spinner frame, or empty when idle.
func (app *App) spinnerGlyph() string {
	if !app.working {
		return ""
	}

	return string(spinnerFrames[app.spinnerFrame%len(spinnerFrames)])
}

// run drives an App on an injected screen; tests call this with a fake screen.
func run(ctx context.Context, screen tcell.Screen, opts RunOptions) error {
	return newApp(screen, opts).loop(ctx)
}

// tickWhen returns ticks only when enabled, otherwise a nil channel that
// disables its select case.
func tickWhen(enabled bool, ticks <-chan time.Time) <-chan time.Time {
	if enabled {
		return ticks
	}

	return nil
}
