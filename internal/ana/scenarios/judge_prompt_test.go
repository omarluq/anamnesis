package scenarios_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/scenarios"
)

func TestJudgeSystemPromptStructure(t *testing.T) {
	t.Parallel()

	prompt := scenarios.JudgeSystemPrompt

	require.NotEmpty(t, prompt)
	assert.True(t, strings.HasPrefix(prompt,
		"You are an audit judge reviewing a Linux system investigation."),
		"prompt must open with the verbatim judge identity sentence")
	assert.True(t, strings.HasSuffix(prompt, "Be lenient on style."),
		"prompt must close with the lenient-on-style directive")
}

func TestJudgeSystemPromptOutputKeys(t *testing.T) {
	t.Parallel()

	prompt := scenarios.JudgeSystemPrompt

	assert.Contains(t, prompt, "Respond in JSON:",
		"prompt must explicitly require JSON-shaped output")
	assert.Contains(t, prompt, `"approve": true | false`,
		"prompt must shape the verdict as a boolean approve field")

	keys := []string{`"approve"`, `"critique"`}

	require.Len(t, keys, 2, "both output JSON keys must be covered")

	for _, want := range keys {
		t.Run(want, func(t *testing.T) {
			t.Parallel()

			assert.Contains(t, prompt, want)
		})
	}
}

func TestJudgeSystemPromptGroundingAndStrictness(t *testing.T) {
	t.Parallel()

	prompt := scenarios.JudgeSystemPrompt

	assert.Contains(t, prompt, "supporting cited entry",
		"prompt must require each claim to map to a supporting cited entry")
	assert.Contains(t, prompt, "Be strict on ungrounded claims",
		"prompt must instruct the judge to be strict on ungrounded claims")
}
