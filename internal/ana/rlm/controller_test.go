package rlm_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/samber/oops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/repl"
	"github.com/omarluq/anamnesis/internal/ana/rlm"
	"github.com/omarluq/anamnesis/internal/openai"
	"github.com/omarluq/anamnesis/internal/terminal"
)

// mockEvalCapture is a testify mock of the EvalCapture seam: *repl.Interpreter and
// this mock both satisfy the interface, so the controller loop drives either
// through the same contract. Expectations script the Result Eval returns for a
// turn's code and the answer Final resolves once a terminal primitive has run.
type mockEvalCapture struct {
	mock.Mock
}

// Eval records its arguments and replays the Result scripted via
// .On("Eval", name, src).Return(result, err).
func (m *mockEvalCapture) Eval(name, src string) (repl.Result, error) {
	args := m.Called(name, src)

	result, ok := args.Get(0).(repl.Result)
	if !ok {
		return repl.Result{Retval: reflect.Value{}, Stdout: "", Stderr: ""}, args.Error(1)
	}

	return result, args.Error(1)
}

// Final records the resolution call and replays the answer scripted via
// .On("Final").Return(answer, ok).
func (m *mockEvalCapture) Final() (string, bool) {
	args := m.Called()

	return args.String(0), args.Bool(1)
}

// Compile-time assertion that the mock satisfies the seam it stands in for.
var _ rlm.EvalCapture = (*mockEvalCapture)(nil)

func TestControllerRunResolvesFinalLiteralAnswer(t *testing.T) {
	t.Parallel()

	// agent.FINAL records the answer as a literal; Final returns that literal
	// verbatim, so the controller surfaces it unchanged.
	assertControllerResolvesAnswer(t,
		`boots := journal.Boots(); fmt.Println(len(boots)); agent.FINAL("checkout-api hit an OOM kill")`,
		"3\n",
		"checkout-api hit an OOM kill",
	)
}

func TestControllerRunResolvesFinalVarAnswer(t *testing.T) {
	t.Parallel()

	// agent.FINAL_VAR names a REPL variable; Final resolves it to the variable's
	// current value, so the controller surfaces the interpreter-resolved answer
	// rather than a literal recorded in source.
	assertControllerResolvesAnswer(t,
		`summary := agent.Query("summarize the OOM backtrace", entries); agent.FINAL_VAR("summary")`,
		"",
		"memory pressure killed checkout-api after a leak",
	)
}

func TestControllerRunErrorsWhenDoneWithoutTerminalAnswer(t *testing.T) {
	t.Parallel()

	// The controller reported Done but no terminal primitive resolved an answer:
	// Final reports ok=false, so resolve surfaces the controller_missing_final fault
	// rather than rendering an empty answer.
	ctx := context.Background()
	fixture := newSessionFixture()
	eval := new(mockEvalCapture)
	controller := rlm.NewController(&fixture.session, eval)

	doneTurn := openai.ControllerResponse{Thinking: "claim done with nothing resolved", Code: "", Done: true}

	fixture.controller.
		On("Respond", ctx, fixtureSystemPrompt, fixtureQuestion, "").
		Return(doneTurn, nil).
		Once()
	eval.On("Final").Return("", false).Once()

	got, err := controller.Run(ctx)
	require.Error(t, err)
	assert.Empty(t, got)
	require.ErrorContains(t, err, "done without a terminal answer")

	var oopsErr oops.OopsError

	require.ErrorAs(t, err, &oopsErr)
	assert.Equal(t, "controller_missing_final", oopsErr.Code())

	assert.Empty(t, fixture.session.History)
	fixture.controller.AssertExpectations(t)
	eval.AssertExpectations(t)
}

// assertControllerResolvesAnswer drives the controller through one code turn that
// records a terminal answer and one Done turn, asserting it evaluates the code
// exactly once, appends the single rendered ControllerTurn, emits one turn event,
// and returns the answer Final resolves. It is the shared spine for the FINAL
// literal and FINAL_VAR cases, which differ only in the code evaluated and the
// answer the interpreter resolves.
func assertControllerResolvesAnswer(t *testing.T, code, stdout, answer string) {
	t.Helper()

	ctx := context.Background()
	fixture := newSessionFixture()
	eval := new(mockEvalCapture)
	controller := rlm.NewController(&fixture.session, eval)

	codeTurn := openai.ControllerResponse{Thinking: "inspect then conclude", Code: code, Done: false}
	doneTurn := openai.ControllerResponse{Thinking: "the answer is ready", Code: "", Done: true}
	wantTurn := rlm.ControllerTurn{Code: code, Stdout: stdout, Retval: "", Err: "", Index: 0}

	fixture.controller.On("Respond", ctx, fixtureSystemPrompt, fixtureQuestion, "").
		Return(codeTurn, nil).
		Once()
	fixture.controller.On("Respond", ctx, fixtureSystemPrompt, fixtureQuestion,
		rlm.Render([]rlm.ControllerTurn{wantTurn})).
		Return(doneTurn, nil).
		Once()

	eval.On("Eval", "turn_0", code).
		Return(repl.Result{Retval: reflect.Value{}, Stdout: stdout, Stderr: ""}, nil).
		Once()
	eval.On("Final").
		Return(answer, true).
		Once()

	got, err := controller.Run(ctx)
	require.NoError(t, err)
	assert.Equal(t, answer, got)

	require.Len(t, fixture.session.History, 1)
	assert.Equal(t, wantTurn, fixture.session.History[0])

	event := <-fixture.events
	assert.Equal(t, terminal.TraceKindThinking, event.Kind)
	assert.Equal(t, codeTurn.Thinking, event.Text)
	assert.Equal(t, fixtureRunID, event.RunID)

	fixture.controller.AssertExpectations(t)
	eval.AssertExpectations(t)
}
