package journal_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/samber/oops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/journal"
)

// mockReader is a testify mock of the journal.Reader seam. The pool lifecycle
// only drives FlushMatches and Close across acquire/release/close; the query-side
// methods exist to satisfy the interface and are never exercised here.
type mockReader struct {
	mock.Mock
}

// AddMatch records the match argument and replays the scripted error.
func (m *mockReader) AddMatch(match string) error {
	return m.Called(match).Error(0)
}

// AddDisjunction replays the scripted error.
func (m *mockReader) AddDisjunction() error {
	return m.Called().Error(0)
}

// SeekHead replays the scripted error.
func (m *mockReader) SeekHead() error {
	return m.Called().Error(0)
}

// Next replays the scripted advance count and error.
func (m *mockReader) Next() (uint64, error) {
	args := m.Called()

	count, ok := args.Get(0).(uint64)
	if !ok {
		return 0, args.Error(1)
	}

	return count, args.Error(1)
}

// Fields replays the scripted raw field map and error.
func (m *mockReader) Fields() (map[string]any, error) {
	args := m.Called()

	fields, ok := args.Get(0).(map[string]any)
	if !ok {
		return nil, args.Error(1)
	}

	return fields, args.Error(1)
}

// FlushMatches records that the pool reset the Reader before reuse.
func (m *mockReader) FlushMatches() {
	m.Called()
}

// Close records that the pool released the Reader and replays the scripted error.
func (m *mockReader) Close() error {
	return m.Called().Error(0)
}

// compile-time assertion that mockReader satisfies the Reader seam.
var _ journal.Reader = (*mockReader)(nil)

// mockFactory is a testify mock of the journal.ReaderFactory seam, scripting the
// Readers the pool opens when no idle Reader is available.
type mockFactory struct {
	mock.Mock
}

// NewReader replays the scripted Reader and error for one pool open.
func (m *mockFactory) NewReader() (journal.Reader, error) {
	args := m.Called()

	reader, ok := args.Get(0).(journal.Reader)
	if !ok {
		return nil, args.Error(1)
	}

	return reader, args.Error(1)
}

// compile-time assertion that mockFactory satisfies the ReaderFactory seam.
var _ journal.ReaderFactory = (*mockFactory)(nil)

// newPooledReader returns a mock Reader that tolerates the pool's lifecycle calls:
// FlushMatches on every release and Close when the pool drops the Reader.
func newPooledReader() *mockReader {
	reader := new(mockReader)
	reader.On("FlushMatches").Return()
	reader.On("Close").Return(nil)

	return reader
}

func TestClientAcquireReleaseReusesIdleReader(t *testing.T) {
	t.Parallel()

	reader := newPooledReader()

	factory := new(mockFactory)
	factory.On("NewReader").Return(reader, nil).Once()

	client := journal.NewClientWithFactory(factory, 4)

	first, err := client.Acquire()
	require.NoError(t, err)
	assert.Same(t, reader, first)

	require.NoError(t, client.Release(first))

	second, err := client.Acquire()
	require.NoError(t, err)
	assert.Same(t, first, second, "release then acquire must reuse the idle reader")

	require.NoError(t, client.Release(second))
	require.NoError(t, client.Close())

	factory.AssertExpectations(t)
	factory.AssertNumberOfCalls(t, "NewReader", 1)
	reader.AssertCalled(t, "FlushMatches")
	reader.AssertNumberOfCalls(t, "Close", 1)
}

func TestClientCloseReleasesEveryReader(t *testing.T) {
	t.Parallel()

	readers := []*mockReader{newPooledReader(), newPooledReader(), newPooledReader()}

	factory := new(mockFactory)
	for _, reader := range readers {
		factory.On("NewReader").Return(reader, nil).Once()
	}

	client := journal.NewClientWithFactory(factory, len(readers))

	held := make([]journal.Reader, 0, len(readers))

	for range readers {
		reader, err := client.Acquire()
		require.NoError(t, err)

		held = append(held, reader)
	}

	for _, reader := range held {
		require.NoError(t, client.Release(reader))
	}

	require.NoError(t, client.Close())

	for _, reader := range readers {
		reader.AssertNumberOfCalls(t, "Close", 1)
	}

	// Close is idempotent and must not double-close any pooled reader.
	require.NoError(t, client.Close())

	for _, reader := range readers {
		reader.AssertNumberOfCalls(t, "Close", 1)
	}

	factory.AssertExpectations(t)
}

func TestClientAcquireAfterCloseReturnsClosedError(t *testing.T) {
	t.Parallel()

	factory := new(mockFactory)

	client := journal.NewClientWithFactory(factory, 2)
	require.NoError(t, client.Close())

	reader, err := client.Acquire()
	assert.Nil(t, reader)
	require.Error(t, err)

	var oopsErr oops.OopsError

	require.ErrorAs(t, err, &oopsErr)
	assert.Equal(t, "client_closed", oopsErr.Code())

	factory.AssertNotCalled(t, "NewReader")
}

func TestClientAcquirePropagatesFactoryError(t *testing.T) {
	t.Parallel()

	factory := new(mockFactory)
	factory.On("NewReader").Return(nil, assert.AnError).Once()

	client := journal.NewClientWithFactory(factory, 2)

	reader, err := client.Acquire()
	assert.Nil(t, reader)
	require.ErrorIs(t, err, assert.AnError)

	factory.AssertExpectations(t)
}

func TestClientReleaseAfterCloseClosesReader(t *testing.T) {
	t.Parallel()

	reader := newPooledReader()

	factory := new(mockFactory)
	factory.On("NewReader").Return(reader, nil).Once()

	client := journal.NewClientWithFactory(factory, 2)

	acquired, err := client.Acquire()
	require.NoError(t, err)

	require.NoError(t, client.Close())
	require.NoError(t, client.Release(acquired))

	reader.AssertCalled(t, "FlushMatches")
	reader.AssertNumberOfCalls(t, "Close", 1)
	factory.AssertExpectations(t)
}

func TestClientReleaseOverflowClosesReaderWhileOpen(t *testing.T) {
	t.Parallel()

	pooled := newPooledReader()
	overflow := newPooledReader()

	factory := new(mockFactory)
	factory.On("NewReader").Return(pooled, nil).Once()
	factory.On("NewReader").Return(overflow, nil).Once()

	// maxIdle 0 clamps to one, so the pool keeps a single idle reader.
	client := journal.NewClientWithFactory(factory, 0)

	first, err := client.Acquire()
	require.NoError(t, err)
	assert.Same(t, pooled, first)

	second, err := client.Acquire()
	require.NoError(t, err)
	assert.Same(t, overflow, second)

	// The first release fills the lone idle slot.
	require.NoError(t, client.Release(first))

	// The second release overflows the bound while the client is still open, so the
	// reader is closed instead of pooled.
	require.NoError(t, client.Release(second))

	overflow.AssertNumberOfCalls(t, "Close", 1)
	pooled.AssertNumberOfCalls(t, "Close", 0)

	require.NoError(t, client.Close())
	factory.AssertExpectations(t)
}

func TestClientConcurrentAcquireNeverDoubleHandsReader(t *testing.T) {
	t.Parallel()

	const (
		workers    = 16
		iterations = 100
	)

	readers := make([]*mockReader, workers)

	factory := new(mockFactory)

	for index := range readers {
		readers[index] = newPooledReader()
		factory.On("NewReader").Return(readers[index], nil).Once()
	}

	client := journal.NewClientWithFactory(factory, workers)
	tracker := newHoldTracker()

	var waitGroup sync.WaitGroup

	waitGroup.Add(workers)

	for range workers {
		go func() {
			defer waitGroup.Done()

			hammerPool(client, tracker, iterations)
		}()
	}

	waitGroup.Wait()

	require.NoError(t, client.Close())
	assert.False(t, tracker.doubleHanded(), "pool handed the same reader to two callers at once")
	assert.False(t, tracker.failed(), "acquire or release returned an unexpected error")
}

// hammerPool repeatedly acquires, briefly holds, and releases a Reader, recording
// any double-hand or unexpected error through tracker. It runs on its own
// goroutine so the pool's idle list is exercised under the race detector.
func hammerPool(client *journal.Client, tracker *holdTracker, iterations int) {
	for range iterations {
		reader, err := client.Acquire()
		if err != nil {
			tracker.fail()

			return
		}

		tracker.take(reader)
		tracker.drop(reader)

		if relErr := client.Release(reader); relErr != nil {
			tracker.fail()

			return
		}
	}
}

// holdTracker records which Readers are currently held so a concurrent test can
// detect the pool handing the same Reader to two callers at once. It is safe for
// concurrent use.
type holdTracker struct {
	held    map[journal.Reader]struct{}
	mutex   sync.Mutex
	doubled atomic.Bool
	errored atomic.Bool
}

// newHoldTracker returns an empty holdTracker ready to record held Readers.
func newHoldTracker() *holdTracker {
	return &holdTracker{
		held:    make(map[journal.Reader]struct{}),
		mutex:   sync.Mutex{},
		doubled: atomic.Bool{},
		errored: atomic.Bool{},
	}
}

// take marks reader as held, flagging a double-hand if it was already held.
func (h *holdTracker) take(reader journal.Reader) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	if _, dup := h.held[reader]; dup {
		h.doubled.Store(true)
	}

	h.held[reader] = struct{}{}
}

// drop marks reader as no longer held.
func (h *holdTracker) drop(reader journal.Reader) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	delete(h.held, reader)
}

// fail records that a worker saw an unexpected acquire or release error.
func (h *holdTracker) fail() {
	h.errored.Store(true)
}

// doubleHanded reports whether the pool ever handed one Reader to two callers.
func (h *holdTracker) doubleHanded() bool {
	return h.doubled.Load()
}

// failed reports whether any worker saw an unexpected acquire or release error.
func (h *holdTracker) failed() bool {
	return h.errored.Load()
}
