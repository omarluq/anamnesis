package repl

import "reflect"

// Result is the outcome of one Eval: the final expression's value plus whatever
// the evaluated source wrote to standard output and standard error during the
// turn. Stdout is the load-bearing field — the controller's context grows by
// what its code prints, not by the values it touches.
type Result struct {
	// Retval is the value of src's final expression, or the zero Value on error.
	Retval reflect.Value
	// Stdout is everything the turn's code printed to standard output.
	Stdout string
	// Stderr is everything the turn's code printed to standard error.
	Stderr string
}

// captureResult snapshots the per-turn output buffers alongside value into a
// Result. It reads the buffers by value, so the caller is free to reset them on
// the next Eval without disturbing an already-returned Result.
func (interpreter *Interpreter) captureResult(value reflect.Value) Result {
	return Result{
		Retval: value,
		Stdout: interpreter.stdout.String(),
		Stderr: interpreter.stderr.String(),
	}
}
