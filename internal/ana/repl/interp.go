// Package repl embeds the mvm Go interpreter so the RLM controller can execute
// the Go source it generates on each turn. The interpreter retains its variable
// state across Eval calls, so values bound in one turn stay visible to the next,
// and each Eval captures whatever its source printed to stdout and stderr.
package repl

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/mvm-sh/mvm/interp"
	"github.com/mvm-sh/mvm/lang/golang"
	"github.com/mvm-sh/mvm/stdlib"
	_ "github.com/mvm-sh/mvm/stdlib/all" // populates stdlib.Values with Go's standard library bindings.
	"github.com/samber/oops"
)

// CodeEvalTimedOut is the oops code EvalContext stamps on the error it returns when an
// eval does not finish within its effective timeout. The rlm controller matches on this
// shared sentinel rather than the literal string, so the eval-timeout contract has a
// single source of truth across the repl boundary.
const CodeEvalTimedOut = "eval_timed_out"

// Interpreter wraps an mvm interpreter preloaded with the Go standard library.
// Each Eval runs a named source fragment against the shared session state and
// captures its output into per-turn buffers. The zero value is not usable;
// construct one with NewInterpreter.
//
// The interpreter owns the agent and query primitive bindings RegisterAgent and
// RegisterQuery install, rather than a package-global map keyed by *Interpreter:
// the per-session state lives and dies with the interpreter, so a finished
// session is reclaimed by the garbage collector with no binding entry to leak and
// no teardown to remember.
//
// An Interpreter is not safe for concurrent use: Eval mutates the shared engine
// and resets the per-turn output buffers, so a single caller must serialize its
// Eval calls, as the RLM controller does by evaluating one turn at a time. The mvm
// interpreter has no preemption of its own, so EvalContext runs each eval off the
// caller's goroutine under a timeout; a timed-out eval is abandoned and leaks until
// the process exits, poisoning this interpreter — see EvalContext.
type Interpreter struct {
	// engine is the mvm interpreter holding the persistent session state.
	engine *interp.Interp
	// agent is the §7 terminal primitive façade RegisterAgent bound, or nil when
	// none has been registered on this interpreter yet.
	agent *Agent
	// queryRunner backs the §6 bounded sub-call primitives RegisterQuery bound, or
	// nil when none has been registered on this interpreter yet.
	queryRunner *queryRunner
	// stdout captures what the current turn's source printed to standard output,
	// behind a mutex so EvalContext can snapshot partial output race-free while an
	// abandoned, timed-out eval goroutine may still be writing.
	stdout lockedBuffer
	// stderr captures what the current turn's source printed to standard error,
	// isolating it from the host process; it is not surfaced in Result.
	stderr lockedBuffer
}

// evalOutcome carries one engine.Eval result off the detached eval goroutine onto
// the buffered done channel: the final value with a nil err, or the zero value with
// the repl-tagged fault EvalContext surfaces.
type evalOutcome struct {
	// err is the wrapped evaluation fault, or nil when the eval succeeded.
	err error
	// value is the final expression's value, or the zero Value on error.
	value reflect.Value
}

// NewInterpreter returns an Interpreter whose mvm engine has the Go standard
// library imported and its packages auto-resolved, ready to evaluate controller
// source. Its SetIO is wired to the per-turn buffers so interpreted fmt.Print
// output is captured rather than written to the host process. State established
// by one Eval persists into later Eval calls.
func NewInterpreter() *Interpreter {
	engine := interp.NewInterpreter(golang.GoSpec)
	engine.ImportPackageValues(stdlib.Values)
	engine.AutoImportPackages()

	interpreter := &Interpreter{
		engine:      engine,
		agent:       nil,
		queryRunner: nil,
		stdout:      lockedBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}},
		stderr:      lockedBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}},
	}
	engine.SetIO(os.Stdin, &interpreter.stdout, &interpreter.stderr)

	return interpreter
}

// Eval compiles and runs src under the label name against the persistent session
// state, returning a Result that carries the value of the final expression and
// everything the source printed this turn. The output buffers are reset before
// each run so no output leaks across turns. A compile or runtime fault is wrapped
// as an oops error tagged with the repl domain; the Result still carries any
// output produced before the fault. Eval runs synchronously and cannot be
// interrupted; the controller drives EvalContext instead so a non-terminating turn
// is bounded by a timeout.
func (interpreter *Interpreter) Eval(name, src string) (Result, error) {
	interpreter.stdout.Reset()
	interpreter.stderr.Reset()

	value, err := interpreter.engine.Eval(name, src)
	if err != nil {
		wrapped := oops.In("repl").Code("eval_failed").Wrapf(err, "evaluate %q", name)

		return interpreter.captureResult(reflect.Value{}), wrapped
	}

	return interpreter.captureResult(value), nil
}

// EvalContext runs src under the label name against the persistent session state,
// bounded by timeout and by any deadline on ctx, whichever is sooner. The mvm
// interpreter has no preemption, so a model-emitted non-terminating loop would wedge
// a synchronous Eval forever; EvalContext therefore runs the eval on its own
// goroutine and selects on the result, the timeout, and ctx. On completion it
// returns the Result and fault exactly as Eval would.
//
// On timeout or ctx cancellation it snapshots the best-effort partial stdout the
// turn printed before it wedged and returns an eval_timed_out error. Go cannot kill
// a goroutine, so the abandoned eval leaks until the process exits and this
// interpreter is now POISONED: its engine state and output buffers may still be
// mutated by that goroutine, so the caller MUST end the session and never Eval
// against this interpreter again.
func (interpreter *Interpreter) EvalContext(
	ctx context.Context,
	timeout time.Duration,
	name, src string,
) (Result, error) {
	interpreter.stdout.Reset()
	interpreter.stderr.Reset()

	// If the shared deadline has already elapsed or ctx is already canceled, there is
	// no budget to run this turn: spawning evalAsync would only leak a goroutine the
	// select abandons on its first tick. Return the timeout result without starting it.
	budget := effectiveTimeout(ctx, timeout)
	if ctx.Err() != nil || budget <= 0 {
		return interpreter.timedOut(name, timeout)
	}

	// Buffered (cap 1) so the abandoned eval goroutine can always deliver its
	// outcome and exit, even after a timeout has made this select stop listening.
	done := make(chan evalOutcome, 1)
	go interpreter.evalAsync(name, src, done)

	timer := time.NewTimer(budget)
	defer timer.Stop()

	select {
	case outcome := <-done:
		return interpreter.captureResult(outcome.value), outcome.err
	case <-timer.C:
		return interpreter.timedOut(name, timeout)
	case <-ctx.Done():
		return interpreter.timedOut(name, timeout)
	}
}

// evalAsync runs engine.Eval and delivers its outcome on done. It recovers any
// panic the interpreter lets escape — converting it into an eval_panicked error
// rather than letting it crash the process from this detached goroutine — and
// wraps an ordinary fault as eval_failed, exactly as Eval does. done is buffered so
// this send never blocks even after EvalContext has abandoned the goroutine.
func (interpreter *Interpreter) evalAsync(name, src string, done chan<- evalOutcome) {
	defer func() {
		if recovered := recover(); recovered != nil {
			done <- evalOutcome{
				value: reflect.Value{},
				err:   oops.In("repl").Code("eval_panicked").Errorf("eval %q panicked: %v", name, recovered),
			}
		}
	}()

	value, err := interpreter.engine.Eval(name, src)
	done <- newEvalOutcome(name, value, err)
}

// newEvalOutcome packages an engine.Eval return, wrapping a non-nil fault as the
// repl-tagged eval_failed error EvalContext surfaces and dropping the value, so the
// timeout select branch and this success branch produce identically shaped results.
func newEvalOutcome(name string, value reflect.Value, err error) evalOutcome {
	if err == nil {
		return evalOutcome{value: value, err: nil}
	}

	return evalOutcome{
		value: reflect.Value{},
		err:   oops.In("repl").Code("eval_failed").Wrapf(err, "evaluate %q", name),
	}
}

// timedOut builds the Result EvalContext returns when an eval does not finish in
// time: the best-effort partial stdout snapshotted from the locked buffer — race-free
// even though the abandoned goroutine may still be writing — and an eval_timed_out
// error naming the breached timeout. The interpreter is poisoned afterwards, so the
// caller must end the session rather than reuse it.
func (interpreter *Interpreter) timedOut(name string, timeout time.Duration) (Result, error) {
	partial := interpreter.stdout.String()

	err := oops.
		In("repl").
		Code(CodeEvalTimedOut).
		Errorf(
			"eval %q exceeded %s; the generated code did not return (possible non-terminating loop)",
			name,
			timeout,
		)

	return Result{Retval: reflect.Value{}, Stdout: partial}, err
}

// effectiveTimeout is the smaller of the caller's per-eval timeout and the time
// left on ctx's deadline, so a near-exhausted wall-clock deadline still bounds the
// eval even when the per-eval timeout is generous. With no deadline on ctx it is the
// per-eval timeout unchanged.
func effectiveTimeout(ctx context.Context, timeout time.Duration) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return timeout
	}

	return min(timeout, time.Until(deadline))
}
