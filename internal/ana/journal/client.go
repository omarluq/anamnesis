package journal

import (
	"errors"
	"sync"

	"github.com/samber/oops"
)

// Reader is one reusable handle onto the systemd journal: the read-only subset of
// a libsystemd journal cursor the host surface needs. It narrows the active query
// with matches, walks the matching records in order, and decodes the current
// record's raw fields. A Client pools Readers and resets each one with
// FlushMatches before reuse, so a Reader must not be touched after it is released
// back to its Client. Implementations need not be safe for concurrent use; the
// pool hands each Reader to a single caller at a time.
type Reader interface {
	// AddMatch adds a "FIELD=value" exact-match constraint to the active query.
	// Matches on the same field OR together; matches on different fields AND.
	AddMatch(match string) error
	// SeekHead positions the read cursor before the first matching record so the
	// next Next call yields the oldest entry.
	SeekHead() error
	// SeekRealtime positions the read cursor by wall-clock time so the next Next call
	// yields the first matching record at or after usec microseconds since the Unix
	// epoch. A windowed query seeks here instead of SeekHead so it reads only its time
	// window rather than scanning the whole journal from the head.
	SeekRealtime(usec uint64) error
	// Next advances to the next matching record, returning the number of records
	// advanced — zero at the end of the journal — or an error.
	Next() (uint64, error)
	// Fields returns the current record's raw journald field map, cursor and
	// realtime timestamp included, ready for the package parser to decode into an
	// Entry.
	Fields() (map[string]any, error)
	// FlushMatches drops every match so a pooled Reader starts its next query
	// clean; the Client calls it when a Reader is released back to the pool.
	FlushMatches()
	// Close releases the Reader's underlying libsystemd handle.
	Close() error
}

// ReaderFactory opens fresh journal Readers for a Client's pool. The default
// factory wired by NewClient returns sdjournal-backed Readers; tests inject a
// factory that returns in-memory Readers so the pool lifecycle can be exercised
// without cgo.
type ReaderFactory interface {
	// NewReader opens a fresh Reader positioned at the head of the journal, or
	// returns an error when the underlying journal cannot be opened.
	NewReader() (Reader, error)
}

// Client reads systemd journal records through a bounded pool of reusable
// Readers. Acquire hands out a Reader — reusing an idle one or opening a fresh one
// through the ReaderFactory — and Release returns it for reuse after resetting its
// matches. A Client is safe for concurrent use by multiple goroutines and must not
// be copied after first use. It holds no closable handle of its own: the CLI runs
// one investigation and exits, so the OS reclaims each pooled Reader's libsystemd
// fd at process exit rather than through an explicit Client teardown.
type Client struct {
	factory ReaderFactory
	idle    []Reader
	mutex   sync.Mutex
	maxIdle int
}

// NewClientWithFactory builds a pooled journal Client that opens Readers through
// factory and keeps at most maxIdle idle Readers for reuse; maxIdle is clamped to
// a minimum of one. The default Client wiring calls this with an sdjournal-backed
// factory.
func NewClientWithFactory(factory ReaderFactory, maxIdle int) *Client {
	return &Client{
		factory: factory,
		idle:    nil,
		mutex:   sync.Mutex{},
		maxIdle: max(maxIdle, 1),
	}
}

// Acquire returns a Reader for the caller's exclusive use, reusing an idle Reader
// when one is available or opening a fresh one through the factory otherwise. The
// caller must hand it back with Release. The factory open runs with the pool lock
// released — opening a journal handle can block — since no idle Reader is then
// available to reuse.
func (client *Client) Acquire() (Reader, error) {
	client.mutex.Lock()

	last := len(client.idle) - 1
	if last < 0 {
		client.mutex.Unlock()

		return client.factory.NewReader()
	}

	reader := client.idle[last]
	client.idle[last] = nil
	client.idle = client.idle[:last]

	client.mutex.Unlock()

	return reader, nil
}

// Release returns a Reader to the pool for reuse after flushing its matches so the
// next caller starts clean. When the idle pool is already full, Release closes the
// Reader instead of pooling it. A nil Reader is ignored.
func (client *Client) Release(reader Reader) error {
	if reader == nil {
		return nil
	}

	reader.FlushMatches()

	client.mutex.Lock()

	if len(client.idle) < client.maxIdle {
		client.idle = append(client.idle, reader)
		client.mutex.Unlock()

		return nil
	}

	client.mutex.Unlock()

	return reader.Close()
}

// withReader borrows a pooled Reader for operation, runs work against it and
// releases the Reader before returning, joining any release error with work's own.
// It centralizes the acquire-wrap-defer-release lifecycle every host-surface call
// shares; the named result lets the deferred Release fold its error into err.
func withReader[T any](client *Client, operation string, work func(reader Reader) (T, error)) (result T, err error) {
	reader, acquireErr := client.Acquire()
	if acquireErr != nil {
		return result, oops.In("journal").Code("acquire").Wrapf(acquireErr, "acquire reader for %s", operation)
	}

	defer func() {
		err = errors.Join(err, client.Release(reader))
	}()

	return work(reader)
}
