package terminal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRLMControllerLeakedEmitAfterTeardownDoesNotPanic proves the adapter never
// closes the channel an investigation goroutine sends on, so a fan-out sub-call
// goroutine that an abandoned, un-preemptable mvm eval leaked can keep emitting
// long after the run returned without panicking on send-to-closed-channel. The
// Investigator captures the channel it was handed and, after the run has fully
// torn down (out closed), a leaked goroutine emits on that very channel, guarded
// exactly as the real emitter guards its send — abandoning it once the run
// context is canceled, modeled here by emitDone. With the pre-fix defer close(out)
// this send raced a closed channel and panicked the process; the forwarding pump
// routes it at the never-closed inner channel where the guard makes it a no-op.
func TestRLMControllerLeakedEmitAfterTeardownDoesNotPanic(t *testing.T) {
	t.Parallel()

	const runID = uint64(1)

	// captured carries the events channel the Investigator was handed back to the
	// test, so it can drive a post-teardown emit on the very channel the run used.
	captured := make(chan chan<- TraceEvent, 1)

	investigate := func(_ context.Context, _, _ string, events chan<- TraceEvent, _ uint64) (string, error) {
		captured <- events

		events <- TraceEvent{
			Kind:    TraceKindFinal,
			Text:    "root cause: oom-kill",
			Err:     "",
			Depth:   0,
			RunID:   runID,
			QueryID: 0,
		}

		return "root cause: oom-kill", nil
	}

	out := NewRLMController(investigate).Start(context.Background(), "why did boot 3 oom", "", runID)

	received := make([]TraceEvent, 0, 1)
	for event := range out {
		received = append(received, event)
	}

	require.Len(t, received, 1, "the pump forwards the run's events before out closes")
	assert.Equal(t, TraceKindFinal, received[0].Kind)

	// out is closed, so the run and pump goroutines have both returned. A leaked
	// fan-out goroutine now emits on the captured channel; with emitDone closed it
	// models the emitter's guard after the run context is canceled at teardown.
	leakedEvents := <-captured
	emitDone := make(chan struct{})

	close(emitDone)

	settled := make(chan struct{})

	go func() {
		defer close(settled)

		for idx := range 8 {
			event := TraceEvent{
				Kind:    TraceKindQueryStart,
				Text:    "late fan-out emit",
				Err:     "",
				Depth:   1,
				RunID:   runID,
				QueryID: uint64(idx + 1),
			}

			select {
			case leakedEvents <- event:
			case <-emitDone:
			}
		}
	}()

	select {
	case <-settled:
	case <-time.After(time.Second):
		t.Fatal("leaked emit blocked after teardown; the adapter changed the channel contract")
	}
}

// TestRLMControllerEmitsFailureFinalThenCloses proves a failed run still surfaces
// one final event carrying the cause and then closes out, so the pump both
// forwards the failure note and closes the shell-facing channel on run completion
// even though no investigation ever emitted. This is the spinner backstop the fix
// relies on: dropping the channel close would leave a failed run's composer wedged.
func TestRLMControllerEmitsFailureFinalThenCloses(t *testing.T) {
	t.Parallel()

	const runID = uint64(5)

	wantErr := errors.New("assemble session: missing collaborator")
	investigate := func(_ context.Context, _, _ string, _ chan<- TraceEvent, _ uint64) (string, error) {
		return "", wantErr
	}

	out := NewRLMController(investigate).Start(context.Background(), "why did boot 3 oom", "", runID)

	received := make([]TraceEvent, 0, 1)
	for event := range out {
		received = append(received, event)
	}

	require.Len(t, received, 1, "a failed run posts one final event before out closes")
	assert.Equal(t, TraceKindFinal, received[0].Kind)
	assert.Contains(t, received[0].Text, "assemble session: missing collaborator")
	assert.Equal(t, runID, received[0].RunID)
}
