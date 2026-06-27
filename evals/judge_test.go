package main

import (
	"context"
	"errors"
	"testing"

	"github.com/samber/oops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	// judgeQuery is the user question the Tier 3 cases audit an answer against.
	judgeQuery = "What was wrong with my box around 09:00?"
	// judgeAnswer is the controller's final answer the judge reviews.
	judgeAnswer = "checkout-api hit an OOM event at 09:00; the kernel OOM killer reclaimed its memory."
	// judgeExtension is the case's judge_prompt_extension that Tier3 must forward to
	// the judge as extra grounding context.
	judgeExtension = "identify a memory pressure event in checkout-api"
)

// mockJudger is a testify mock of the Judger seam: the openai judge layer and this
// mock both satisfy Judger, so the Tier 3 check drives either through the same
// contract. Expectations script the verdict Judge returns; the recorder confirms
// the JudgeRequest the check handed it, including the grounding extension.
type mockJudger struct {
	mock.Mock
}

// Judge records its arguments and replays the verdict scripted via
// .On("Judge", ...).Return(verdict, err).
func (m *mockJudger) Judge(ctx context.Context, req JudgeRequest) (JudgeVerdict, error) {
	args := m.Called(ctx, req)

	verdict, ok := args.Get(0).(JudgeVerdict)
	if !ok {
		return JudgeVerdict{Critique: "", Approve: false}, args.Error(1)
	}

	return verdict, args.Error(1)
}

// Compile-time assertion that the mock satisfies the seam it stands in for.
var _ Judger = (*mockJudger)(nil)

func TestTier3ApprovingVerdictPasses(t *testing.T) {
	t.Parallel()

	judge := new(mockJudger)
	judge.On("Judge", mock.Anything, mock.Anything).
		Return(JudgeVerdict{Critique: "", Approve: true}, nil)

	got, err := Tier3(context.Background(), judge, judgeQuery, judgeAnswer, judgeExtension)
	require.NoError(t, err)

	assert.True(t, got.Approve)
	assert.Empty(t, got.Critique)

	// The case's judge_prompt_extension must reach the Judger as extra context.
	judge.AssertCalled(t, "Judge", mock.Anything, mock.MatchedBy(func(req JudgeRequest) bool {
		return req.Extension == judgeExtension
	}))

	// Confirm the rest of the forwarded payload too: the audited question and answer.
	require.Len(t, judge.Calls, 1)

	req, ok := judge.Calls[0].Arguments.Get(1).(JudgeRequest)
	require.True(t, ok)
	assert.Equal(t, judgeQuery, req.Question)
	assert.Equal(t, judgeAnswer, req.Answer)
	assert.Equal(t, judgeExtension, req.Extension)

	judge.AssertExpectations(t)
}

func TestTier3RejectingVerdictFails(t *testing.T) {
	t.Parallel()

	const critique = "the answer invents an ssh.service unit absent from the cited entries"

	judge := new(mockJudger)
	judge.On("Judge", mock.Anything, mock.Anything).
		Return(JudgeVerdict{Critique: critique, Approve: false}, nil)

	got, err := Tier3(context.Background(), judge, judgeQuery, judgeAnswer, judgeExtension)
	require.NoError(t, err)

	assert.False(t, got.Approve)
	assert.Equal(t, critique, got.Critique)

	judge.AssertExpectations(t)
}

func TestTier3JudgeErrorWrapsOops(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("judge responses call failed")

	judge := new(mockJudger)
	judge.On("Judge", mock.Anything, mock.Anything).
		Return(JudgeVerdict{Critique: "", Approve: false}, sentinel)

	got, err := Tier3(context.Background(), judge, judgeQuery, judgeAnswer, judgeExtension)
	require.Error(t, err)
	assert.Equal(t, Tier3Result{Critique: "", Approve: false}, got)

	var oopsErr oops.OopsError
	require.ErrorAs(t, err, &oopsErr)
	assert.Equal(t, "evals", oopsErr.Domain())
	assert.Equal(t, "judge_failed", oopsErr.Code())
	require.ErrorContains(t, err, judgeQuery)
	require.ErrorIs(t, err, sentinel)

	judge.AssertExpectations(t)
}
