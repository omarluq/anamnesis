package repl

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestLockedBufferConcurrentWriteAndRead drives many goroutines writing into a
// lockedBuffer while the test reads String() concurrently, proving the mutex makes a
// mid-write snapshot race-free under `go test -race` — the guarantee EvalContext
// leans on to recover partial stdout from a still-writing, abandoned eval goroutine.
// Unlike a wedged eval the writers each write a fixed number of times and are joined,
// so this test terminates and the final length is deterministic.
func TestLockedBufferConcurrentWriteAndRead(t *testing.T) {
	t.Parallel()

	var buffer lockedBuffer

	const (
		writers         = 8
		writesPerWriter = 64
		payload         = "x"
	)

	var waitGroup sync.WaitGroup

	waitGroup.Add(writers)

	for range writers {
		go func() {
			defer waitGroup.Done()

			for range writesPerWriter {
				if _, err := buffer.Write([]byte(payload)); err != nil {
					t.Errorf("lockedBuffer.Write: %v", err)
				}
			}
		}()
	}

	// Read concurrently with the in-flight writes: the lock must serialize each
	// snapshot against the writers so the race detector sees no data race.
	for range writers * writesPerWriter {
		_ = buffer.String()
	}

	waitGroup.Wait()

	assert.Len(t, buffer.String(), writers*writesPerWriter*len(payload))
}
