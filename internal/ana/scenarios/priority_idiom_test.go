package scenarios_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/repl"
	"github.com/omarluq/anamnesis/internal/ana/scenarios"
)

// TestControllerPromptPriorityIdiomCompiles guards the priority-filter idiom the
// controller prompt teaches: it must actually compile in the mvm interpreter the
// model writes Go for. It asserts the prompt teaches the &prio pointer form and
// never the invalid new(value) form, then proves empirically against a real
// interpreter that the &prio form evaluates clean while new(4) is rejected — so the
// most common investigative operation can never silently regress to a non-compiling
// idiom.
func TestControllerPromptPriorityIdiomCompiles(t *testing.T) {
	t.Parallel()

	prompt := scenarios.ControllerSystemPrompt
	assert.Contains(t, prompt, "MaxPriority: &prio",
		"the prompt teaches the *int pointer idiom")
	assert.NotContains(t, prompt, "new(4)",
		"the prompt never teaches the invalid new(value) idiom")
	assert.NotContains(t, prompt, "new(3)",
		"the prompt never teaches the invalid new(value) idiom")

	interpreter := repl.NewInterpreter()

	_, err := interpreter.Eval("priority_idiom", "prio := 4\n_ = &prio")
	require.NoError(t, err,
		"the &prio idiom the prompt teaches must compile in mvm")

	_, err = interpreter.Eval("invalid_new_value", "_ = new(4)")
	require.Error(t, err,
		"new(value) is invalid Go, so the interpreter rejects what the prompt no longer teaches")
}
