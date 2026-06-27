package scenarios_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/scenarios"
)

func TestSubLLMSystemPromptStructure(t *testing.T) {
	t.Parallel()

	prompt := scenarios.SubLLMSystemPrompt

	require.NotEmpty(t, prompt)
	assert.True(t, strings.HasPrefix(prompt,
		"You are a focused analysis sub-call in a recursive investigation."),
		"prompt must open with the verbatim sub-call identity sentence")
	assert.True(t, strings.HasSuffix(prompt, "Markdown allowed but kept minimal."),
		"prompt must close with the markdown brevity directive")
}

func TestSubLLMSystemPromptInvariants(t *testing.T) {
	t.Parallel()

	invariants := []string{
		"context insufficient to answer",
		"50-200 words",
		"ONLY",
	}

	require.Len(t, invariants, 3, "all three sub-LLM invariants must be covered")

	for _, want := range invariants {
		t.Run(want, func(t *testing.T) {
			t.Parallel()

			assert.Contains(t, scenarios.SubLLMSystemPrompt, want)
		})
	}
}

func TestSubLLMSystemPromptContextOnlyConstraint(t *testing.T) {
	t.Parallel()

	prompt := scenarios.SubLLMSystemPrompt

	assert.Contains(t, prompt, "using ONLY the information in the context",
		"prompt must restrict answers to the supplied context only")
	assert.Contains(t, prompt, "Do not invent fields, units, timestamps, or causes",
		"prompt must forbid inventing facts absent from the context")
}
