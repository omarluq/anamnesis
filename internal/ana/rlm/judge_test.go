package rlm_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/journal"
	"github.com/omarluq/anamnesis/internal/ana/rlm"
	"github.com/omarluq/anamnesis/internal/openai"
)

func TestControllerRunRendersApprovedAnswerWithoutRetry(t *testing.T) {
	t.Parallel()

	// An approving judge settles the §5 gate on the first Done: the loop renders the
	// resolved answer verbatim and takes no extra controller turn, so Respond and
	// Judge are each driven exactly once.
	ctx := context.Background()
	fixture := newSessionFixture()
	eval := new(mockEvalCapture)
	controller := rlm.NewController(&fixture.session, eval)

	answer := "checkout-api was OOM-killed under memory pressure"
	doneTurn := openai.ControllerResponse{Thinking: "the answer is ready", Code: "", Done: true}

	fixture.controller.
		On("Respond", ctx, fixtureSystemPrompt, fixtureQuestion, "").
		Return(doneTurn, nil).
		Once()
	eval.On("Final").Return(answer, true).Once()
	fixture.judge.
		On("Judge", ctx, fixtureQuestion, answer, "").
		Return("", nil).
		Once()

	got, err := controller.RunAudited(ctx)
	require.NoError(t, err)
	assert.Equal(t, answer, got)

	fixture.controller.AssertNumberOfCalls(t, "Respond", 1)
	fixture.judge.AssertNumberOfCalls(t, "Judge", 1)
	assert.Empty(t, fixture.session.History)

	fixture.controller.AssertExpectations(t)
	eval.AssertExpectations(t)
	fixture.judge.AssertExpectations(t)
}

func TestControllerRunRetriesOnceOnJudgeCritique(t *testing.T) {
	t.Parallel()

	// A judge that critiques once then approves drives exactly one additional
	// controller turn: the first Done is fed the critique back as a revision
	// directive, the controller re-finalizes, and the second judge pass approves the
	// answer the loop then renders.
	assertJudgeRetryRendersAnswer(t, "")
}

func TestControllerRunRendersAnswerAfterSecondCritique(t *testing.T) {
	t.Parallel()

	// The §5 retry is capped at one: a judge that critiques on both passes still
	// renders the answer after exactly one additional turn rather than looping
	// forever, proving the recorded critique gates the single retry.
	assertJudgeRetryRendersAnswer(t, "still too vague to act on")
}

func TestRunAuditedSkipsJudgeOnForceFinish(t *testing.T) {
	t.Parallel()

	// A force-finished investigation yields an honest "investigation incomplete" note,
	// not a grounded answer: RunAudited returns it directly without auditing it, so the
	// judge is never called on a non-answer.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	fixture := newSessionFixture()
	eval := new(mockEvalCapture)
	controller := rlm.NewController(&fixture.session, eval)

	got, err := controller.RunAudited(ctx)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(got, "investigation incomplete"),
		"a force-finished run renders the honest incomplete note")
	assert.Equal(t, "ctx_canceled", controller.FinishReason(),
		"the force-finish reason is the canceled context")

	fixture.judge.AssertNotCalled(t, "Judge")
	fixture.controller.AssertNotCalled(t, "Respond")
	fixture.controller.AssertExpectations(t)
	eval.AssertExpectations(t)
	fixture.judge.AssertExpectations(t)
}

func TestRunAuditedPreservesGroundedFinalWhenRevisionForceFinishes(t *testing.T) {
	t.Parallel()

	// The §5 regression: a grounded FINAL the judge critiques must NOT be downgraded to
	// the "investigation incomplete" note when the revision pass then force-finishes.
	// Here the first pass reaches FINAL and is judged; the judge cancels the run as it
	// critiques, so the revision pass force-finishes on the canceled context before
	// taking a turn — and RunAudited returns the last grounded FINAL rather than the note.
	ctx, cancel := context.WithCancel(context.Background())
	fixture := newSessionFixture()
	eval := new(mockEvalCapture)
	controller := rlm.NewController(&fixture.session, eval)

	answer := "checkout-api was OOM-killed; the leak is in cache.service"
	critique := "tie the OOM-kill to a cited entry"
	doneTurn := openai.ControllerResponse{Thinking: "conclude with the root cause", Code: "", Done: true}

	fixture.controller.
		On("Respond", ctx, fixtureSystemPrompt, fixtureQuestion, "").
		Return(doneTurn, nil).
		Once()
	eval.On("Final").Return(answer, true).Once()
	fixture.judge.
		On("Judge", ctx, fixtureQuestion, answer, "").
		Run(func(mock.Arguments) { cancel() }).
		Return(critique, nil).
		Once()

	got, err := controller.RunAudited(ctx)
	require.NoError(t, err)
	assert.Equal(t, answer, got, "the grounded FINAL is preserved, not downgraded")
	assert.NotContains(t, got, "investigation incomplete",
		"a force-finished revision must not erase a sound answer")

	// One pass finalized and was judged once; the revision force-finished on the canceled
	// context without another Respond or Judge call.
	fixture.controller.AssertNumberOfCalls(t, "Respond", 1)
	fixture.judge.AssertNumberOfCalls(t, "Judge", 1)

	fixture.controller.AssertExpectations(t)
	eval.AssertExpectations(t)
	fixture.judge.AssertExpectations(t)
}

func TestControllerRunRejectsFabricatedCitation(t *testing.T) {
	t.Parallel()

	// The citation grounding gate fails the run before the judge ever sees it: the
	// controller cited a cursor no journal query returned this session, so
	// Store.Validate rejects the answer and Run surfaces the fabricated cursor.
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

	got, err := controller.RunAudited(ctx)
	require.Error(t, err)
	assert.Empty(t, got)
	require.ErrorContains(t, err, "validate final citations")
	require.ErrorContains(t, err, "cur-ghost")

	fixture.judge.AssertNotCalled(t, "Judge")
	fixture.controller.AssertExpectations(t)
	eval.AssertExpectations(t)
}

// assertJudgeRetryRendersAnswer drives the §5 one-retry path: the judge critiques
// the first Done, the controller is shown that critique in its framed history and
// re-finalizes, and the second judge pass returns secondVerdict. Either verdict
// renders the answer after exactly one additional controller turn — an empty
// secondVerdict is the approve-on-retry case, a non-empty one proves the retry is
// capped at one — so it is the shared spine for both critique scenarios.
func assertJudgeRetryRendersAnswer(t *testing.T, secondVerdict string) {
	t.Helper()

	ctx := context.Background()
	fixture := newSessionFixture()
	eval := new(mockEvalCapture)
	controller := rlm.NewController(&fixture.session, eval)

	answer := "the leak in checkout-api triggered an OOM kill"
	critique := "ground the OOM-kill claim in a cited journal entry"
	doneTurn := openai.ControllerResponse{Thinking: "report the finding", Code: "", Done: true}

	fixture.controller.
		On("Respond", ctx, fixtureSystemPrompt, fixtureQuestion, "").
		Return(doneTurn, nil).
		Once()
	fixture.controller.
		On("Respond", ctx, fixtureSystemPrompt, fixtureQuestion,
			mock.MatchedBy(func(history string) bool { return strings.Contains(history, critique) })).
		Return(doneTurn, nil).
		Once()

	eval.On("Final").Return(answer, true).Times(2)
	fixture.judge.On("Judge", ctx, fixtureQuestion, answer, "").Return(critique, nil).Once()
	fixture.judge.On("Judge", ctx, fixtureQuestion, answer, "").Return(secondVerdict, nil).Once()

	got, err := controller.RunAudited(ctx)
	require.NoError(t, err)
	assert.Equal(t, answer, got)

	fixture.controller.AssertNumberOfCalls(t, "Respond", 2)
	fixture.judge.AssertNumberOfCalls(t, "Judge", 2)

	fixture.controller.AssertExpectations(t)
	eval.AssertExpectations(t)
	fixture.judge.AssertExpectations(t)
}
