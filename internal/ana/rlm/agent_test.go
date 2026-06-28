package rlm_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/samber/oops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/journal"
	"github.com/omarluq/anamnesis/internal/ana/rlm"
	"github.com/omarluq/anamnesis/internal/terminal"
)

// citableEntry is a fully-populated journal entry whose cursor a test can record
// visible before citing it through the agent.
func citableEntry(cursor string) journal.Entry {
	return journal.Entry{
		Timestamp: time.Time{},
		Cursor:    cursor,
		BootID:    "boot-1",
		Unit:      "checkout-api.service",
		Comm:      "checkout-api",
		Hostname:  "host-1",
		Message:   "Out of memory: Killed process",
		Priority:  2,
		PID:       4242,
	}
}

func TestAgentQueryReturnsSubAnswerReservesBudgetAndEmits(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fixture := newSessionFixture()
	agent := rlm.NewAgent(ctx, &fixture.session)

	// fmt.Sprint sorts map keys, so the rendered evidence is deterministic.
	fixture.sub.On("Answer", ctx, "why oom?", "map[oom:3]").
		Return("memory pressure", nil).
		Once()

	got := agent.Query("why oom?", map[string]int{"oom": 3})
	assert.Equal(t, "memory pressure", got)
	fixture.sub.AssertExpectations(t)

	start := <-fixture.events
	assert.Equal(t, terminal.TraceKindQueryStart, start.Kind)
	assert.Equal(t, "why oom?", start.Text)
	assert.Equal(t, 1, start.Depth, "the query start carries the fan-out depth")
	assert.Equal(t, fixtureRunID, start.RunID)

	end := <-fixture.events
	assert.Equal(t, terminal.TraceKindQueryEnd, end.Kind)
	assert.Equal(t, "memory pressure", end.Text)
	assert.Equal(t, 1, end.Depth, "the query end carries the fan-out depth")
	assert.Equal(t, fixtureRunID, end.RunID)

	// Query reserved exactly one sub-call: MaxSubCalls-1 reservations remain.
	remaining := 0
	for fixture.budget.ReserveSubCall() == nil {
		remaining++
	}

	assert.Equal(t, fixture.budget.MaxSubCalls-1, remaining)
}

func TestAgentQueryPanicsWhenSubCallBudgetExhausted(t *testing.T) {
	t.Parallel()

	fixture := newSessionFixture()
	agent := rlm.NewAgent(context.Background(), &fixture.session)

	for range fixture.budget.MaxSubCalls {
		require.NoError(t, fixture.budget.ReserveSubCall())
	}

	defer func() {
		recovered := recover()
		require.NotNil(t, recovered, "Query must panic once the sub-call budget is exhausted")

		err, ok := recovered.(error)
		require.True(t, ok, "panic value must be an error")
		require.ErrorContains(t, err, "sub-call budget")

		var oopsErr oops.OopsError

		require.ErrorAs(t, err, &oopsErr)
		assert.Equal(t, "rlm", oopsErr.Domain())
		assert.Equal(t, "agent_sub_call_budget", oopsErr.Code())

		fixture.sub.AssertNotCalled(t, "Answer")
		// The budget gate runs before any observable work: no sub-call trace event
		// reached the buffered (cap-1) channel before the panic.
		assert.Empty(t, fixture.events)
	}()

	agent.Query("anything", nil)
	t.Fatal("Query returned instead of panicking on an exhausted budget")
}

func TestAgentQueryPanicsWhenSubLLMFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fixture := newSessionFixture()
	agent := rlm.NewAgent(ctx, &fixture.session)

	// fmt.Sprint(nil) renders the untyped nil ctx as "<nil>".
	fixture.sub.On("Answer", ctx, "boom", "<nil>").
		Return("", errors.New("upstream down")).
		Once()

	defer func() {
		recovered := recover()
		require.NotNil(t, recovered, "Query must panic when the sub-LLM call fails")

		err, ok := recovered.(error)
		require.True(t, ok, "panic value must be an error")

		var oopsErr oops.OopsError

		require.ErrorAs(t, err, &oopsErr)
		assert.Equal(t, "agent_sub_call_failed", oopsErr.Code())
		fixture.sub.AssertExpectations(t)
	}()

	agent.Query("boom", nil)
	t.Fatal("Query returned instead of panicking on a sub-LLM failure")
}

func TestAgentCiteForwardsEntriesToStore(t *testing.T) {
	t.Parallel()

	fixture := newSessionFixture()
	agent := rlm.NewAgent(context.Background(), &fixture.session)

	visible := citableEntry("cur-oom")
	fixture.store.RecordVisible([]journal.Entry{visible})

	agent.Cite([]journal.Entry{visible})
	require.NoError(t, fixture.store.Validate(), "a cited visible cursor validates")

	agent.Cite([]journal.Entry{citableEntry("cur-ghost")})

	err := fixture.store.Validate()
	require.Error(t, err, "a cited cursor never made visible must fail validation")
	assert.ErrorContains(t, err, "cur-ghost")
}

func TestAgentFinalSetsLiteralAnswer(t *testing.T) {
	t.Parallel()

	fixture := newSessionFixture()
	agent := rlm.NewAgent(context.Background(), &fixture.session)

	assert.False(t, agent.Done(), "a fresh agent has no terminal answer")

	agent.Final("root cause: oom-kill")
	assert.True(t, agent.Done())

	answer, ok := agent.Literal()
	assert.True(t, ok)
	assert.Equal(t, "root cause: oom-kill", answer)

	_, isVar := agent.Variable()
	assert.False(t, isVar, "a literal answer is not a variable reference")
}

func TestAgentFinalVarRecordsVariableName(t *testing.T) {
	t.Parallel()

	fixture := newSessionFixture()
	agent := rlm.NewAgent(context.Background(), &fixture.session)

	agent.FinalVar("summary")
	assert.True(t, agent.Done())

	name, ok := agent.Variable()
	assert.True(t, ok)
	assert.Equal(t, "summary", name)

	_, isLiteral := agent.Literal()
	assert.False(t, isLiteral, "a variable reference is not a literal answer")
}
