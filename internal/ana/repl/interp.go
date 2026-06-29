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
	"sync/atomic"
	"time"

	"github.com/mvm-sh/mvm/interp"
	"github.com/mvm-sh/mvm/lang/golang"
	_ "github.com/mvm-sh/mvm/stdlib/all" // populates stdlib.Values, which allowedStdlibValues filters.
	"github.com/samber/lo"
	"github.com/samber/oops"
)

// CodeEvalTimedOut is the oops code EvalContext stamps on the error it returns when an
// eval does not finish within its effective timeout. The rlm controller matches on this
// shared sentinel rather than the literal string, so the eval-timeout contract has a
// single source of truth across the repl boundary.
const CodeEvalTimedOut = "eval_timed_out"

// Interpreter wraps an mvm interpreter preloaded with the allow-listed subset of
// the Go standard library (see allowlist.go; host-effect packages such as os and
// net are withheld so interpreted source stays sandboxed).
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
// caller's goroutine under an idle-progress watchdog; a watchdog-tripped eval is
// abandoned and leaks until the process exits, poisoning this interpreter — see
// EvalContext.
type Interpreter struct {
	// engine is the mvm interpreter holding the persistent session state.
	engine *interp.Interp
	// agent is the §7 terminal primitive façade RegisterAgent bound, or nil when
	// none has been registered on this interpreter yet.
	agent *Agent
	// queryRunner backs the §6 bounded sub-call primitives RegisterQuery bound, or
	// nil when none has been registered on this interpreter yet.
	queryRunner *queryRunner
	// progress is the tree-wide work counter the EvalContext idle watchdog reads to
	// tell a busy eval from a wedged one. NewInterpreter seeds a private counter;
	// SetProgress swaps in a shared one so a child loop's work keeps the parent's
	// watchdog from firing. Every sub-call and turn-start bumps it; stdout growth
	// counts too. It is never nil after construction.
	progress *atomic.Int64
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

// NewInterpreter returns an Interpreter whose mvm engine has the allow-listed
// subset of the Go standard library imported and its packages auto-resolved,
// ready to evaluate controller source. It imports only safeStdlibPackages rather
// than the whole stdlib.Values bridge, so interpreted source can reach fmt and
// its peers but never os/os-exec/syscall/net and the other host-effect packages
// whose bridge bindings would otherwise grant the controller's raw Go arbitrary
// host access — see allowlist.go for the security rationale. Its SetIO is wired
// to the per-turn buffers so interpreted fmt.Print output is captured rather than
// written to the host process. State established by one Eval persists into later
// Eval calls.
func NewInterpreter() *Interpreter {
	engine := interp.NewInterpreter(golang.GoSpec)
	engine.ImportPackageValues(allowedStdlibValues())
	engine.AutoImportPackages()

	interpreter := &Interpreter{
		engine:      engine,
		agent:       nil,
		queryRunner: nil,
		progress:    new(atomic.Int64),
		stdout:      lockedBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}},
		stderr:      lockedBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}},
	}
	engine.SetIO(os.Stdin, &interpreter.stdout, &interpreter.stderr)

	return interpreter
}

// SetProgress shares one tree-wide progress counter across every interpreter in an
// investigation, so a child controller loop's work registers as progress on the
// parent's EvalContext idle watchdog even though the child prints to its own stdout
// rather than the parent's. The rlm recursor mints one counter per run and calls this
// on every interpreter it builds. Call it before the first eval — it is not safe to
// swap the counter concurrently with a running watchdog.
func (interpreter *Interpreter) SetProgress(progress *atomic.Int64) {
	interpreter.progress = progress
}

// Eval compiles and runs src under the label name against the persistent session
// state, returning a Result that carries the value of the final expression and
// everything the source printed this turn. The output buffers are reset before
// each run so no output leaks across turns. A compile or runtime fault is wrapped
// as an oops error tagged with the repl domain; the Result still carries any
// output produced before the fault.
//
// Eval runs synchronously and cannot be interrupted, so it is the plain test/utility
// entry point, not the production loop's path: the controller drives EvalContext
// instead, which runs the eval off-goroutine under an idle-progress watchdog so a
// non-terminating turn cannot wedge the session. Eval and EvalContext share the same
// engine and capture, so a test that does not need the watchdog can use this simpler
// call.
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
// guarded by an idle-progress watchdog whose window is timeout, and by ctx. The mvm
// interpreter has no preemption, so a model-emitted non-terminating loop would wedge
// a synchronous Eval forever; EvalContext therefore runs the eval on its own
// goroutine and watches it. On completion it returns the Result and fault exactly as
// Eval would.
//
// timeout is an IDLE window, not a total budget: a slow-but-advancing turn — a wide
// fan-out whose sub-calls and child loops are all making progress — runs as long as it
// keeps advancing, while only a turn that makes NO progress for the whole window is
// force-finished. Progress is observed tree-wide: every sub-call, every turn-start
// across the investigation, and any growth of this turn's stdout resets the idle
// window (see SetProgress and progressMark). A true non-terminating loop does zero
// I/O and no sub-calls, so the window elapses and the watchdog trips.
//
// On a tripped watchdog it snapshots the best-effort partial stdout the turn printed
// before it wedged and returns an eval_timed_out error. A ctx cancellation instead
// returns a cancellation error (NOT eval_timed_out), so the controller tells a
// user-cancel apart from a genuine wedge and classifies the stop as ctx-canceled rather
// than an eval timeout. Either way Go cannot kill a goroutine, so an already-started
// eval leaks until the process exits and this interpreter is now POISONED: its engine
// state and output buffers may still be mutated by that goroutine, so the caller MUST
// end the session and never Eval against this interpreter again.
func (interpreter *Interpreter) EvalContext(
	ctx context.Context,
	timeout time.Duration,
	name, src string,
) (Result, error) {
	interpreter.stdout.Reset()
	interpreter.stderr.Reset()

	// A turn beginning is itself tree-wide progress: bump before launching so a
	// sibling or parent watchdog sees this node advance even on a turn that prints
	// nothing and makes no sub-call.
	interpreter.progress.Add(1)

	// Nothing to run if ctx is already canceled or the window is non-positive: spawning
	// evalAsync would only leak a goroutine the watchdog abandons on its first tick. An
	// already-canceled ctx is a cancellation, not an idle timeout, so report it as such;
	// a non-positive window is the degenerate idle case and force-finishes as a timeout.
	if ctx.Err() != nil {
		return interpreter.canceled(ctx, name)
	}

	if timeout <= 0 {
		return interpreter.timedOut(name, timeout)
	}

	// Buffered (cap 1) so the abandoned eval goroutine can always deliver its
	// outcome and exit, even after the watchdog has stopped listening.
	done := make(chan evalOutcome, 1)
	go interpreter.evalAsync(name, src, done)

	return interpreter.watchProgress(ctx, timeout, name, done)
}

// watchProgress drives the idle-progress watchdog for one running eval: it resets an
// idle deadline whenever tree-wide progress is observed and force-finishes only after
// the eval has made no progress for the whole window. The ticker bounds how quickly an
// idle eval is detected; a busy eval — sub-calls firing, stdout growing, child loops
// taking turns — keeps moving the deadline forward and is never killed. A canceled ctx
// (the user quit, or the run tearing down on return) ends it at once with a cancellation
// fault, not an idle timeout, so the stop is classified as a ctx-cancel.
func (interpreter *Interpreter) watchProgress(
	ctx context.Context,
	window time.Duration,
	name string,
	done <-chan evalOutcome,
) (Result, error) {
	ticker := time.NewTicker(watchdogInterval(window))
	defer ticker.Stop()

	last := interpreter.progressMark()
	deadline := time.Now().Add(window)

	for {
		select {
		case outcome := <-done:
			return interpreter.captureResult(outcome.value), outcome.err
		case <-ctx.Done():
			return interpreter.canceled(ctx, name)
		case now := <-ticker.C:
			if mark := interpreter.progressMark(); mark != last {
				last = mark
				deadline = now.Add(window)

				continue
			}

			if !now.Before(deadline) {
				return interpreter.timedOut(name, window)
			}
		}
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

// timedOut builds the Result EvalContext returns when the idle watchdog force-finishes
// an eval: the best-effort partial stdout snapshotted from the locked buffer — race-free
// even though the abandoned goroutine may still be writing — and an eval_timed_out
// error naming the idle window it overran. The interpreter is poisoned afterwards, so
// the caller must end the session rather than reuse it.
func (interpreter *Interpreter) timedOut(name string, window time.Duration) (Result, error) {
	partial := interpreter.stdout.String()

	err := oops.
		In("repl").
		Code(CodeEvalTimedOut).
		Errorf(
			"eval %q made no progress within its %s idle window (possible non-terminating loop)",
			name,
			window,
		)

	return Result{Retval: reflect.Value{}, Stdout: partial}, err
}

// canceled builds the Result EvalContext returns when ctx is canceled (the user quit,
// or the run tearing down on return) rather than the idle watchdog tripping. It carries
// the best-effort partial stdout and the context's cancellation wrapped under a code
// that is deliberately NOT eval_timed_out, so evalTimedOut reports false and the
// controller's loop classifies the stop as a ctx-cancel — not a wedge — instead of
// blaming a non-terminating loop. If the eval had already started, its goroutine is
// abandoned and this interpreter is poisoned, so the caller must still end the session.
func (interpreter *Interpreter) canceled(ctx context.Context, name string) (Result, error) {
	partial := interpreter.stdout.String()

	err := oops.
		In("repl").
		Code("eval_canceled").
		Wrapf(ctx.Err(), "eval %q canceled before it returned", name)

	return Result{Retval: reflect.Value{}, Stdout: partial}, err
}

// Idle-watchdog sampling bounds. The watchdog polls every window/divisor, clamped so a
// tiny window (the tests') still polls promptly and a generous one (the 10-minute
// production default) does not spin.
const (
	watchdogIntervalDivisor = 8
	watchdogMinInterval     = 5 * time.Millisecond
	watchdogMaxInterval     = time.Second
)

// progressMark is the monotonic tree-wide progress signal the idle watchdog samples:
// the shared progress counter plus this turn's accumulated stdout length. The counter
// advances on every sub-call and turn-start across the investigation, and stdout only
// grows within a turn (the buffer is reset at eval start), so the mark never decreases
// while an eval runs — a change between samples means real work happened.
func (interpreter *Interpreter) progressMark() int64 {
	return interpreter.progress.Load() + int64(interpreter.stdout.Len())
}

// recordProgress advances the tree-wide work counter the idle watchdog samples, so a
// host read that returns without printing — a windowed journal.Query can scan a whole
// boot through cgo and print nothing — registers as progress rather than reading as a
// wedge. It reads interpreter.progress at call time, so a read issued after SetProgress
// shared the tree counter bumps that shared counter, not the pre-share local one.
func (interpreter *Interpreter) recordProgress() {
	interpreter.progress.Add(1)
}

// watchdogInterval picks how often the idle watchdog samples progress for a given
// window: a fraction of the window so idleness is caught within roughly one window,
// clamped so a tiny window still polls sanely and a generous one does not spin.
func watchdogInterval(window time.Duration) time.Duration {
	return lo.Clamp(window/watchdogIntervalDivisor, watchdogMinInterval, watchdogMaxInterval)
}
