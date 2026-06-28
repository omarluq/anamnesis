package rlm_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/citations"
	"github.com/omarluq/anamnesis/internal/ana/journal"
	"github.com/omarluq/anamnesis/internal/ana/rlm"
	"github.com/omarluq/anamnesis/internal/openai"
	"github.com/omarluq/anamnesis/internal/terminal"
)

// Fixture constants are shared by the session builder and the collaborator test
// so the scripted controller calls match the prompts the Session was wired with.
const (
	fixtureRunID        = uint64(7)
	fixtureSystemPrompt = "you are the controller"
	fixtureQuestion     = "why did checkout-api crash?"
)

// mockControllerLLM is a testify mock of the ControllerLLM seam: the openai
// controller layer and this mock both satisfy the interface, so the session loop
// drives either through the same contract. Expectations script the structured
// reply Respond returns and the recorder confirms the (prompt, question,
// history) the loop drove it with.
type mockControllerLLM struct {
	mock.Mock
}

// Respond records its arguments and replays the response scripted via
// .On("Respond", ...).Return(resp, err). The onReasoning streaming callback is
// accepted to satisfy the ControllerLLM seam but deliberately left out of the
// m.Called matcher, so existing four-argument .On("Respond", ...) expectations keep
// matching; tests that need to exercise the streaming path invoke it explicitly.
func (m *mockControllerLLM) Respond(
	ctx context.Context,
	systemPrompt, question, history string,
	_ func(string),
) (openai.ControllerResponse, error) {
	args := m.Called(ctx, systemPrompt, question, history)

	response, ok := args.Get(0).(openai.ControllerResponse)
	if !ok {
		return openai.ControllerResponse{Thinking: "", Code: "", Done: false}, args.Error(1)
	}

	return response, args.Error(1)
}

// mockSubLLM is a testify mock of the SubLLM seam standing in for the openai
// sub-LLM layer agent.Query drives.
type mockSubLLM struct {
	mock.Mock
}

// Answer records its arguments and replays the string scripted via
// .On("Answer", ...).Return(text, err).
func (m *mockSubLLM) Answer(ctx context.Context, prompt, evidence string) (string, error) {
	args := m.Called(ctx, prompt, evidence)

	return args.String(0), args.Error(1)
}

// mockJudger is a testify mock of the Judger seam standing in for the openai
// judge layer.
type mockJudger struct {
	mock.Mock
}

// Judge records its arguments and replays the critique scripted via
// .On("Judge", ...).Return(critique, err).
func (m *mockJudger) Judge(ctx context.Context, question, answer, cited string) (string, error) {
	args := m.Called(ctx, question, answer, cited)

	return args.String(0), args.Error(1)
}

// Compile-time assertions that every mock satisfies the rlm seam it stands in for.
var (
	_ rlm.ControllerLLM = (*mockControllerLLM)(nil)
	_ rlm.SubLLM        = (*mockSubLLM)(nil)
	_ rlm.Judger        = (*mockJudger)(nil)
)

// sessionFixture bundles a wired Session with handles on the collaborators and
// the trace channel so a test can assert wiring and script the model mocks.
type sessionFixture struct {
	controller *mockControllerLLM
	sub        *mockSubLLM
	judge      *mockJudger
	budget     *rlm.Budget
	store      *citations.Store
	emitter    *rlm.Emitter
	events     chan terminal.TraceEvent
	session    rlm.Session
}

// newSessionFixture builds a Session from testify-mock collaborators and real
// budget, citation store, and emitter, exercising the exhaustruct-complete
// Session literal the controller spine depends on.
func newSessionFixture() *sessionFixture {
	controller := new(mockControllerLLM)
	sub := new(mockSubLLM)
	judge := new(mockJudger)
	budget := rlm.NewBudget()
	store := citations.NewStore()
	// Sized so a short controller run — each turn emits a thinking event plus a
	// code-start/code-end pair, and a sub-call adds a query-start/query-end — never
	// blocks the synchronous emitter before the test drains the channel.
	events := make(chan terminal.TraceEvent, 16)
	emitter := rlm.NewEmitter(context.Background(), events, fixtureRunID)

	return &sessionFixture{
		controller: controller,
		sub:        sub,
		judge:      judge,
		budget:     budget,
		store:      store,
		emitter:    emitter,
		events:     events,
		session: rlm.Session{
			Controller:   controller,
			Sub:          sub,
			Judge:        judge,
			Budget:       budget,
			Store:        store,
			Emitter:      emitter,
			History:      nil,
			Question:     fixtureQuestion,
			SystemPrompt: fixtureSystemPrompt,
		},
	}
}

func TestSessionWiresBudgetStoreEmitter(t *testing.T) {
	t.Parallel()

	fixture := newSessionFixture()
	session := fixture.session

	assert.Same(t, fixture.controller, session.Controller)
	assert.Same(t, fixture.sub, session.Sub)
	assert.Same(t, fixture.judge, session.Judge)
	assert.Same(t, fixture.budget, session.Budget)
	assert.Same(t, fixture.store, session.Store)
	assert.Same(t, fixture.emitter, session.Emitter)
	assert.Equal(t, fixtureQuestion, session.Question)
	assert.Equal(t, fixtureSystemPrompt, session.SystemPrompt)

	require.NoError(t, session.Budget.ReserveTurn())

	session.Emitter.Thinking("planning")

	event := <-fixture.events
	assert.Equal(t, terminal.TraceKindThinking, event.Kind)
	assert.Equal(t, "planning", event.Text)
	assert.Equal(t, fixtureRunID, event.RunID)
}

func TestSessionStoreValidatesCitedCursor(t *testing.T) {
	t.Parallel()

	fixture := newSessionFixture()
	entry := journal.Entry{
		Timestamp: time.Time{},
		Cursor:    "cur-oom",
		BootID:    "boot-1",
		Unit:      "checkout-api.service",
		Comm:      "checkout-api",
		Hostname:  "host-1",
		Message:   "Out of memory: Killed process",
		Priority:  2,
		PID:       4242,
	}

	fixture.session.Store.RecordVisible([]journal.Entry{entry})
	fixture.session.Store.Cite([]journal.Entry{entry})

	require.NoError(t, fixture.session.Store.Validate())
}

func TestSessionDrivesModelCollaborators(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fixture := newSessionFixture()

	want := openai.ControllerResponse{
		Thinking: "inspect boots",
		Code:     "journal.Boots()",
		Done:     false,
	}
	fixture.controller.On("Respond", ctx, fixtureSystemPrompt, fixtureQuestion, "history").
		Return(want, nil).
		Once()
	fixture.sub.On("Answer", ctx, "summarize", "[]Entry").
		Return("3 boots", nil).
		Once()
	fixture.judge.On("Judge", ctx, fixtureQuestion, "final answer", "cur-oom").
		Return("", nil).
		Once()

	resp, err := fixture.session.Controller.Respond(ctx, fixtureSystemPrompt, fixtureQuestion, "history", nil)
	require.NoError(t, err)
	assert.Equal(t, want, resp)

	answer, err := fixture.session.Sub.Answer(ctx, "summarize", "[]Entry")
	require.NoError(t, err)
	assert.Equal(t, "3 boots", answer)

	critique, err := fixture.session.Judge.Judge(ctx, fixtureQuestion, "final answer", "cur-oom")
	require.NoError(t, err)
	assert.Empty(t, critique)

	fixture.controller.AssertExpectations(t)
	fixture.sub.AssertExpectations(t)
	fixture.judge.AssertExpectations(t)
}
