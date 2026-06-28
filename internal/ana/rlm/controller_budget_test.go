package rlm_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/samber/oops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/repl"
	"github.com/omarluq/anamnesis/internal/ana/rlm"
	"github.com/omarluq/anamnesis/internal/openai"
	"github.com/omarluq/anamnesis/internal/terminal"
)

func TestControllerRunForceFinishesAfterTurnBudget(t *testing.T) {
	t.Parallel()

	// A controller that never calls agent.FINAL must not loop forever: once it
	// spends the §6 MaxTurns budget the loop force-finishes, returning the partial
	// findings printed across exactly MaxTurns recorded turns.
	ctx := context.Background()
	fixture := newSessionFixture()
	events := widenTrace(fixture)
	eval := new(mockEvalCapture)
	controller := rlm.NewController(&fixture.session, eval)

	maxTurns := fixture.session.Budget.MaxTurns
	code := `entries := journal.Boots(); fmt.Println(len(entries))`
	thinking := "inspect one more boot before concluding"
	codeTurn := openai.ControllerResponse{Thinking: thinking, Code: code, Done: false}

	fixture.controller.
		On("Respond", ctx, fixtureSystemPrompt, fixtureQuestion, mock.Anything).
		Return(codeTurn, nil).
		Times(maxTurns)

	findings := scriptTurnEvals(eval, code, maxTurns)
	wantAnswer := "investigation incomplete, partial findings: " + strings.Join(findings, "; ")

	got, err := controller.Run(ctx)
	require.NoError(t, err)
	assert.Equal(t, wantAnswer, got)
	require.Len(t, fixture.session.History, maxTurns)

	assertTurnEvents(t, drainTrace(events), thinking, maxTurns)

	fixture.controller.AssertExpectations(t)
	eval.AssertExpectations(t)
}

func TestControllerRunForceFinishesOnCancelledContext(t *testing.T) {
	t.Parallel()

	// A context canceled before the loop starts is the §6 wall-time backstop: the
	// loop force-finishes immediately, calls neither the controller nor the
	// interpreter, and returns the standing header because nothing was gathered.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	fixture := newSessionFixture()
	eval := new(mockEvalCapture)
	controller := rlm.NewController(&fixture.session, eval)

	got, err := controller.Run(ctx)
	require.NoError(t, err)
	assert.Equal(t, "investigation incomplete, partial findings", got)
	assert.Empty(t, fixture.session.History)

	fixture.controller.AssertExpectations(t)
	eval.AssertExpectations(t)
}

func TestControllerRunRecoversOverBudgetQueryPanic(t *testing.T) {
	t.Parallel()

	// An over-budget agent.Query panics inside the interpreted turn (SPEC §6); the
	// loop must recover it onto the turn's Err field and keep running rather than
	// crash, so a following Done turn still resolves an answer.
	ctx := context.Background()
	fixture := newSessionFixture()
	eval := new(mockEvalCapture)
	controller := rlm.NewController(&fixture.session, eval)

	code := `summary := agent.Query("summarize the OOM backtrace", entries)`
	panicMsg := "agent.Query exhausted the sub-call budget of 30"
	answer := "investigation stalled: the sub-call budget was exhausted"
	codeTurn := openai.ControllerResponse{Thinking: "fan a query out", Code: code, Done: false}
	doneTurn := openai.ControllerResponse{Thinking: "report the stall", Code: "", Done: true}

	fixture.controller.
		On("Respond", ctx, fixtureSystemPrompt, fixtureQuestion, "").
		Return(codeTurn, nil).
		Once()
	fixture.controller.
		On("Respond", ctx, fixtureSystemPrompt, fixtureQuestion, mock.Anything).
		Return(doneTurn, nil).
		Once()

	eval.On("EvalContext", mock.Anything, mock.Anything, "turn_0", code).Panic(panicMsg).Once()
	eval.On("Final").Return(answer, true).Once()

	got, err := controller.Run(ctx)
	require.NoError(t, err)
	assert.Equal(t, answer, got)

	require.Len(t, fixture.session.History, 1)
	recorded := fixture.session.History[0]
	assert.Equal(t, code, recorded.Code)
	assert.Empty(t, recorded.Stdout)
	assert.Contains(t, recorded.Err, panicMsg)

	event := <-fixture.events
	assert.Equal(t, terminal.TraceKindThinking, event.Kind)
	assert.Equal(t, codeTurn.Thinking, event.Text)

	fixture.controller.AssertExpectations(t)
	eval.AssertExpectations(t)
}

func TestControllerRunForceFinishesOnEvalTimeout(t *testing.T) {
	t.Parallel()

	// A turn whose generated Go never returns surfaces from EvalContext as an
	// eval_timed_out error: the loop records the turn with the timeout cause and the
	// partial stdout printed before it wedged, then force-finishes — distinct from the
	// recoverable over-budget panic — because the abandoned eval goroutine poisons the
	// interpreter, so a following turn must not run against it.
	ctx := context.Background()
	fixture := newSessionFixture()
	eval := new(mockEvalCapture)
	controller := rlm.NewController(&fixture.session, eval)

	code := `for { fmt.Println("scanning") }`
	partial := "scanning\nscanning\n"
	timeoutErr := oops.
		In("repl").
		Code("eval_timed_out").
		Errorf("eval %q exceeded %s; the generated code did not return (possible non-terminating loop)",
			"turn_0", 10*time.Minute)
	codeTurn := openai.ControllerResponse{Thinking: "loop over the boots", Code: code, Done: false}

	fixture.controller.
		On("Respond", ctx, fixtureSystemPrompt, fixtureQuestion, "").
		Return(codeTurn, nil).
		Once()
	eval.
		On("EvalContext", mock.Anything, mock.Anything, "turn_0", code).
		Return(repl.Result{Retval: reflect.Value{}, Stdout: partial}, timeoutErr).
		Once()

	got, err := controller.Run(ctx)
	require.NoError(t, err)
	assert.Equal(t, "investigation incomplete, partial findings: "+partial, got)

	require.Len(t, fixture.session.History, 1)
	recorded := fixture.session.History[0]
	assert.Equal(t, code, recorded.Code)
	assert.Equal(t, partial, recorded.Stdout)
	assert.Contains(t, recorded.Err, "did not return")

	fixture.controller.AssertExpectations(t)
	eval.AssertExpectations(t)
}

// widenTrace swaps the fixture's narrow trace channel for a wider buffered one so a
// multi-turn force-finish run can emit every turn's events without the emitter
// blocking on a full buffer, and returns the wide channel to drain.
func widenTrace(fixture *sessionFixture) chan terminal.TraceEvent {
	// Three buffer slots per recorded turn — a thinking event, a code-start, and a
	// code-end — plus headroom, so the synchronous emitter never blocks before
	// drainTrace runs, and the buffer scales with the fixture's MaxTurns budget.
	buffer := (fixture.session.Budget.MaxTurns + 1) * 3

	events := make(chan terminal.TraceEvent, buffer)
	fixture.session.Emitter = rlm.NewEmitter(context.Background(), events, fixtureRunID)

	return events
}

// scriptTurnEvals programs eval to answer each of count turns with a distinct
// stdout finding while the run code stays constant, returning the per-turn findings
// in turn order so a caller can roll them into the expected force-finish summary.
func scriptTurnEvals(eval *mockEvalCapture, code string, count int) []string {
	findings := make([]string, 0, count)

	for turn := range count {
		stdout := fmt.Sprintf("finding %d", turn)
		findings = append(findings, stdout)

		eval.
			On("EvalContext", mock.Anything, mock.Anything, fmt.Sprintf("turn_%d", turn), code).
			Return(repl.Result{Retval: reflect.Value{}, Stdout: stdout}, nil).
			Once()
	}

	return findings
}

// drainTrace reads every event a force-finish run buffered onto the trace channel,
// stopping once the channel is momentarily empty — safe because the run has already
// returned, so no further events are coming.
func drainTrace(events <-chan terminal.TraceEvent) []terminal.TraceEvent {
	drained := make([]terminal.TraceEvent, 0)

	for {
		select {
		case event := <-events:
			drained = append(drained, event)
		default:
			return drained
		}
	}
}

// assertTurnEvents proves the loop streamed each of wantTurns recorded turns in
// execution order — a thinking event (carrying the reasoning and this run's ID),
// then its code-start, then its code-end — exactly three events per turn in that
// sequence. Asserting the per-turn ORDER, not just per-kind totals, catches a
// regression that streamed every thinking first and every code block afterward,
// which the streaming contract forbids.
func assertTurnEvents(t *testing.T, events []terminal.TraceEvent, thinking string, wantTurns int) {
	t.Helper()

	require.Len(t, events, wantTurns*3, "each recorded turn streams thinking, then code-start, then code-end")

	for turn := range wantTurns {
		thinkingEvent := events[turn*3]
		codeStart := events[turn*3+1]
		codeEnd := events[turn*3+2]

		assert.Equal(t, terminal.TraceKindThinking, thinkingEvent.Kind, "turn %d opens with its thinking", turn)
		assert.Equal(t, thinking, thinkingEvent.Text, "turn %d carries the controller's reasoning", turn)
		assert.Equal(t, terminal.TraceKindCodeStart, codeStart.Kind, "turn %d streams code-start after thinking", turn)
		assert.Equal(t, terminal.TraceKindCodeEnd, codeEnd.Kind, "turn %d streams code-end after code-start", turn)

		for _, event := range []terminal.TraceEvent{thinkingEvent, codeStart, codeEnd} {
			assert.Equal(t, fixtureRunID, event.RunID, "turn %d events carry the run ID", turn)
		}
	}
}
