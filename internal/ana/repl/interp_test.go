package repl_test

import (
	"reflect"
	"testing"

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
	t.Logf("redeclare a persisted var with := across Eval → err=%v", redeclareErr)

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
