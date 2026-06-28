package rlm_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/repl"
	"github.com/omarluq/anamnesis/internal/ana/rlm"
	"github.com/omarluq/anamnesis/internal/ana/scenarios"
	"github.com/omarluq/anamnesis/internal/openai"
	"github.com/omarluq/anamnesis/internal/terminal"
)

// childCode is the source a recursive child controller loop evaluates: it answers
// its sub-question with one leaf sub-call and signals FINAL. At MaxDepth the leaf
// agent.Query falls back to the flat base-case sub-LLM call.
const childCode = `agent.FINAL(agent.Query("leaf question", []string{"x"}))`

// concludeThinking is the reasoning a root loop reports on the done turn that
// closes an investigation, shared so the same literal does not repeat per test.
const concludeThinking = "conclude"

// scriptChildControllerTurns programs the controller mock for one child loop: a
// code turn that runs the leaf sub-call and signals FINAL, then a done turn. The
// child loop is framed with the §14 sub-controller prompt, so matching on that
// prompt keeps the child turns distinct from the root loop's turns.
func scriptChildControllerTurns(controllerLLM *mockControllerLLM, code string) {
	childTurn := openai.ControllerResponse{Thinking: "answer the sub-question", Code: code, Done: false}
	childDone := openai.ControllerResponse{Thinking: "sub-question answered", Code: "", Done: true}

	controllerLLM.
		On("Respond", mock.Anything, scenarios.SubControllerPrompt, mock.Anything, mock.Anything).
		Return(childTurn, nil).
		Once()
	controllerLLM.
		On("Respond", mock.Anything, scenarios.SubControllerPrompt, mock.Anything, mock.Anything).
		Return(childDone, nil).
		Once()
}

// TestInvestigateRecursesThroughChildControllerLoop drives a 2-level recursion: at
// MaxDepth 1 the root's agent.Query spawns a full child controller loop one level
// deeper, whose own agent.Query reaches the leaf and falls back to the flat
// base-case sub-call. It proves the depth really increments — the child loop runs
// under the §14 sub-controller prompt — that the leaf base case fires at MaxDepth,
// and that the child's FINAL splices back as the parent's sub-answer.
func TestInvestigateRecursesThroughChildControllerLoop(t *testing.T) {
	t.Parallel()

	const (
		question = "why did checkout-api crash?"
		answer   = "checkout-api was OOM-killed"
	)

	controllerLLM := new(mockControllerLLM)
	sub := new(mockSubLLM)
	judge := new(mockJudger)
	events := make(chan terminal.TraceEvent, investigateTraceBuffer)

	rootTurn := openai.ControllerResponse{
		Thinking: "decompose through a recursive sub-call",
		Code:     `agent.FINAL(agent.Query("summarize the failures", []string{"e"}))`,
		Done:     false,
	}
	rootDone := openai.ControllerResponse{Thinking: concludeThinking, Code: "", Done: true}

	scriptControllerTurns(controllerLLM, question, rootTurn, rootDone)
	scriptChildControllerTurns(controllerLLM, childCode)
	sub.On("Answer", mock.Anything, "leaf question", "[x]").Return(answer, nil).Once()
	judge.On("Judge", mock.Anything, question, answer, mock.Anything).Return("", nil).Once()

	deps := rlm.Deps{
		Controller:  controllerLLM,
		Sub:         sub,
		Judge:       judge,
		Journal:     new(mockJournalHost),
		Systemd:     new(mockSystemdHost),
		Events:      events,
		RunID:       fixtureRunID,
		MaxDepth:    1,
		MaxSubCalls: repl.DefaultMaxSubCalls,
	}

	got, err := rlm.Investigate(context.Background(), question, &deps)
	require.NoError(t, err)
	assert.Equal(t, answer, got, "the child loop's FINAL splices back as the root answer")

	controllerLLM.AssertCalled(t, "Respond", mock.Anything, scenarios.SubControllerPrompt,
		mock.Anything, mock.Anything)
	sub.AssertCalled(t, "Answer", mock.Anything, "leaf question", "[x]")
	sub.AssertExpectations(t)
	judge.AssertExpectations(t)
}

// TestInvestigateSharedBudgetCapsSubCallsTreeWide proves the §6 sub-call budget is
// shared across the whole recursion tree, not reset per child loop: with a budget
// of one sub-call, the root's agent.Query spends it, so the child loop's own
// agent.Query finds the budget exhausted and degrades to text — the leaf base case
// is never reached. The exhausted sub-call comes back as a graceful string the
// parent reasons over rather than a Go error that unwinds the turn.
func TestInvestigateSharedBudgetCapsSubCallsTreeWide(t *testing.T) {
	t.Parallel()

	const question = "why did checkout-api crash?"

	controllerLLM := new(mockControllerLLM)
	sub := new(mockSubLLM)
	judge := new(mockJudger)
	events := make(chan terminal.TraceEvent, investigateTraceBuffer)

	rootTurn := openai.ControllerResponse{
		Thinking: "recurse once",
		Code:     `agent.FINAL(agent.Query("summarize the failures", []string{"e"}))`,
		Done:     false,
	}
	rootDone := openai.ControllerResponse{Thinking: concludeThinking, Code: "", Done: true}

	scriptControllerTurns(controllerLLM, question, rootTurn, rootDone)
	scriptChildControllerTurns(controllerLLM, childCode)
	judge.On("Judge", mock.Anything, question, mock.Anything, mock.Anything).Return("", nil).Once()

	deps := rlm.Deps{
		Controller:  controllerLLM,
		Sub:         sub,
		Judge:       judge,
		Journal:     new(mockJournalHost),
		Systemd:     new(mockSystemdHost),
		Events:      events,
		RunID:       fixtureRunID,
		MaxDepth:    1,
		MaxSubCalls: 1,
	}

	got, err := rlm.Investigate(context.Background(), question, &deps)
	require.NoError(t, err)
	assert.Contains(t, got, "sub-investigation skipped",
		"the child's sub-call finds the shared budget already spent and degrades to text")
	sub.AssertNotCalled(t, "Answer")
}

// TestInvestigateQueryBatchedFanOutRacesCleanly drives a wide agent.QueryBatched
// fan-out of leaf base-case sub-calls so the run hammers the shared atomic sub-call
// budget and the bounded fan-out concurrently. Under -race a clean run proves the
// fan-out shares the tree-wide budget without data races and returns its replies in
// input order.
func TestInvestigateQueryBatchedFanOutRacesCleanly(t *testing.T) {
	t.Parallel()

	const (
		question = "which units failed this boot?"
		fanOut   = 12
	)

	controllerLLM := new(mockControllerLLM)
	sub := new(mockSubLLM)
	judge := new(mockJudger)
	events := make(chan terminal.TraceEvent, investigateTraceBuffer+fanOut)

	rootTurn := openai.ControllerResponse{Thinking: "fan out", Code: fanOutCode(fanOut), Done: false}
	rootDone := openai.ControllerResponse{Thinking: concludeThinking, Code: "", Done: true}

	scriptControllerTurns(controllerLLM, question, rootTurn, rootDone)
	sub.On("Answer", mock.Anything, mock.Anything, mock.Anything).Return("ok", nil)
	judge.On("Judge", mock.Anything, question, "ok", mock.Anything).Return("", nil).Once()

	deps := rlm.Deps{
		Controller:  controllerLLM,
		Sub:         sub,
		Judge:       judge,
		Journal:     new(mockJournalHost),
		Systemd:     new(mockSystemdHost),
		Events:      events,
		RunID:       fixtureRunID,
		MaxDepth:    0,
		MaxSubCalls: repl.DefaultMaxSubCalls,
	}

	got, err := rlm.Investigate(context.Background(), question, &deps)
	require.NoError(t, err)
	assert.Equal(t, "ok", got)
	sub.AssertNumberOfCalls(t, "Answer", fanOut)
}

// TestInvestigateRecursiveFanOutKeepsEveryBranch fans several NON-leaf sub-calls
// out in parallel from one agent.QueryBatched at MaxDepth 1, so every branch must
// spawn its own child controller loop concurrently. It guards against conflating
// fan-out breadth with recursion depth: the per-path depth ceiling lives on each
// branch's immutable RecursionContext, so concurrent siblings at the same depth
// must not exhaust a shared depth gauge and degrade to text. Every branch reaching
// the leaf sub-call — proven by exactly fanOut leaf calls — shows no branch was
// spuriously refused.
func TestInvestigateRecursiveFanOutKeepsEveryBranch(t *testing.T) {
	t.Parallel()

	const (
		question = "which units failed this boot?"
		fanOut   = 6
	)

	controllerLLM := new(mockControllerLLM)
	sub := new(mockSubLLM)
	judge := new(mockJudger)
	events := make(chan terminal.TraceEvent, investigateTraceBuffer+fanOut*5)

	rootTurn := openai.ControllerResponse{Thinking: "fan out and recurse", Code: fanOutCode(fanOut), Done: false}
	rootDone := openai.ControllerResponse{Thinking: concludeThinking, Code: "", Done: true}
	childTurn := openai.ControllerResponse{Thinking: "answer the sub-question", Code: childCode, Done: false}
	childDone := openai.ControllerResponse{Thinking: "sub-question answered", Code: "", Done: true}

	scriptControllerTurns(controllerLLM, question, rootTurn, rootDone)
	// Match child turns on history rather than call order, so concurrent child loops
	// resolve correctly however their Respond calls interleave: a fresh child loop's
	// first turn frames an empty history, and its done turn frames a non-empty one.
	controllerLLM.
		On("Respond", mock.Anything, scenarios.SubControllerPrompt, mock.Anything, "").
		Return(childTurn, nil)
	controllerLLM.
		On("Respond", mock.Anything, scenarios.SubControllerPrompt, mock.Anything,
			mock.MatchedBy(func(history string) bool { return history != "" })).
		Return(childDone, nil)
	sub.On("Answer", mock.Anything, "leaf question", "[x]").Return("ok", nil)
	judge.On("Judge", mock.Anything, question, "ok", mock.Anything).Return("", nil).Once()

	deps := rlm.Deps{
		Controller:  controllerLLM,
		Sub:         sub,
		Judge:       judge,
		Journal:     new(mockJournalHost),
		Systemd:     new(mockSystemdHost),
		Events:      events,
		RunID:       fixtureRunID,
		MaxDepth:    1,
		MaxSubCalls: repl.DefaultMaxSubCalls,
	}

	got, err := rlm.Investigate(context.Background(), question, &deps)
	require.NoError(t, err)
	assert.Equal(t, "ok", got)
	sub.AssertNumberOfCalls(t, "Answer", fanOut)
}

// fanOutCode builds controller source that fans count leaf sub-calls out through
// agent.QueryBatched and signals FINAL on the first reply, so a test drives a wide
// parallel fan-out from a single turn.
func fanOutCode(count int) string {
	prompts := make([]string, count)
	ctxs := make([]string, count)

	for index := range count {
		suffix := strconv.Itoa(index)
		prompts[index] = `"q` + suffix + `"`
		ctxs[index] = `[]string{"c` + suffix + `"}`
	}

	return "replies := agent.QueryBatched([]string{" + strings.Join(prompts, ", ") +
		"}, []any{" + strings.Join(ctxs, ", ") + "})\nagent.FINAL(replies[0])"
}
