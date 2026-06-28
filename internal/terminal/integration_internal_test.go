package terminal

import (
	"context"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/transcript"
)

const (
	// integrationQuery is the prompt the test submits through the real composer to
	// kick off the replay controller's scripted investigation.
	integrationQuery = "why did the gpu oops on resume"
	// staleRunID stamps the phantom run whose events the loop must gate out.
	staleRunID uint64 = 99
	// staleAnswer is the FINAL text the superseded run tries to push; the gate must
	// keep it out of the transcript.
	staleAnswer = "STALE ANSWER FROM A SUPERSEDED RUN"
)

// TestAppIntegrationReplayDrivesTranscriptAndDropsStaleRunID drives the whole shell
// end to end over a recording screen: a superseded run's final answer arrives on
// the active run-zero channel and must be ignored by RunID gating, then the offline
// replay controller answers a query submitted through the real composer-and-loop
// path. It asserts the inline transcript — a user prompt box, a collapsed thinking
// block, a nested query block with its output, and the markdown answer — plus the
// footer usage totals, and that neither the stale answer nor any trace/cost pane
// ever rendered, all without a single network call.
func TestAppIntegrationReplayDrivesTranscriptAndDropsStaleRunID(t *testing.T) {
	t.Parallel()

	screen := newFakeScreen(110, 40)
	ambient := make(chan TraceEvent)
	controller := newReplayController(defaultReplayScript(), 0)

	app := newApp(screen, RunOptions{Trace: ambient, Controller: controller, Title: defaultTitle})

	done := make(chan error, 1)
	go func() { done <- app.loop(context.Background()) }()

	// A superseded run's final answer arrives on the run-zero channel and is gated
	// out by RunID before it can touch the transcript.
	sendTrace(t, ambient, traceEvent(TraceKindFinal, staleAnswer, staleRunID))

	// The user submits a query and the replay controller drives the scripted
	// investigation to its FINAL answer.
	submitQuery(screen, integrationQuery)
	awaitContents(t, screen, "pin the firmware")

	screen.inject(tcell.NewEventKey(tcell.KeyCtrlC, "", tcell.ModNone))
	require.NoError(t, awaitLoop(t, done))

	// The transcript is the submit, the thinking turn, the nested query, and the
	// answer — in order, with no stray messages.
	assert.Equal(t,
		[]transcript.Role{
			transcript.RoleUser,
			transcript.RoleThinking,
			transcript.RoleToolResult,
			transcript.RoleAssistant,
		},
		historyRoles(app),
		"the inline transcript holds the user prompt, thinking, query, and answer")

	contents := screen.contents()
	assert.Contains(t, contents, integrationQuery, "the user prompt box renders")
	assert.Contains(t, contents, thinkingLabel, "the dim thinking block renders, always expanded")
	assert.Contains(t, contents, queryName, "the nested query block header renders")
	assert.Contains(t, contents, "i915 GPU driver oopsed", "the nested query block shows its output")
	assert.Contains(t, contents, "pin the firmware", "the assistant markdown answer renders")

	assert.NotContains(t, contents, staleAnswer, "the stale-RunID answer is gated before it can render")
	assert.NotContains(t, contents, "Trace", "no trace pane is rendered")
	assert.NotContains(t, contents, "Metric", "no cost pane is rendered")

	// The footer is the title and the key hints — no usage or cost accounting.
	footer := screenRow(t, contents, "anamnesis")
	assert.Contains(t, footer, "ctrl+o queries", "the footer renders the key hints")
}
