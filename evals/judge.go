package main

import (
	"context"

	"github.com/samber/oops"
)

// JudgeVerdict is the audit judge's decision on a controller run's final answer:
// whether the answer is approved as grounded, plus a short critique naming what
// to fix when it is not. It mirrors the SPEC §16 judge reply (an approve flag and
// a 1-3 sentence critique) at the eval-harness boundary, decoupled from the
// openai layer that produces it so the Tier 3 check depends only on this package.
type JudgeVerdict struct {
	// Critique is empty on approval, else a 1-3 sentence explanation of what to fix.
	Critique string
	// Approve is true when the judge found every factual claim grounded in a cited
	// entry.
	Approve bool
}

// JudgeRequest is the payload Tier3 hands a Judger for one case: the original user
// question, the controller's final answer, and the case-specific grounding
// extension (the golden case's judge_prompt_extension) the judge weighs as extra
// context per SPEC §17. The Judger implementation renders these into the §16 judge
// prompt; Tier3 stays agnostic to that rendering.
type JudgeRequest struct {
	// Question is the original natural-language user query the run answered.
	Question string
	// Answer is the controller's terminal answer the judge audits.
	Answer string
	// Extension is the golden case's judge_prompt_extension: the extra grounding
	// requirement handed to the Tier 3 judge as additional context.
	Extension string
}

// Judger is the Tier 3 audit-judge seam: it reviews a JudgeRequest and returns the
// judge's verdict on whether the answer is grounded. The openai judge layer
// satisfies it in production; tests inject a mock so the Tier 3 check runs with no
// live network.
type Judger interface {
	// Judge reviews the request and returns the audit verdict, or an error when the
	// underlying judge call fails.
	Judge(ctx context.Context, req JudgeRequest) (JudgeVerdict, error)
}

// Tier3Result reports the outcome of the Tier 3 LLM-judge check over a controller
// run: the judge's critique and whether it approved the answer. The per-case
// scorer derives the pass verdict from Approve, inverting it for known-bad
// (ExpectJudgeReject) cases where a rejecting judge is the desired outcome.
type Tier3Result struct {
	// Critique is the judge's explanation of what to fix, empty on approval.
	Critique string
	// Approve is true when the judge approved the answer as grounded.
	Approve bool
}

// Tier3 runs the LLM-judge check for a single controller run: it hands the judge
// the user question, the run's final answer, and the case's judge_prompt_extension
// as extra grounding context, then maps the returned verdict onto a Tier3Result. A
// judge failure is wrapped with oops so it surfaces in the evals domain rather than
// as a bare collaborator error.
func Tier3(ctx context.Context, judge Judger, question, answer, extension string) (Tier3Result, error) {
	verdict, err := judge.Judge(ctx, JudgeRequest{
		Question:  question,
		Answer:    answer,
		Extension: extension,
	})
	if err != nil {
		return Tier3Result{}, oops.
			In("evals").
			Code("judge_failed").
			Wrapf(err, "tier 3 judge for query %q", question)
	}

	return Tier3Result(verdict), nil
}
