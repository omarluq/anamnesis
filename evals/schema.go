package main

import (
	"strings"

	"github.com/samber/lo"
)

// MaxAnswerWords is the inclusive upper bound on FINAL answer length, in
// whitespace-separated words, that a Tier 2 pass allows.
const MaxAnswerWords = 800

// Tier2Result reports the outcome of the Tier 2 schema check over a FINAL
// answer: which required keywords were missing, which forbidden keywords
// appeared, the measured word count, and the overall pass verdict.
type Tier2Result struct {
	// MissingKeywords holds the expected keywords absent from the answer.
	MissingKeywords []string
	// ForbiddenPresent holds the forbidden keywords found in the answer.
	ForbiddenPresent []string
	// WordCount is the number of whitespace-separated words in the answer.
	WordCount int
	// Pass is true when no keyword is missing, none is forbidden, and the answer
	// is within MaxAnswerWords words.
	Pass bool
}

// Tier2 runs the schema check for a single answer: it must contain every keyword
// in expected (case-insensitive substring), none in forbidden, and stay within
// MaxAnswerWords words. Keyword matching is case-insensitive so that, for
// example, "oom" in the answer satisfies an expected "OOM".
func Tier2(answer string, expected, forbidden []string) Tier2Result {
	lowered := strings.ToLower(answer)

	missing := lo.Filter(expected, func(keyword string, _ int) bool {
		return !strings.Contains(lowered, strings.ToLower(keyword))
	})

	present := lo.Filter(forbidden, func(keyword string, _ int) bool {
		return strings.Contains(lowered, strings.ToLower(keyword))
	})

	wordCount := len(strings.Fields(answer))
	pass := len(missing) == 0 && len(present) == 0 && wordCount <= MaxAnswerWords

	return Tier2Result{
		MissingKeywords:  missing,
		ForbiddenPresent: present,
		WordCount:        wordCount,
		Pass:             pass,
	}
}
