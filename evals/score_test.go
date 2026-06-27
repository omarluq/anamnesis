package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/samber/oops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	// scoreCaseID is the golden case identifier the scorer cases assert round-trips
	// onto CaseResult.
	scoreCaseID = "s1-oom-cascade"
	// failingAnswer is an answer that names none of the scoringCase keywords, so it
	// fails the schema tier; it is shared so a single literal drives every case.
	failingAnswer = "the box looked healthy to me."
)

// scoringCase returns a fully-labeled golden case whose run the passingRun helper
// satisfies on every tier, so each test can flip exactly one field (the answer,
// the called tools, or ExpectJudgeReject) to drive a single verdict. It returns a
// pointer because scoreCase takes the case by reference.
func scoringCase() *GoldenCase {
	return &GoldenCase{
		ID:                   scoreCaseID,
		UserQuery:            judgeQuery,
		Fixture:              "scenario1_oom.json",
		JudgePromptExtension: judgeExtension,
		ExpectedTools:        []string{toolBoots, toolQuery},
		ExpectedKeywords:     []string{keywordOOM},
		ForbiddenKeywords:    []string{keywordUnknownError},
		ScenarioClass:        1,
		MinRecursionDepth:    1,
		MustRecurse:          false,
		ExpectJudgeReject:    false,
	}
}

// passingRun returns a controller run that passes the rule and schema tiers for a
// scoringCase: it calls every expected tool, reaches the required depth, signals a
// terminal answer, and names the expected keyword without any forbidden one.
func passingRun() RunOutput {
	return RunOutput{
		Answer:         "checkout-api hit an OOM event at 09:00; the kernel reclaimed its pages.",
		ToolsCalled:    []string{toolBoots, toolQuery, toolCounts},
		RecursionDepth: 2,
		FinalCalled:    true,
	}
}

// sampleCost is the wall-clock and token cost the scorer records onto CaseResult.
func sampleCost() RunCost {
	return RunCost{Wall: 18 * time.Second, Tokens: 12400}
}

// approvingJudge returns a mock Judger scripted to approve every request.
func approvingJudge() *mockJudger {
	judge := new(mockJudger)
	judge.On("Judge", mock.Anything, mock.Anything).
		Return(JudgeVerdict{Critique: "", Approve: true}, nil)

	return judge
}

// rejectingJudge returns a mock Judger scripted to reject every request with the
// given critique.
func rejectingJudge(critique string) *mockJudger {
	judge := new(mockJudger)
	judge.On("Judge", mock.Anything, mock.Anything).
		Return(JudgeVerdict{Critique: critique, Approve: false}, nil)

	return judge
}

func TestScoreCaseMapsTierVerdictsStraightThrough(t *testing.T) {
	t.Parallel()

	judge := approvingJudge()

	got, err := scoreCase(context.Background(), judge, scoringCase(), passingRun(), sampleCost())
	require.NoError(t, err)

	// A normal case maps every tier verdict straight through: the rule and schema
	// tiers pass, and the judge tier mirrors the approval.
	assert.True(t, got.Tier1Pass)
	assert.True(t, got.Tier2Pass)
	assert.True(t, got.Tier3Pass)

	// Identity and cost ride along onto the result for the §17 table.
	assert.Equal(t, scoreCaseID, got.CaseID)
	assert.Equal(t, 12400, got.Tokens)
	assert.Equal(t, 18*time.Second, got.Wall)

	// scoreCase must build the JudgeRequest from the case's query and extension and
	// the run's answer, not from some other field; inspect the forwarded payload.
	require.Len(t, judge.Calls, 1)

	req, ok := judge.Calls[0].Arguments.Get(1).(JudgeRequest)
	require.True(t, ok)
	assert.Equal(t, judgeQuery, req.Question)
	assert.Equal(t, passingRun().Answer, req.Answer)
	assert.Equal(t, judgeExtension, req.Extension)

	judge.AssertExpectations(t)
}

func TestScoreCaseFailingRuleAndSchemaTiersMapStraightThrough(t *testing.T) {
	t.Parallel()

	run := passingRun()
	run.ToolsCalled = []string{toolBoots} // drops the expected journal.Query → Tier 1 fails.
	run.Answer = failingAnswer            // names no expected keyword → Tier 2 fails.

	judge := approvingJudge()

	got, err := scoreCase(context.Background(), judge, scoringCase(), run, sampleCost())
	require.NoError(t, err)

	// The failing rule and schema verdicts map straight through, independently of
	// the judge tier, which still passes on approval for a normal case.
	assert.False(t, got.Tier1Pass)
	assert.False(t, got.Tier2Pass)
	assert.True(t, got.Tier3Pass)

	judge.AssertExpectations(t)
}

func TestScoreCaseKnownBadRejectingJudgePasses(t *testing.T) {
	t.Parallel()

	golden := scoringCase()
	golden.ID = "s1-oom-hallucinated"
	golden.ExpectJudgeReject = true

	const critique = "the answer invents an ssh.service unit absent from the cited entries"

	judge := rejectingJudge(critique)

	got, err := scoreCase(context.Background(), judge, golden, passingRun(), sampleCost())
	require.NoError(t, err)

	// Known-bad inversion: a rejecting judge is the desired outcome, so the case
	// passes Tier 3 even though the judge withheld approval.
	assert.True(t, got.Tier3Pass)

	judge.AssertExpectations(t)
}

func TestScoreCaseKnownBadApprovingJudgeFails(t *testing.T) {
	t.Parallel()

	golden := scoringCase()
	golden.ExpectJudgeReject = true

	judge := approvingJudge()

	got, err := scoreCase(context.Background(), judge, golden, passingRun(), sampleCost())
	require.NoError(t, err)

	// Known-bad inversion: an approving judge rubber-stamps a deliberately
	// ungrounded answer, so the case fails Tier 3 despite the approval.
	assert.False(t, got.Tier3Pass)

	judge.AssertExpectations(t)
}

func TestScoreCaseJudgeErrorWrapsOops(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("judge responses call failed")

	judge := new(mockJudger)
	judge.On("Judge", mock.Anything, mock.Anything).
		Return(JudgeVerdict{Critique: "", Approve: false}, sentinel)

	got, err := scoreCase(context.Background(), judge, scoringCase(), passingRun(), sampleCost())
	require.Error(t, err)
	assert.False(t, got.Tier3Pass)
	assert.Empty(t, got.CaseID)

	// The wrap enriches the message with the case identifier while the judge's own
	// failure code surfaces, since oops folds a re-wrapped code to the innermost.
	var oopsErr oops.OopsError
	require.ErrorAs(t, err, &oopsErr)
	assert.Equal(t, "evals", oopsErr.Domain())
	assert.Equal(t, "judge_failed", oopsErr.Code())
	require.ErrorContains(t, err, scoreCaseID)
	require.ErrorIs(t, err, sentinel)

	judge.AssertExpectations(t)
}

func TestAggregateCountsTalliesAndPercentages(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Four scored cases yield Tier 1: 3/4, Tier 2: 2/4, Tier 3: 1/4.
	allPass, err := scoreCase(ctx, approvingJudge(), scoringCase(), passingRun(), sampleCost())
	require.NoError(t, err)

	judgeFails, err := scoreCase(ctx, rejectingJudge("ungrounded"), scoringCase(), passingRun(), sampleCost())
	require.NoError(t, err)

	schemaFailsRun := passingRun()
	schemaFailsRun.Answer = failingAnswer // names no expected keyword → Tier 2 fails.

	schemaFails, err := scoreCase(ctx, rejectingJudge("ungrounded"), scoringCase(), schemaFailsRun, sampleCost())
	require.NoError(t, err)

	allFailRun := passingRun()
	allFailRun.ToolsCalled = []string{toolBoots} // drops journal.Query → Tier 1 fails.
	allFailRun.Answer = failingAnswer            // names no expected keyword → Tier 2 fails.

	allFail, err := scoreCase(ctx, rejectingJudge("ungrounded"), scoringCase(), allFailRun, sampleCost())
	require.NoError(t, err)

	got := Aggregate([]CaseResult{allPass, judgeFails, schemaFails, allFail})

	assert.Equal(t, 3, got.Tier1.Passed)
	assert.Equal(t, 4, got.Tier1.Total)
	assert.InDelta(t, 75.0, got.Tier1.Percent, 0.0001)

	assert.Equal(t, 2, got.Tier2.Passed)
	assert.Equal(t, 4, got.Tier2.Total)
	assert.InDelta(t, 50.0, got.Tier2.Percent, 0.0001)

	assert.Equal(t, 1, got.Tier3.Passed)
	assert.Equal(t, 4, got.Tier3.Total)
	assert.InDelta(t, 25.0, got.Tier3.Percent, 0.0001)
}

func TestAggregateEmptyRunIsZero(t *testing.T) {
	t.Parallel()

	got := Aggregate(nil)

	assert.Equal(t, 0, got.Tier1.Total)
	assert.Equal(t, 0, got.Tier1.Passed)
	assert.Zero(t, got.Tier1.Percent)
	assert.Zero(t, got.Tier2.Percent)
	assert.Zero(t, got.Tier3.Percent)
}
