// Package repl embeds the mvm Go interpreter so the RLM controller can execute
// the Go source it generates on each turn. The interpreter retains its variable
// state across Eval calls, so values bound in one turn stay visible to the next,
// and each Eval captures whatever its source printed to stdout and stderr.
package repl

import (
	"bytes"
	"os"
	"reflect"

	"github.com/mvm-sh/mvm/interp"
	"github.com/mvm-sh/mvm/lang/golang"
	"github.com/mvm-sh/mvm/stdlib"
	_ "github.com/mvm-sh/mvm/stdlib/all" // populates stdlib.Values with Go's standard library bindings.
	"github.com/samber/oops"
)

// Interpreter wraps an mvm interpreter preloaded with the Go standard library.
// Each Eval runs a named source fragment against the shared session state and
// captures its output into per-turn buffers. The zero value is not usable;
// construct one with NewInterpreter.
//
// An Interpreter is not safe for concurrent use: Eval mutates the shared engine
// and resets the per-turn output buffers, so a single caller must serialize its
// Eval calls, as the RLM controller does by evaluating one turn at a time.
type Interpreter struct {
	engine *interp.Interp
	stdout bytes.Buffer
	stderr bytes.Buffer
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
		engine: engine,
		stdout: bytes.Buffer{},
		stderr: bytes.Buffer{},
	}
	engine.SetIO(os.Stdin, &interpreter.stdout, &interpreter.stderr)

	return interpreter
}

// Eval compiles and runs src under the label name against the persistent session
// state, returning a Result that carries the value of the final expression and
// everything the source printed this turn. The output buffers are reset before
// each run so no output leaks across turns. A compile or runtime fault is wrapped
// as an oops error tagged with the repl domain; the Result still carries any
// output produced before the fault.
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
