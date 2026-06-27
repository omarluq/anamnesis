package terminal

import (
	"context"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// staleRunID stamps the phantom run whose events the loop must gate out: it
	// matches neither the run-zero ambient stream nor the replay controller's run.
	staleRunID uint64 = 99
	// staleAnswer is the FINAL text the superseded run tries to push; the gate must
	// keep it out of both the trace pane and the chat answer view.
	staleAnswer = "STALE ANSWER FROM A SUPERSEDED RUN"
	// ambientMarker is the matching run-zero line whose render proves the loop has
	// already drained and gated the stale events ahead of it on the FIFO channel.
	ambientMarker = "ambient: warming up"
	// integrationQuery is the prompt the test submits through the real composer to
	// kick off the replay controller's scripted investigation.
	integrationQuery = "why did the gpu oops on resume"
)

// primeStaleAmbientTrace pushes a superseded run's events onto the still-active
// run-zero trace channel and then waits for the trailing matching marker to
// render. The channel is unbuffered and FIFO, so the marker rendering proves the
// loop has already gated the stale-RunID events queued ahead of it: their answer
// never reaches the trace pane and their usage never reaches the cost pane.
func primeStaleAmbientTrace(t *testing.T, screen *fakeScreen, ambient chan<- TraceEvent) {
	t.Helper()

	sendTrace(t, ambient, traceEvent(TraceKindUsage, "stale usage meter", 9_999, 9_999, 9_000_000, staleRunID))
	sendTrace(t, ambient, traceEvent(TraceKindFinal, staleAnswer, 0, 0, 0, staleRunID))
	sendTrace(t, ambient, traceEvent(TraceKindStdout, ambientMarker, 0, 0, 0, 0))

	awaitContents(t, screen, ambientMarker)

	contents := screen.contents()
	assert.NotContains(t, contents, staleAnswer, "the stale-RunID answer is gated before it can render")
	assert.Contains(t, contents, "$0.0000", "the stale-RunID usage never tallies into the cost pane")
	assert.NotContains(t, contents, "$9.0000", "the stale-RunID cost is dropped, not accumulated")
}

// assertReplayTraceLines asserts the trace pane holds exactly the replay script's
// non-usage events, in script order. The expectation is derived from the canonical
// script so the assertion pins routing and ordering: usage events are excluded
// (they belong to the cost pane) and the run-zero marker is gone, cleared by the
// per-run reset before the replay events landed.
func assertReplayTraceLines(t *testing.T, app *App) {
	t.Helper()

	expected := lo.FilterMap(defaultReplayScript(), func(event TraceEvent, _ int) (string, bool) {
		return traceText(event), event.Kind != TraceKindUsage
	})

	assert.Equal(t, expected, traceLines(app),
		"the trace pane holds the replay script's non-usage events in order")
}

// assertSessionCost asserts the cost pane accumulated only the replay run's two
// usage meters: the stale-RunID meter's 9,999 tokens and $9 were dropped, so the
// session totals stay at the replay script's figures.
func assertSessionCost(t *testing.T, app *App) {
	t.Helper()

	assert.Equal(t, 1792, app.cost.tokensIn, "only the replay run's input tokens tallied")
	assert.Equal(t, 384, app.cost.tokensOut, "only the replay run's output tokens tallied")
	assert.Equal(t, "$1.5000", app.cost.dollars(), "the stale-RunID cost never reached the session total")
}

// assertChatAnswer asserts the chat answer view echoed the submitted query and
// rendered the replay controller's FINAL answer, and never rendered the stale
// answer from the superseded run.
func assertChatAnswer(t *testing.T, app *App) {
	t.Helper()

	assert.Contains(t, app.chat.view.Text, integrationQuery, "the submitted query is echoed into the chat view")
	assert.Contains(t, app.chat.view.Text, replayFinalAnswer, "the FINAL answer renders into the chat answer view")
	assert.NotContains(t, app.chat.view.Text, staleAnswer, "the stale-RunID answer never reached the chat view")
}

// TestAppIntegrationReplayDrivesInvestigationAndDropsStaleRunID drives the whole
// shell end to end over a recording screen. First a phantom run's events arrive
// on the active run-zero trace channel and must be ignored by RunID gating; then
// the offline replay controller answers a query submitted through the real
// composer-and-loop path. It asserts the ordered trace lines, the non-zero
// session cost totals, the FINAL answer rendered into the chat answer view, and
// that neither the stale answer nor its usage ever landed, all without a single
// network call.
func TestAppIntegrationReplayDrivesInvestigationAndDropsStaleRunID(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(100, 32)
	ambient := make(chan TraceEvent)
	controller := newReplayController(defaultReplayScript(), 0)

	app := newApp(screen, RunOptions{Trace: ambient, Controller: controller, Title: defaultTitle})

	done := make(chan error, 1)
	go func() { done <- app.loop(context.Background()) }()

	// Phase 1: a superseded run's events arrive on the run-zero channel and are
	// gated out by RunID before they can touch the trace or cost panes.
	primeStaleAmbientTrace(t, screen, ambient)

	// Phase 2: the user submits a query and the replay controller drives the full
	// scripted investigation to its FINAL answer. Awaiting the [final] trace line
	// proves the turn/code/stdout/sub-call/usage/final sequence drained in order.
	submitQuery(screen, integrationQuery)
	awaitContents(t, screen, "[final]")

	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))
	require.NoError(t, awaitLoop(t, done))

	assertReplayTraceLines(t, app)
	assertSessionCost(t, app)
	assertChatAnswer(t, app)
}
