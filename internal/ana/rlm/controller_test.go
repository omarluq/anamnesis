package rlm_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/journal"
	"github.com/omarluq/anamnesis/internal/ana/repl"
	"github.com/omarluq/anamnesis/internal/ana/rlm"
	"github.com/omarluq/anamnesis/internal/openai"
	"github.com/omarluq/anamnesis/internal/terminal"
)

// mockEvalCapture is a testify mock of the EvalCapture seam: *repl.Interpreter and
// this mock both satisfy the interface, so the controller loop drives either
// through the same contract. Expectations script the Result EvalContext returns for
// a turn's code and the answer Final resolves once a terminal primitive has run.
type mockEvalCapture struct {
	mock.Mock
}

// EvalContext records its arguments and replays the Result scripted via
// .On("EvalContext", ctx, timeout, name, src).Return(result, err).
func (m *mockEvalCapture) EvalContext(
	ctx context.Context,
	timeout time.Duration,
	name, src string,
) (repl.Result, error) {
	args := m.Called(ctx, timeout, name, src)

	result, ok := args.Get(0).(repl.Result)
	if !ok {
		return repl.Result{Retval: reflect.Value{}, Stdout: ""}, args.Error(1)
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

func TestControllerRunForceFinishesWhenDoneWithoutTerminalAnswer(t *testing.T) {
	t.Parallel()

	// The controller reported Done but no terminal primitive resolved an answer (every
	// turn's code errored). Rather than failing the run with a raw error, resolve
	// force-finishes with the honest incomplete note so the user sees a graceful message
	// rather than a crash, and the run-end reason records "no_answer".
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
	require.NoError(t, err)
	assert.Contains(t, got, "investigation incomplete")
	assert.Equal(t, "no_answer", controller.FinishReason())

	assert.Empty(t, fixture.session.History)
	fixture.controller.AssertExpectations(t)
	eval.AssertExpectations(t)
}

func TestControllerRunRejectsFabricatedCitation(t *testing.T) {
	t.Parallel()

	// The §7/§10 citation grounding gate fails the run as the answer resolves: the
	// controller cited a cursor no journal query returned this session, so
	// Store.Validate rejects the answer and Run surfaces the fabricated cursor rather
	// than rendering an ungrounded conclusion.
	ctx := context.Background()
	fixture := newSessionFixture()
	eval := new(mockEvalCapture)
	controller := rlm.NewController(&fixture.session, eval)

	answer := "the leak traces to an uncited entry"
	doneTurn := openai.ControllerResponse{Thinking: "report the finding", Code: "", Done: true}

	fixture.session.Store.Cite([]journal.Entry{{
		Timestamp: time.Time{},
		Cursor:    "cur-ghost",
		BootID:    "",
		Unit:      "",
		Comm:      "",
		Hostname:  "",
		Message:   "",
		Priority:  0,
		PID:       0,
	}})

	fixture.controller.
		On("Respond", ctx, fixtureSystemPrompt, fixtureQuestion, "").
		Return(doneTurn, nil).
		Once()
	eval.On("Final").Return(answer, true).Once()

	got, err := controller.Run(ctx)
	require.Error(t, err)
	assert.Empty(t, got)
	require.ErrorContains(t, err, "validate final citations")
	require.ErrorContains(t, err, "cur-ghost")

	fixture.controller.AssertExpectations(t)
	eval.AssertExpectations(t)
}

func TestControllerRunRendersAnswerWithGroundedCitation(t *testing.T) {
	t.Parallel()

	// A citation grounded in a session-visible cursor passes the §7/§10 gate: Run
	// renders the resolved answer rather than rejecting it, the companion to the
	// fabricated-cursor rejection above.
	ctx := context.Background()
	fixture := newSessionFixture()
	eval := new(mockEvalCapture)
	controller := rlm.NewController(&fixture.session, eval)

	answer := "checkout-api was OOM-killed under memory pressure"
	doneTurn := openai.ControllerResponse{Thinking: "report the finding", Code: "", Done: true}

	cited := citableEntry("cur-oom")
	fixture.session.Store.RecordVisible([]journal.Entry{cited})
	fixture.session.Store.Cite([]journal.Entry{cited})

	fixture.controller.
		On("Respond", ctx, fixtureSystemPrompt, fixtureQuestion, "").
		Return(doneTurn, nil).
		Once()
	eval.On("Final").Return(answer, true).Once()

	got, err := controller.Run(ctx)
	require.NoError(t, err)
	assert.Equal(t, answer, got)

	fixture.controller.AssertExpectations(t)
	eval.AssertExpectations(t)
}

// TestControllerRunResolvesInlineFinalOnDoneTurn proves the fix for a model that
// conflates the two-step ending by putting the agent.FINAL call in the very turn it
// sets Done. The loop evaluates that turn's code before resolving, so the inline
// agent.FINAL runs and records the answer instead of being skipped — the run returns
// the answer rather than failing with "done without a terminal answer".
func TestControllerRunResolvesInlineFinalOnDoneTurn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fixture := newSessionFixture()
	eval := new(mockEvalCapture)
	controller := rlm.NewController(&fixture.session, eval)

	const code = `agent.FINAL("checkout-api was OOM-killed")`

	answer := "checkout-api was OOM-killed"
	inlineDoneTurn := openai.ControllerResponse{Thinking: "conclude inline", Code: code, Done: true}
	wantTurn := rlm.ControllerTurn{Code: code, Stdout: "", Retval: "", Err: "", Index: 0}

	fixture.controller.
		On("Respond", ctx, fixtureSystemPrompt, fixtureQuestion, "").
		Return(inlineDoneTurn, nil).
		Once()
	eval.On("EvalContext", ctx, fixture.session.Budget.PerEvalTimeout, "turn_0", code).
		Return(repl.Result{Retval: reflect.Value{}, Stdout: ""}, nil).
		Once()
	eval.On("Final").Return(answer, true).Once()

	got, err := controller.Run(ctx)
	require.NoError(t, err, "an inline agent.FINAL on a Done turn must resolve, not fail")
	assert.Equal(t, answer, got)

	require.Len(t, fixture.session.History, 1, "the inline Done turn's code is evaluated and recorded")
	assert.Equal(t, wantTurn, fixture.session.History[0])

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

	eval.On("EvalContext", ctx, fixture.session.Budget.PerEvalTimeout, "turn_0", code).
		Return(repl.Result{Retval: reflect.Value{}, Stdout: stdout}, nil).
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
