package journal

import (
	"errors"
	"sync"

	"github.com/samber/lo"
	"github.com/samber/oops"
)

// errClientClosed is returned by Acquire once the Client has been closed; no
// further Readers can be handed out after Close has run.
var errClientClosed = oops.In("journal").Code("client_closed").Errorf("journal client is closed")

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
	// AddDisjunction inserts an OR boundary so the next match group is ORed with
	// the matches added so far, expressing a disjunction across fields.
	AddDisjunction() error
	// SeekHead positions the read cursor before the first matching record so the
	// next Next call yields the oldest entry.
	SeekHead() error
	// Next advances to the next matching record, returning the number of records
	// advanced — zero at the end of the journal — or an error.
	Next() (uint64, error)
	// Fields returns the current record's raw journald field map, cursor and
	// realtime timestamp included, ready to decode with DecodeFields.
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
// matches. Close releases every pooled Reader. A Client is safe for concurrent use
// by multiple goroutines and must not be copied after first use.
type Client struct {
	factory ReaderFactory
	idle    []Reader
	mutex   sync.Mutex
	maxIdle int
	closed  bool
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
		closed:  false,
	}
}

// Acquire returns a Reader for the caller's exclusive use, reusing an idle Reader
// when one is available or opening a fresh one through the factory otherwise. The
// caller must hand it back with Release. Acquire reports errClientClosed once the
// Client has been closed.
func (client *Client) Acquire() (Reader, error) {
	client.mutex.Lock()

	if client.closed {
		client.mutex.Unlock()

		return nil, errClientClosed
	}

	last := len(client.idle) - 1
	if last < 0 {
		client.mutex.Unlock()

		return client.acquireFresh()
	}

	reader := client.idle[last]
	client.idle[last] = nil
	client.idle = client.idle[:last]

	client.mutex.Unlock()

	return reader, nil
}

// Release returns a Reader to the pool for reuse after flushing its matches so the
// next caller starts clean. When the Client is closed or the idle pool is already
// full, Release closes the Reader instead of pooling it. A nil Reader is ignored.
func (client *Client) Release(reader Reader) error {
	if reader == nil {
		return nil
	}

	reader.FlushMatches()

	client.mutex.Lock()

	if !client.closed && len(client.idle) < client.maxIdle {
		client.idle = append(client.idle, reader)
		client.mutex.Unlock()

		return nil
	}

	client.mutex.Unlock()

	return reader.Close()
}

// Close marks the Client closed and releases every idle Reader, returning the
// joined error of all Reader Close calls. Close is idempotent: calling it again
// after the first close is a no-op that returns nil. After Close, Acquire reports
// errClientClosed.
func (client *Client) Close() error {
	client.mutex.Lock()

	if client.closed {
		client.mutex.Unlock()

		return nil
	}

	client.closed = true
	readers := client.idle
	client.idle = nil

	client.mutex.Unlock()

	errs := lo.Map(readers, func(reader Reader, _ int) error {
		return reader.Close()
	})

	return errors.Join(errs...)
}

// acquireFresh opens a brand-new Reader through the factory with the pool lock
// released — opening a journal handle can block — then re-checks the closed flag
// under the lock before handing the Reader out. Without that re-check a Close that
// raced between Acquire's first closed-check and this open would leak a live Reader
// the Client no longer tracks; instead the fresh Reader is closed and
// errClientClosed is reported, so Acquire rejects consistently once Close has run.
func (client *Client) acquireFresh() (Reader, error) {
	reader, err := client.factory.NewReader()
	if err != nil {
		return nil, err
	}

	client.mutex.Lock()
	closed := client.closed
	client.mutex.Unlock()

	if closed {
		return nil, errors.Join(errClientClosed, reader.Close())
	}

	return reader, nil
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
