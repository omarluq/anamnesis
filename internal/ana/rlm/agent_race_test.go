package rlm_test

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"testing"

	"github.com/samber/oops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/citations"
	"github.com/omarluq/anamnesis/internal/ana/journal"
	"github.com/omarluq/anamnesis/internal/ana/rlm"
	"github.com/omarluq/anamnesis/internal/terminal"
)

// raceFixture wires a Session whose sub-call budget and trace channel are sized
// for a wide fan-out, so a QueryBatched stress test can reserve many sub-calls
// and emit two events per pair without the channel blocking the host goroutines.
type raceFixture struct {
	sub     *mockSubLLM
	budget  *rlm.Budget
	store   *citations.Store
	events  chan terminal.TraceEvent
	session rlm.Session
}

// newRaceFixture builds a raceFixture sized for fanOut concurrent sub-calls: the
// budget grants headroom beyond fanOut so the exact-reservation assertions have
// room to drain, and the trace channel buffers two slots per sub-call — a
// query-start and a query-end — so the host goroutines never block on a full
// channel before the test drains it.
func newRaceFixture(fanOut int) *raceFixture {
	controller := new(mockControllerLLM)
	sub := new(mockSubLLM)
	judge := new(mockJudger)
	budget := rlm.NewBudget()
	budget.MaxSubCalls = fanOut * 4
	store := citations.NewStore()
	events := make(chan terminal.TraceEvent, fanOut*2)
	emitter := rlm.NewEmitter(context.Background(), events, fixtureRunID)

	return &raceFixture{
		sub:    sub,
		budget: budget,
		store:  store,
		events: events,
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

func TestAgentQueryBatchedReturnsResultsInInputOrder(t *testing.T) {
	t.Parallel()

	const fanOut = 50

	ctx := context.Background()
	fixture := newRaceFixture(fanOut)
	agent := rlm.NewAgent(ctx, &fixture.session)

	prompts := make([]string, fanOut)
	ctxs := make([]any, fanOut)

	for index := range prompts {
		prompt := "prompt-" + strconv.Itoa(index)
		prompts[index] = prompt
		ctxs[index] = index
		// fmt.Sprint(index) renders the int ctx as its decimal string, so the
		// evidence each pair carries is the index itself.
		fixture.sub.On("Answer", ctx, prompt, strconv.Itoa(index)).
			Return("answer-"+strconv.Itoa(index), nil).
			Once()
	}

	answers := agent.QueryBatched(prompts, ctxs)

	require.Len(t, answers, fanOut)

	for index := range answers {
		assert.Equalf(t, "answer-"+strconv.Itoa(index), answers[index],
			"answer at position %d must match the prompt fed at that position", index)
	}

	fixture.sub.AssertExpectations(t)
	fixture.sub.AssertNumberOfCalls(t, "Answer", fanOut)

	// The fan-out reserved exactly one sub-call per pair, so MaxSubCalls-fanOut
	// reservations remain available.
	remaining := 0
	for fixture.budget.ReserveSubCall() == nil {
		remaining++
	}

	assert.Equal(t, fixture.budget.MaxSubCalls-fanOut, remaining)
}

func TestAgentQueryBatchedRacesCleanlyOnCountersAndStore(t *testing.T) {
	t.Parallel()

	const (
		workers   = 12
		perWorker = 5
	)

	ctx := context.Background()
	fixture := newRaceFixture(workers * perWorker)
	agent := rlm.NewAgent(ctx, &fixture.session)

	fixture.sub.On("Answer", ctx, mock.Anything, mock.Anything).Return("ok", nil)

	visible := citableEntry("cur-shared")
	fixture.store.RecordVisible([]journal.Entry{visible})

	var waitGroup sync.WaitGroup

	waitGroup.Add(workers)

	for worker := range workers {
		go func() {
			defer waitGroup.Done()

			prompts := make([]string, perWorker)
			ctxs := make([]any, perWorker)

			for index := range prompts {
				prompts[index] = fmt.Sprintf("w%d-p%d", worker, index)
				ctxs[index] = index
			}

			agent.QueryBatched(prompts, ctxs)
			agent.Cite([]journal.Entry{visible})
		}()
	}

	waitGroup.Wait()

	// Concurrent fan-outs hammered the atomic sub-call counter and the
	// mutex-guarded store; a clean run under -race must validate and have spent
	// exactly one sub-call per pair.
	require.NoError(t, fixture.store.Validate())

	spent := workers * perWorker
	remaining := 0

	for fixture.budget.ReserveSubCall() == nil {
		remaining++
	}

	assert.Equal(t, fixture.budget.MaxSubCalls-spent, remaining)
}

func TestAgentQueryBatchedPanicsOnMismatchedBatch(t *testing.T) {
	t.Parallel()

	fixture := newRaceFixture(2)
	agent := rlm.NewAgent(context.Background(), &fixture.session)

	defer func() {
		recovered := recover()
		require.NotNil(t, recovered, "QueryBatched must panic when prompts and ctxs differ in length")

		err, ok := recovered.(error)
		require.True(t, ok, "panic value must be an error")

		var oopsErr oops.OopsError

		require.ErrorAs(t, err, &oopsErr)
		assert.Equal(t, "rlm", oopsErr.Domain())
		assert.Equal(t, "agent_batch_mismatch", oopsErr.Code())

		fixture.sub.AssertNotCalled(t, "Answer")
	}()

	agent.QueryBatched([]string{"only-one"}, []any{1, 2})
	t.Fatal("QueryBatched returned instead of panicking on a mismatched batch")
}

func TestAgentQueryBatchedPanicsWhenBudgetCannotCoverBatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fixture := newRaceFixture(3)
	fixture.budget.MaxSubCalls = 2
	agent := rlm.NewAgent(ctx, &fixture.session)

	fixture.sub.On("Answer", ctx, mock.Anything, mock.Anything).Return("ok", nil)

	defer func() {
		recovered := recover()
		require.NotNil(t, recovered, "QueryBatched must panic when the budget cannot cover the batch")

		err, ok := recovered.(error)
		require.True(t, ok, "panic value must be an error")

		var oopsErr oops.OopsError

		require.ErrorAs(t, err, &oopsErr)
		assert.Equal(t, "agent_sub_call_budget", oopsErr.Code())
	}()

	agent.QueryBatched([]string{"a", "b", "c"}, []any{1, 2, 3})
	t.Fatal("QueryBatched returned instead of panicking on an exhausted budget")
}
