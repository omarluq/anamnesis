package rlm

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/repl"
	"github.com/omarluq/anamnesis/internal/openai"
)

func TestCapForHistoryLeavesSmallTextUnchanged(t *testing.T) {
	t.Parallel()

	text := "finding: checkout-api OOM-killed at 09:01"

	assert.Equal(t, text, capForHistory(text), "text within the cap re-enters history verbatim")
}

func TestCapForHistoryBoundsOversizedText(t *testing.T) {
	t.Parallel()

	// A turn ending on a bare []Entry expression renders a huge value; the cap must
	// bound what re-enters the controller transcript so the context cannot grow
	// without limit — the structural RLM property the prompt should not have to
	// enforce on its own.
	oversized := strings.Repeat("a", maxHistoryFieldBytes*3)

	capped := capForHistory(oversized)

	require.Less(t, len(capped), len(oversized), "an oversized value is truncated")
	assert.LessOrEqual(t, len(capped), maxHistoryFieldBytes+64, "the kept prefix stays near the byte cap")
	assert.True(t, strings.HasPrefix(oversized, capped[:maxHistoryFieldBytes]),
		"the retained text is the original head, not a re-summary")
	assert.Contains(t, capped, "bytes elided to bound controller context",
		"the elision marker records how much was dropped")
}

func TestCapForHistoryCutsOnRuneBoundary(t *testing.T) {
	t.Parallel()

	// Multi-byte runes straddling the byte cap must not be split, so the truncated
	// value stays valid UTF-8 rather than ending in a mangled half-rune.
	oversized := strings.Repeat("é", maxHistoryFieldBytes) // two bytes each, so over the cap

	capped := capForHistory(oversized)

	assert.True(t, utf8.ValidString(capped), "the truncated text remains valid UTF-8")
	assert.Contains(t, capped, "bytes elided", "the elision marker is appended")
}

func TestThinkingTracePrefersReasoningSummary(t *testing.T) {
	t.Parallel()

	// When the Responses API returned a reasoning summary, the turn's thinking trace
	// renders that fuller prose rather than the terse structured Thinking field.
	response := openai.ControllerResponse{
		Thinking:  "inspect sshd",
		Code:      "journal.Boots()",
		Done:      false,
		Reasoning: "I listed the boots, then narrowed in on the unit that OOM-killed at 09:01.",
	}

	assert.Equal(t, response.Reasoning, thinkingTrace(response),
		"the reasoning summary is rendered as the turn's thinking when present")
}

func TestThinkingTraceFallsBackToThinkingFieldWhenNoSummary(t *testing.T) {
	t.Parallel()

	// With no reasoning summary (empty Reasoning), the trace falls back to the brief
	// structured Thinking field so a turn that returned no summary still shows its
	// rationale.
	response := openai.ControllerResponse{
		Thinking:  "list the recent boots",
		Code:      "journal.Boots()",
		Done:      false,
		Reasoning: "",
	}

	assert.Equal(t, response.Thinking, thinkingTrace(response),
		"an empty reasoning summary falls back to the brief structured thinking field")
}

func TestCodeOutputLeadsWithErrorThenStdoutThenRetval(t *testing.T) {
	t.Parallel()

	// A turn that printed, returned a value, and then errored renders all three for
	// the code block: the error first so a failed turn shows what went wrong, then the
	// trailing-newline-trimmed stdout, then the final expression's value.
	result := repl.Result{
		Retval: reflect.ValueOf(42),
		Stdout: "scanning boots\n",
		Stderr: "",
	}

	output := codeOutput(result, errors.New("boom"))

	assert.Equal(t, "error: boom\nscanning boots\n42", output)
}

func TestCodeOutputOmitsEmptySections(t *testing.T) {
	t.Parallel()

	// A clean turn with no return value renders just its stdout, trimmed, so a silent
	// turn yields an empty string rather than blank-line noise.
	result := repl.Result{
		Retval: reflect.Value{},
		Stdout: "only stdout\n",
		Stderr: "",
	}

	assert.Equal(t, "only stdout", codeOutput(result, nil))
	assert.Empty(t, codeOutput(repl.Result{Retval: reflect.Value{}, Stdout: "", Stderr: ""}, nil),
		"a turn that printed nothing and returned nothing renders an empty block")
}
