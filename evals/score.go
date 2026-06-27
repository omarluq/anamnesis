package main

import (
	"context"
	"time"

	"github.com/samber/lo"
	"github.com/samber/oops"
)

// RunOutput captures the gradeable result of a single controller run that the
// eval tiers inspect: the host tools the controller called, the terminal answer
// it produced, the recursion depth it reached, and whether it ever signaled a
// terminal answer.
type RunOutput struct {
	// Answer is the terminal answer text produced by the agent.FINAL signal.
	Answer string
	// ToolsCalled lists the host tools (e.g. "journal.Query") the controller
	// invoked during the run, in call order.
	ToolsCalled []string
	// RecursionDepth is the deepest agent.Query recursion level the run reached.
	RecursionDepth int
	// FinalCalled is true when the controller signaled a terminal answer via
	// agent.FINAL (or agent.FINAL_VAR).
	FinalCalled bool
}

// RunCost is the measured cost of a single controller run that the scorer records
// alongside the tier verdicts: the wall-clock time the run took and the total
// tokens it consumed. The eval harness observes these while executing a case and
// hands them to scoreCase so each CaseResult carries the figures the §17 results
// table renders.
type RunCost struct {
	// Wall is the wall-clock duration the run took end to end.
	Wall time.Duration
	// Tokens is the total tokens (input plus output) the run consumed.
	Tokens int
}

// CaseResult is the scored outcome of one golden case: the per-tier pass verdicts
// (with the known-bad inversion already applied to Tier 3) plus the run's identity
// and cost. Aggregate tallies the verdicts across many of these, and the §17
// results table renders one row per CaseResult.
type CaseResult struct {
	// CaseID is the golden case's stable identifier.
	CaseID string
	// Wall is the wall-clock duration the run took end to end.
	Wall time.Duration
	// Tokens is the total tokens the run consumed.
	Tokens int
	// Tier1Pass is true when the run passed the rule-based tier.
	Tier1Pass bool
	// Tier2Pass is true when the run passed the schema tier.
	Tier2Pass bool
	// Tier3Pass is true when the run passed the judge tier after the known-bad
	// inversion: a normal case passes on approval, a known-bad case on rejection.
	Tier3Pass bool
}

// scoreCase scores one controller run against its golden case across all three
// tiers and returns the combined CaseResult. The rule and schema tiers map
// straight through to their pass verdicts; the judge tier is inverted for
// known-bad cases (those whose golden label sets ExpectJudgeReject), where a
// rejecting judge is the desired outcome. A judge failure is wrapped with the case
// identifier and returned with an empty result, surfacing in the evals domain
// under the judge's own failure code so the harness can report which case could
// not be scored.
func scoreCase(
	ctx context.Context,
	judge Judger,
	golden *GoldenCase,
	run RunOutput,
	cost RunCost,
) (CaseResult, error) {
	tier1 := Tier1(run, golden.ExpectedTools, golden.MinRecursionDepth)
	tier2 := Tier2(run.Answer, golden.ExpectedKeywords, golden.ForbiddenKeywords)

	tier3, err := Tier3(ctx, judge, golden.UserQuery, run.Answer, golden.JudgePromptExtension)
	if err != nil {
		return CaseResult{}, oops.
			In("evals").
			Wrapf(err, "score case %q", golden.ID)
	}

	return CaseResult{
		CaseID:    golden.ID,
		Wall:      cost.Wall,
		Tokens:    cost.Tokens,
		Tier1Pass: tier1.Pass,
		Tier2Pass: tier2.Pass,
		Tier3Pass: applyKnownBadInversion(tier3.Approve, golden.ExpectJudgeReject),
	}, nil
}

// applyKnownBadInversion maps the judge's approval onto the Tier 3 pass verdict: a
// normal case passes when the judge approved, while a known-bad case passes only
// when the judge rejected it. This inversion is the §18 self-preference guard — a
// judge that rubber-stamps a deliberately ungrounded answer fails the case instead
// of passing it.
func applyKnownBadInversion(approved, expectReject bool) bool {
	if expectReject {
		return !approved
	}

	return approved
}

// TierTally is one tier's pass count across a scored eval run: how many cases
// passed the tier out of the total scored, and the derived pass percentage.
type TierTally struct {
	// Passed is the number of cases that passed the tier.
	Passed int
	// Total is the number of cases scored.
	Total int
	// Percent is the pass percentage (Passed/Total*100), or 0 when no cases ran.
	Percent float64
}

// newTierTally builds a TierTally for passed out of total, deriving the pass
// percentage and guarding the empty run so a zero total reports 0 rather than
// dividing by zero.
func newTierTally(passed, total int) TierTally {
	return TierTally{
		Passed:  passed,
		Total:   total,
		Percent: passPercent(passed, total),
	}
}

// Tallies is the per-tier aggregate over a scored eval run: independent pass
// counts for each of the three tiers, mirroring the §17 aggregate line
// "Tier 1: x/n (p%) | Tier 2: y/n (q%) | Tier 3: z/n (r%)".
type Tallies struct {
	// Tier1 is the rule-based tier's pass tally.
	Tier1 TierTally
	// Tier2 is the schema tier's pass tally.
	Tier2 TierTally
	// Tier3 is the judge tier's pass tally, after the known-bad inversion.
	Tier3 TierTally
}

// Aggregate tallies the per-tier pass counts and percentages across results,
// counting each tier independently so a case that fails one tier still credits the
// tiers it passed. The total is the number of results; an empty input yields zero
// tallies.
func Aggregate(results []CaseResult) Tallies {
	total := len(results)

	tier1 := lo.CountBy(results, func(result CaseResult) bool { return result.Tier1Pass })
	tier2 := lo.CountBy(results, func(result CaseResult) bool { return result.Tier2Pass })
	tier3 := lo.CountBy(results, func(result CaseResult) bool { return result.Tier3Pass })

	return Tallies{
		Tier1: newTierTally(tier1, total),
		Tier2: newTierTally(tier2, total),
		Tier3: newTierTally(tier3, total),
	}
}

// passPercent returns passed/total as a percentage, or 0 when total is 0 so an
// empty eval run reports cleanly instead of dividing by zero.
func passPercent(passed, total int) float64 {
	if total == 0 {
		return 0
	}

	return float64(passed) / float64(total) * 100
}
