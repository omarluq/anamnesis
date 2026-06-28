package repl

import (
	"bytes"
	"reflect"
	"sync"
)

// Result is the outcome of one Eval: the final expression's value plus whatever
// the evaluated source wrote to standard output during the turn. Stdout is the
// load-bearing field — the controller's context grows by what its code prints, not
// by the values it touches.
type Result struct {
	// Retval is the value of src's final expression, or the zero Value on error.
	Retval reflect.Value
	// Stdout is everything the turn's code printed to standard output.
	Stdout string
}

// captureResult snapshots the per-turn stdout buffer alongside value into a Result.
// It reads the buffer under its lock, so the snapshot is race-free even when a
// timed-out, abandoned eval goroutine is still writing, and the caller is free to
// reset the buffer on the next Eval without disturbing an already-returned Result.
func (interpreter *Interpreter) captureResult(value reflect.Value) Result {
	return Result{
		Retval: value,
		Stdout: interpreter.stdout.String(),
	}
}

// lockedBuffer is a bytes.Buffer guarded by a mutex so a mid-eval snapshot of
// captured stdout is race-free: EvalContext may read String() to recover partial
// output while a timed-out, abandoned eval goroutine is still writing into the same
// buffer. It mirrors librecode's synchronizedBuffer (internal/tool/bash.go). The
// zero value is ready to use; it must not be copied after first use because it
// holds a sync.Mutex, which is why an Interpreter is only ever used by pointer.
type lockedBuffer struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

// Write appends data to the buffer under the lock, satisfying io.Writer so the mvm
// engine's SetIO can target it for the interpreted source's stdout.
func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(data)
}

// String returns the buffer's accumulated contents under the lock, a race-free
// snapshot even while another goroutine is mid-Write.
func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// Len returns the buffer's accumulated byte count under the lock, a race-free read the
// EvalContext idle watchdog samples as a progress signal without copying the contents.
func (b *lockedBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Len()
}

// Reset clears the buffer under the lock so the next turn starts with empty output.
func (b *lockedBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.buf.Reset()
}
