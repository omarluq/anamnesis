package scenarios_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/scenarios"
)

func TestSubControllerPromptStructure(t *testing.T) {
	t.Parallel()

	prompt := scenarios.SubControllerPrompt

	require.NotEmpty(t, prompt)
	assert.True(t, strings.HasPrefix(prompt,
		"You are a focused sub-investigation controller"),
		"prompt must open with the sub-investigation controller identity")
	assert.True(t, strings.HasSuffix(prompt, "Do not hallucinate."),
		"prompt must close with the no-hallucination directive")
}

func TestSubControllerPromptExposesHostAndAgentSurface(t *testing.T) {
	t.Parallel()

	surface := []string{
		"journal.Query",
		"systemd.UnitStatus",
		"agent.Query",
		"agent.FINAL",
	}

	for _, want := range surface {
		t.Run(want, func(t *testing.T) {
			t.Parallel()

			assert.Contains(t, scenarios.SubControllerPrompt, want,
				"the child controller must see the same host and agent surface")
		})
	}
}

func TestSubControllerPromptDeclaresSharedBudget(t *testing.T) {
	t.Parallel()

	prompt := scenarios.SubControllerPrompt

	assert.Contains(t, prompt, "SHARED across the whole investigation tree",
		"the child must know its depth and sub-call budgets are shared tree-wide")
	assert.Contains(t, prompt, "Recurse further ONLY when",
		"the child must be steered away from needless recursion")
}
