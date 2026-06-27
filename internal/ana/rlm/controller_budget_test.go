package rlm_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

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

	assertTurnEvents(t, drainTrace(events, maxTurns), thinking)

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

	eval.On("Eval", "turn_0", code).Panic(panicMsg).Once()
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
	assert.Equal(t, terminal.TraceKindTurn, event.Kind)
	assert.Equal(t, codeTurn.Thinking, event.Text)

	fixture.controller.AssertExpectations(t)
	eval.AssertExpectations(t)
}

// widenTrace swaps the fixture's single-slot trace channel for a buffered one so a
// multi-turn force-finish run can emit a turn event per recorded turn without the
// emitter blocking on a full buffer, and returns the wide channel to drain.
func widenTrace(fixture *sessionFixture) chan terminal.TraceEvent {
	const traceBuffer = 64

	events := make(chan terminal.TraceEvent, traceBuffer)
	fixture.session.Emitter = rlm.NewEmitter(events, fixtureRunID)

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
			On("Eval", fmt.Sprintf("turn_%d", turn), code).
			Return(repl.Result{Retval: reflect.Value{}, Stdout: stdout, Stderr: ""}, nil).
			Once()
	}

	return findings
}

// drainTrace reads exactly count events off the buffered trace channel a
// force-finish run emitted, so a caller can assert against them once the loop has
// returned.
func drainTrace(events <-chan terminal.TraceEvent, count int) []terminal.TraceEvent {
	drained := make([]terminal.TraceEvent, 0, count)

	for range count {
		drained = append(drained, <-events)
	}

	return drained
}

// assertTurnEvents asserts every drained event is a turn event carrying the
// controller's reasoning and this run's ID, proving the loop emitted one event per
// recorded turn before it force-finished.
func assertTurnEvents(t *testing.T, events []terminal.TraceEvent, thinking string) {
	t.Helper()

	for _, event := range events {
		assert.Equal(t, terminal.TraceKindTurn, event.Kind)
		assert.Equal(t, thinking, event.Text)
		assert.Equal(t, fixtureRunID, event.RunID)
	}
}
