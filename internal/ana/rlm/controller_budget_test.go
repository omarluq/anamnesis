package rlm_test

import (
	"context"
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

func TestControllerRunForceFinishesOnCancelledContext(t *testing.T) {
	t.Parallel()

	// A context canceled before the loop starts — the user quitting mid-run — makes the
	// loop force-finish immediately, calling neither the controller nor the interpreter,
	// and return the standing header because nothing was gathered.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	fixture := newSessionFixture()
	eval := new(mockEvalCapture)
	controller := rlm.NewController(&fixture.session, eval)

	got, err := controller.Run(ctx)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(got, "investigation incomplete"),
		"force-finish answer opens with the standing header")
	assert.Contains(t, got, "before it ran a single turn",
		"a pre-loop cancel reports that nothing was gathered, with no raw dump")
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
	assert.True(t, strings.HasPrefix(got, "investigation incomplete"),
		"force-finish answer opens with the standing header")
	assert.NotContains(t, got, partial,
		"force-finish must not replay the timed-out turn's raw stdout into the answer")
	assert.Contains(t, got, "1 turn(s)",
		"force-finish reports the single turn the investigation spent before wedging")

	require.Len(t, fixture.session.History, 1)
	recorded := fixture.session.History[0]
	assert.Equal(t, code, recorded.Code)
	assert.Equal(t, partial, recorded.Stdout)
	assert.Contains(t, recorded.Err, "did not return")

	fixture.controller.AssertExpectations(t)
	eval.AssertExpectations(t)
}
