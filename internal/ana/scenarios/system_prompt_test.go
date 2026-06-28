package scenarios_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/scenarios"
)

func TestControllerSystemPromptStructure(t *testing.T) {
	t.Parallel()

	prompt := scenarios.ControllerSystemPrompt

	require.NotEmpty(t, prompt)
	assert.True(t, strings.HasPrefix(prompt,
		"You are anamnesis (ana for short), an expert Linux SRE"),
		"prompt must open with the verbatim controller identity sentence")
	assert.True(t, strings.HasSuffix(prompt, "Do not hallucinate root causes."),
		"prompt must close with the no-hallucination directive")
}

func TestControllerSystemPromptHostSignatures(t *testing.T) {
	t.Parallel()

	signatures := []string{
		"journal.Boots() []BootInfo",
		"journal.Query(filter *QueryFilter) []Entry",
		"journal.Counts(bootID string, byField string) map[string]int",
		"journal.Unique(field string, filter *QueryFilter) []string",
		"systemd.UnitStatus(name string) UnitStatus",
		"systemd.ListUnits(state string) []Unit",
		"agent.Query(prompt string, ctx any) string",
		"agent.QueryBatched(prompts []string, ctxs []any) []string",
		"agent.Cite(entries []Entry)",
		"agent.FINAL(answer string)",
		"agent.FINAL_VAR(varname string)",
	}

	require.Len(t, signatures, 11, "all 11 host signatures must be covered")

	for _, want := range signatures {
		t.Run(want, func(t *testing.T) {
			t.Parallel()

			assert.Contains(t, scenarios.ControllerSystemPrompt, want)
		})
	}
}

func TestControllerSystemPromptBudgets(t *testing.T) {
	t.Parallel()

	budgets := []string{"30 turns", "recursion depth 3", "60 sub-calls", "30min"}

	require.Len(t, budgets, 4, "all four budget figures must be covered")

	for _, want := range budgets {
		t.Run(want, func(t *testing.T) {
			t.Parallel()

			assert.Contains(t, scenarios.ControllerSystemPrompt, want)
		})
	}
}

func TestControllerSystemPromptOutputContract(t *testing.T) {
	t.Parallel()

	keys := []string{`"thinking"`, `"code"`, `"done"`}

	require.Len(t, keys, 3, "all three output JSON keys must be covered")

	for _, want := range keys {
		t.Run(want, func(t *testing.T) {
			t.Parallel()

			assert.Contains(t, scenarios.ControllerSystemPrompt, want)
		})
	}
}

func TestControllerSystemPromptJournaldFields(t *testing.T) {
	t.Parallel()

	fields := []string{
		"_BOOT_ID",
		"_SYSTEMD_UNIT",
		"PRIORITY",
		"MESSAGE",
		"_PID",
		"_COMM",
		"__CURSOR",
	}

	require.Len(t, fields, 7, "all seven journald field names must be covered")

	for _, want := range fields {
		t.Run(want, func(t *testing.T) {
			t.Parallel()

			assert.Contains(t, scenarios.ControllerSystemPrompt, want)
		})
	}
}
