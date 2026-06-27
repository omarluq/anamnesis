package main

import (
	"github.com/samber/lo"
)

// Tier1Result reports the outcome of the Tier 1 rule-based check over a
// controller run: which expected tools were never called, the recursion depth
// reached against the required minimum, whether a terminal answer was signaled,
// and the overall pass verdict.
type Tier1Result struct {
	// MissingTools holds the expected tools the run never called.
	MissingTools []string
	// DepthReached is the recursion depth the run reached.
	DepthReached int
	// MinDepth is the minimum recursion depth the case required.
	MinDepth int
	// DepthMet is true when DepthReached is at least MinDepth.
	DepthMet bool
	// FinalCalled is true when the run signaled a terminal answer via agent.FINAL.
	FinalCalled bool
	// Pass is true when no expected tool is missing, the depth requirement is met,
	// and the run signaled a terminal answer.
	Pass bool
}

// Tier1 runs the rule-based check for a single controller run: every tool in
// expectedTools must have been called (exact match), the run's recursion depth
// must be at least minDepth, and the run must have signaled a terminal answer.
// All three conditions must hold for the result to pass.
func Tier1(run RunOutput, expectedTools []string, minDepth int) Tier1Result {
	missing := lo.Filter(expectedTools, func(tool string, _ int) bool {
		return !lo.Contains(run.ToolsCalled, tool)
	})

	depthMet := run.RecursionDepth >= minDepth
	pass := len(missing) == 0 && depthMet && run.FinalCalled

	return Tier1Result{
		MissingTools: missing,
		DepthReached: run.RecursionDepth,
		MinDepth:     minDepth,
		DepthMet:     depthMet,
		FinalCalled:  run.FinalCalled,
		Pass:         pass,
	}
}
