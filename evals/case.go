// Package main implements the anamnesis eval harness: it loads the labeled
// golden cases and scores controller runs across the rule, schema, and judge
// tiers.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/samber/oops"
)

// GoldenCase is a single labeled eval case loaded from the golden JSONL file. It
// pairs a user query and its journal fixture with the assertions each tier of
// the harness checks against a controller run.
type GoldenCase struct {
	// ID is the stable, human-readable identifier for the case.
	ID string `json:"id"`
	// UserQuery is the natural-language question posed to the controller.
	UserQuery string `json:"user_query"`
	// Fixture names the journal fixture export that backs this case's run.
	Fixture string `json:"fixture"`
	// JudgePromptExtension is extra grounding context handed to the Tier 3 judge.
	JudgePromptExtension string `json:"judge_prompt_extension"`
	// ExpectedTools lists the host tools (e.g. "journal.Query") a Tier 1 pass
	// requires the controller to have called.
	ExpectedTools []string `json:"expected_tools"`
	// ExpectedKeywords lists the substrings a Tier 2 pass requires in the answer.
	ExpectedKeywords []string `json:"expected_keywords_in_answer"`
	// ForbiddenKeywords lists the substrings a Tier 2 pass forbids in the answer.
	ForbiddenKeywords []string `json:"forbidden_keywords"`
	// ScenarioClass is the demo scenario family (1, 2, or 3) the case belongs to.
	ScenarioClass int `json:"scenario_class"`
	// MinRecursionDepth is the lowest recursion depth a Tier 1 pass requires.
	MinRecursionDepth int `json:"min_recursion_depth"`
	// MustRecurse is true when a Tier 1 pass requires at least one sub-call.
	MustRecurse bool `json:"must_recurse"`
	// ExpectJudgeReject is true for known-bad cases the Tier 3 judge must reject.
	ExpectJudgeReject bool `json:"expect_judge_reject"`
}

// LoadCases reads path as JSONL — one GoldenCase object per non-blank line — and
// returns the parsed cases, wrapping any read or decode failure with oops.
func LoadCases(path string) ([]GoldenCase, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, oops.
			In("evals").
			Code("read_golden_file").
			Wrapf(err, "read golden cases from %q", path)
	}

	var cases []GoldenCase

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for lineNum := 1; scanner.Scan(); lineNum++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var golden GoldenCase
		if decodeErr := json.Unmarshal([]byte(line), &golden); decodeErr != nil {
			return nil, oops.
				In("evals").
				Code("malformed_case").
				Wrapf(decodeErr, "parse golden case on line %d", lineNum)
		}

		cases = append(cases, golden)
	}

	if scanErr := scanner.Err(); scanErr != nil {
		return nil, oops.
			In("evals").
			Code("scan_golden_file").
			Wrapf(scanErr, "scan golden cases from %q", path)
	}

	return cases, nil
}
