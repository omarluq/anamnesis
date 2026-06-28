package repl_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/samber/oops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/repl"
)

// TestInterpreterStatePersistsAcrossEval binds a variable in one Eval and reads
// it in a second on the same Interpreter, proving both that session state
// survives across calls and that the final expression's value crosses back to
// the host as a usable reflect.Value.
func TestInterpreterStatePersistsAcrossEval(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	bound, err := interpreter.Eval("turn_0", "x := 21")
	require.NoError(t, err)
	require.True(t, bound.Retval.IsValid())
	assert.Equal(t, int64(21), bound.Retval.Int(), "the declaration's value crosses back")

	result, err := interpreter.Eval("turn_1", "x * 2")
	require.NoError(t, err)
	require.True(t, result.Retval.IsValid(), "x must still be in scope from the prior Eval")

	assert.Equal(t, reflect.Int, result.Retval.Kind())
	assert.Equal(t, int64(42), result.Retval.Int())
}

// TestInterpreterRedeclareAcrossEval probes whether re-declaring a persisted
// top-level variable with := in a later Eval errors (Go's "no new variables on the
// left side of :=") or is tolerated, so the §14 prompt can steer the controller to
// reuse a persisted variable with = rather than re-declaring it with := every turn.
func TestInterpreterRedeclareAcrossEval(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	_, err := interpreter.Eval("turn_0", "n := 1")
	require.NoError(t, err)

	_, redeclareErr := interpreter.Eval("turn_1", "n := 2")
	require.NoError(t, redeclareErr,
		"mvm tolerates redeclaring a persisted top-level var with := across Eval — it does not raise Go's "+
			"\"no new variables on the left side of :=\", so the prompt need not forbid re-declaration")

	reassigned, err := interpreter.Eval("turn_2", "n = 3\nn")
	require.NoError(t, err, "reassigning a persisted var with = must work across turns")
	assert.Equal(t, int64(3), reassigned.Retval.Int())
}

// TestInterpreterEvalWrapsError checks that a compile fault surfaces as an
// oops error carrying the repl domain and the eval_failed code rather than a
// bare interpreter error.
func TestInterpreterEvalWrapsError(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	result, err := interpreter.Eval("broken", "this is not valid go (")
	require.Error(t, err)
	assert.False(t, result.Retval.IsValid())

	var oopsErr oops.OopsError

	require.ErrorAs(t, err, &oopsErr)
	assert.Equal(t, "repl", oopsErr.Domain())
	assert.Equal(t, "eval_failed", oopsErr.Code())
}

// TestEvalContextReturnsForFastCode proves EvalContext runs ordinary code to
// completion under a generous timeout, returning its captured stdout and the final
// expression's value exactly as the synchronous Eval would.
func TestEvalContextReturnsForFastCode(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	result, err := interpreter.EvalContext(
		context.Background(),
		time.Second,
		"fast",
		`fmt.Println("done"); 21 * 2`,
	)
	require.NoError(t, err)
	assert.Equal(t, "done\n", result.Stdout)
	require.True(t, result.Retval.IsValid())
	assert.Equal(t, int64(42), result.Retval.Int())
}

// TestEvalContextTimesOutOnNonTerminatingLoop proves a turn whose generated Go never
// returns is bounded by the per-eval timeout rather than hanging the caller:
// EvalContext returns promptly with an eval_timed_out error and the best-effort
// partial stdout printed before the loop wedged. The test must not hang — that
// promptness is the property under test — so the tiny timeout is the whole point.
func TestEvalContextTimesOutOnNonTerminatingLoop(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	result, err := interpreter.EvalContext(
		context.Background(),
		50*time.Millisecond,
		"wedged",
		`fmt.Print("seen"); for {}`,
	)
	require.Error(t, err)

	var oopsErr oops.OopsError

	require.ErrorAs(t, err, &oopsErr)
	assert.Equal(t, "repl", oopsErr.Domain())
	assert.Equal(t, "eval_timed_out", oopsErr.Code())
	assert.Equal(t, "seen", result.Stdout, "partial stdout printed before the loop wedged is captured")
	assert.False(t, result.Retval.IsValid(), "a timed-out eval resolves no value")
}
