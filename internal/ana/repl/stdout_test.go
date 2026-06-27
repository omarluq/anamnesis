package repl_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/repl"
)

// TestEvalCapturesStdoutPerTurn proves that interpreted fmt output is redirected
// into the per-turn buffer and that the buffer is reset between Eval calls, so a
// turn never sees stdout left over from the previous turn.
func TestEvalCapturesStdoutPerTurn(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	first, err := interpreter.Eval("turn_0", `fmt.Println("hello")`)
	require.NoError(t, err)
	assert.Equal(t, "hello\n", first.Stdout, "Println output is captured verbatim")

	second, err := interpreter.Eval("turn_1", `fmt.Print("x")`)
	require.NoError(t, err)
	assert.Equal(t, "x", second.Stdout, "no leftover from the prior turn")
}
