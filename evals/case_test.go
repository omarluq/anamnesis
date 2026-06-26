package main_test

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samber/oops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	evalmain "github.com/omarluq/anamnesis/evals"
)

const (
	// keywordOOM is reused across the schema cases as an expected keyword.
	keywordOOM = "OOM"
	// keywordUnknownError is reused across the schema cases as a forbidden keyword.
	keywordUnknownError = "unknown error"
)

// goldenLineOne is the first JSONL record: a single-boot, non-recursive case
// that the Tier 3 judge should accept. Fragments are concatenated so each source
// line stays within the column limit while the on-disk record is one line.
const goldenLineOne = `{"id":"s1-oom-cascade","scenario_class":1,` +
	`"user_query":"What was wrong with my box around 09:00?",` +
	`"fixture":"scenario1_oom.json",` +
	`"expected_tools":["journal.Boots","journal.Counts","journal.Query"],` +
	`"must_recurse":false,"min_recursion_depth":0,` +
	`"expected_keywords_in_answer":["OOM","memory","checkout-api"],` +
	`"forbidden_keywords":["unknown error"],` +
	`"judge_prompt_extension":"identify a memory pressure event in checkout-api",` +
	`"expect_judge_reject":false}`

// goldenLineTwo is the second JSONL record: a recursive boot-diff known-bad case
// that the Tier 3 judge must reject. Every field differs from goldenLineOne so
// the round-trip assertion exercises both branches of each scalar field.
const goldenLineTwo = `{"id":"s2-boot-diff-bad","scenario_class":2,` +
	`"user_query":"Diff the last two boots.",` +
	`"fixture":"scenario2_bootdiff.json",` +
	`"expected_tools":["journal.Boots","journal.QueryBatched"],` +
	`"must_recurse":true,"min_recursion_depth":2,` +
	`"expected_keywords_in_answer":["ssh.service","boot"],` +
	`"forbidden_keywords":["hallucinated"],` +
	`"judge_prompt_extension":"reject if the answer invents an absent unit",` +
	`"expect_judge_reject":true}`

func writeJSONL(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "golden.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

func TestLoadCasesRoundTrip(t *testing.T) {
	t.Parallel()

	path := writeJSONL(t, goldenLineOne+"\n"+goldenLineTwo+"\n")

	cases, err := evalmain.LoadCases(path)
	require.NoError(t, err)
	require.Len(t, cases, 2)

	wantOne := evalmain.GoldenCase{
		ExpectedTools:        []string{"journal.Boots", "journal.Counts", "journal.Query"},
		ExpectedKeywords:     []string{keywordOOM, "memory", "checkout-api"},
		ForbiddenKeywords:    []string{keywordUnknownError},
		ID:                   "s1-oom-cascade",
		UserQuery:            "What was wrong with my box around 09:00?",
		Fixture:              "scenario1_oom.json",
		JudgePromptExtension: "identify a memory pressure event in checkout-api",
		ScenarioClass:        1,
		MinRecursionDepth:    0,
		MustRecurse:          false,
		ExpectJudgeReject:    false,
	}
	assert.Equal(t, wantOne, cases[0])

	wantTwo := evalmain.GoldenCase{
		ExpectedTools:        []string{"journal.Boots", "journal.QueryBatched"},
		ExpectedKeywords:     []string{"ssh.service", "boot"},
		ForbiddenKeywords:    []string{"hallucinated"},
		ID:                   "s2-boot-diff-bad",
		UserQuery:            "Diff the last two boots.",
		Fixture:              "scenario2_bootdiff.json",
		JudgePromptExtension: "reject if the answer invents an absent unit",
		ScenarioClass:        2,
		MinRecursionDepth:    2,
		MustRecurse:          true,
		ExpectJudgeReject:    true,
	}
	assert.Equal(t, wantTwo, cases[1])

	// Call out the two fields the acceptance names explicitly so a regression in
	// either tag is unmistakable.
	assert.Equal(t, "scenario1_oom.json", cases[0].Fixture)
	assert.False(t, cases[0].ExpectJudgeReject)
	assert.Equal(t, "scenario2_bootdiff.json", cases[1].Fixture)
	assert.True(t, cases[1].ExpectJudgeReject)
}

func TestLoadCasesSkipsBlankLines(t *testing.T) {
	t.Parallel()

	path := writeJSONL(t, "\n"+goldenLineOne+"\n\n   \n"+goldenLineTwo+"\n")

	cases, err := evalmain.LoadCases(path)
	require.NoError(t, err)

	assert.Len(t, cases, 2)
	assert.Equal(t, "s1-oom-cascade", cases[0].ID)
	assert.Equal(t, "s2-boot-diff-bad", cases[1].ID)
}

func TestLoadCasesHandlesLargeLine(t *testing.T) {
	t.Parallel()

	// Pad judge_prompt_extension so the single JSONL record exceeds bufio's
	// 64 KiB default scan-token limit, exercising the raised scanner buffer.
	largeExtension := strings.Repeat("a", bufio.MaxScanTokenSize+1)
	record := `{"id":"s1-large","scenario_class":1,` +
		`"judge_prompt_extension":"` + largeExtension + `"}`
	require.Greater(t, len(record), bufio.MaxScanTokenSize)

	cases, err := evalmain.LoadCases(writeJSONL(t, record+"\n"))
	require.NoError(t, err)
	require.Len(t, cases, 1)

	assert.Equal(t, "s1-large", cases[0].ID)
	assert.Equal(t, largeExtension, cases[0].JudgePromptExtension)
}

func TestLoadCasesMalformedLineWrapsOops(t *testing.T) {
	t.Parallel()

	path := writeJSONL(t, goldenLineOne+"\n{\"id\":\"broken\", not valid json\n")

	cases, err := evalmain.LoadCases(path)
	require.Error(t, err)
	assert.Nil(t, cases)

	var oopsErr oops.OopsError
	require.ErrorAs(t, err, &oopsErr)
	assert.Equal(t, "evals", oopsErr.Domain())
	assert.Equal(t, "malformed_case", oopsErr.Code())
	assert.Contains(t, err.Error(), "line 2")
}

func TestLoadCasesMissingFileWrapsOops(t *testing.T) {
	t.Parallel()

	cases, err := evalmain.LoadCases(filepath.Join(t.TempDir(), "absent.jsonl"))
	require.Error(t, err)
	assert.Nil(t, cases)

	var oopsErr oops.OopsError
	require.ErrorAs(t, err, &oopsErr)
	assert.Equal(t, "evals", oopsErr.Domain())
	assert.Equal(t, "read_golden_file", oopsErr.Code())
}
