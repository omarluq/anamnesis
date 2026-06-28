package repl_test

import (
	"context"
	"reflect"
	"sync/atomic"
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

// TestEvalContextDoesNotKillSlowButProgressingEval proves the per-eval bound is an
// IDLE window, not a total budget: an eval that runs far longer than the window but
// keeps printing — each print is tree-wide progress — resets the watchdog's idle
// deadline and runs to completion. A fixed-budget timeout would have force-finished it
// at the window; the progress-aware watchdog does not. This is the real fix for a wide,
// slow-but-advancing fan-out being discarded as "incomplete".
func TestEvalContextDoesNotKillSlowButProgressingEval(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	// Total runtime ~200ms exceeds the 100ms idle window, but each 20ms gap between
	// prints stays well under it, so the eval is never idle long enough to trip.
	result, err := interpreter.EvalContext(
		context.Background(),
		100*time.Millisecond,
		"progressing",
		"for i := 0; i < 10; i++ {\n"+
			"\tfmt.Println(i)\n"+
			"\ttime.Sleep(20 * time.Millisecond)\n"+
			"}\n"+
			"42",
	)
	require.NoError(t, err, "a slow but steadily-progressing eval must complete, not be force-finished")
	require.True(t, result.Retval.IsValid())
	assert.Equal(t, int64(42), result.Retval.Int())
	assert.Contains(t, result.Stdout, "0", "the first iteration printed")
	assert.Contains(t, result.Stdout, "9", "the last iteration printed, so the eval ran to the end")
}

// TestEvalContextIdleWatchdogReadsSharedProgress proves progress is observed tree-wide,
// not just from this turn's stdout: a child loop's work lives on a SHARED counter the
// parent never prints to. With that counter installed and advanced from outside, an
// eval that prints nothing and only sleeps is still kept alive, because the shared
// progress keeps the idle deadline moving. This is the seam that stops a parent's
// watchdog from killing a busy-but-silent descending child loop.
func TestEvalContextIdleWatchdogReadsSharedProgress(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	progress := new(atomic.Int64)
	interpreter.SetProgress(progress)

	// Stand in for a busy child loop: bump the shared counter every 15ms, comfortably
	// inside the 60ms window, while the eval below sleeps silently for 200ms.
	stop := make(chan struct{})
	defer close(stop)

	go func() {
		ticker := time.NewTicker(15 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				progress.Add(1)
			}
		}
	}()

	result, err := interpreter.EvalContext(
		context.Background(),
		60*time.Millisecond,
		"silent-but-tree-busy",
		"time.Sleep(200 * time.Millisecond)\n7",
	)
	require.NoError(t, err, "tree-wide progress on the shared counter must keep a silent eval alive")
	require.True(t, result.Retval.IsValid())
	assert.Equal(t, int64(7), result.Retval.Int())
}

// TestEvalContextForceFinishesIdleEval is the contrasting half of the watchdog pair: an
// eval that makes no progress at all for the whole window — no stdout, no sub-call, no
// shared bump, just a blocking sleep longer than the window — is force-finished exactly
// as a bare for{} wedge is. A turn that only sleeps is indistinguishable from a wedge
// and is meant to be capped; in production a real turn's sub-calls bump progress.
func TestEvalContextForceFinishesIdleEval(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	_, err := interpreter.EvalContext(
		context.Background(),
		40*time.Millisecond,
		"idle-sleep",
		"time.Sleep(400 * time.Millisecond)\n1",
	)
	require.Error(t, err)

	var oopsErr oops.OopsError

	require.ErrorAs(t, err, &oopsErr)
	assert.Equal(t, "repl", oopsErr.Domain())
	assert.Equal(t, "eval_timed_out", oopsErr.Code(), "a zero-progress eval trips the idle watchdog")
}

// TestEvalContextPreCanceledCtxIsNotTimeout proves a ctx already canceled before the
// eval starts surfaces as a cancellation, NOT an eval_timed_out: a user-quit must not
// be misrecorded as a wedge. The error wraps context.Canceled and carries a code other
// than eval_timed_out, so the controller's evalTimedOut check reads false and the loop
// classifies the stop as ctx-canceled.
func TestEvalContextPreCanceledCtxIsNotTimeout(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := interpreter.EvalContext(ctx, time.Second, "pre-canceled", "1")
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled, "the cancellation cause is preserved")

	var oopsErr oops.OopsError

	require.ErrorAs(t, err, &oopsErr)
	assert.NotEqual(t, "eval_timed_out", oopsErr.Code(),
		"a ctx-cancel must not masquerade as an idle timeout")
}

// TestEvalContextMidEvalCancelIsNotTimeout proves a cancel that arrives WHILE a turn is
// running (a generous idle window, so the watchdog itself would not fire) ends the eval
// as a cancellation rather than an eval_timed_out, so a user-quit mid-turn is classified
// as ctx-canceled, not a wedge.
func TestEvalContextMidEvalCancelIsNotTimeout(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	// The 10s idle window cannot trip in the ~20ms before the cancel lands, so the only
	// thing that ends this eval is the cancellation.
	_, err := interpreter.EvalContext(ctx, 10*time.Second, "mid-cancel", "time.Sleep(2 * time.Second)\n1")
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled, "the cancellation cause is preserved")

	var oopsErr oops.OopsError

	require.ErrorAs(t, err, &oopsErr)
	assert.NotEqual(t, "eval_timed_out", oopsErr.Code(),
		"a mid-eval ctx-cancel must not masquerade as an idle timeout")
}
