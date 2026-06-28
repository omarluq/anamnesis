package repl_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/repl"
	"github.com/omarluq/anamnesis/internal/ana/repl/repltest"
)

// fakeEntry is the sample host record the round-trip test registers and ranges
// over. It mirrors the shape of journal.Entry closely enough to prove a host
// []struct crosses into interpreted code and its fields read back, without
// pulling the real journal package into the repl tests.
type fakeEntry struct {
	Unit     string
	Message  string
	Priority int
}

// journalQuerier is the one-method host surface the round-trip test registers as
// the journal package; RegisterSurface reflects its Query method into the
// interpreted journal.Query.
type journalQuerier interface {
	Query(filter string) []fakeEntry
}

// mockJournal is a testify mock of the journalQuerier surface. journalQuerier is
// a single-method seam whose Query return value the test scripts with
// .On("Query", ...).Return(...), and the built-in recorder asserts the
// interpreted code drove Query with the filter the test expected, so a testify
// mock is the right double rather than a bespoke fake.
type mockJournal struct {
	mock.Mock
}

// Query records the filter it was called with and replays the []fakeEntry scripted
// via .On("Query", filter).Return(entries).
func (m *mockJournal) Query(filter string) []fakeEntry {
	args := m.Called(filter)

	entries, ok := args.Get(0).([]fakeEntry)
	if !ok {
		return nil
	}

	return entries
}

// compile-time assertion that mockJournal satisfies the journalQuerier surface.
var _ journalQuerier = (*mockJournal)(nil)

// TestRegisterSurfaceRoundTripsStructSlice registers a host journal surface whose
// Query returns a []fakeEntry, then evaluates controller-style source that ranges
// the result printing each row and yields len(entries). It proves a host struct
// slice crosses into interpreted code intact: both rows reach stdout and the
// length crosses back as the turn's retval of 2.
func TestRegisterSurfaceRoundTripsStructSlice(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	surface := new(mockJournal)
	surface.On("Query", "ssh").Return([]fakeEntry{
		{Unit: "ssh.service", Message: "Accepted password for root", Priority: 6},
		{Unit: "ssh.service", Message: "Failed password for root", Priority: 3},
	})

	err := repl.RegisterSurface[journalQuerier](interpreter, "journal", surface)
	require.NoError(t, err)

	const src = `entries := journal.Query("ssh")
for _, e := range entries {
	fmt.Printf("%s %d %s\n", e.Unit, e.Priority, e.Message)
}
len(entries)`

	result, err := interpreter.Eval("turn_0", src)
	require.NoError(t, err)

	assert.Contains(t, result.Stdout, "ssh.service 6 Accepted password for root")
	assert.Contains(t, result.Stdout, "ssh.service 3 Failed password for root")

	require.True(t, result.Retval.IsValid(), "len(entries) crosses back as the turn retval")
	assert.Equal(t, int64(2), result.Retval.Int())

	surface.AssertExpectations(t)
	surface.AssertCalled(t, "Query", "ssh")
}

// TestRegisterSurfaceRejectsNonInterface proves the bridge refuses a concrete
// (non-interface) type parameter, returning an oops error in the repl domain
// rather than silently exporting the concrete value's whole method set.
func TestRegisterSurfaceRejectsNonInterface(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	err := repl.RegisterSurface[*mockJournal](interpreter, "journal", new(mockJournal))
	require.Error(t, err)

	repltest.RequireOopsCode(t, err, "repl", "host_surface_not_interface")
}

// TestRegisterSurfaceRejectsNilSurface proves the bridge converts a nil surface
// into an oops error in the repl domain rather than panicking when it reflects the
// surface's methods, honoring the never-panic contract HostDeps.Register documents.
func TestRegisterSurfaceRejectsNilSurface(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	err := repl.RegisterSurface[journalQuerier](interpreter, "journal", nil)
	require.Error(t, err)

	repltest.RequireOopsCode(t, err, "repl", "host_surface_nil")
}

// TestRegisterSurfaceRejectsTypedNilSurface proves the bridge rejects a typed-nil
// surface — a (*mockJournal)(nil) that still satisfies journalQuerier — with the
// host_surface_nil oops error rather than binding methods that would panic on the
// nil receiver at the first interpreted call. A typed nil is a valid reflect.Value,
// so the plain IsValid guard misses it; the nilable-kind check is what catches it.
func TestRegisterSurfaceRejectsTypedNilSurface(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	var surface journalQuerier = (*mockJournal)(nil)

	err := repl.RegisterSurface[journalQuerier](interpreter, "journal", surface)
	require.Error(t, err)

	repltest.RequireOopsCode(t, err, "repl", "host_surface_nil")
}

// TestRegisterSurfaceRejectsEmptyInterface proves the bridge refuses a zero-method
// interface type parameter: a surface that declares nothing to register yields an
// oops error in the repl domain rather than installing an empty package.
func TestRegisterSurfaceRejectsEmptyInterface(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	err := repl.RegisterSurface[any](interpreter, "empty", new(mockJournal))
	require.Error(t, err)

	repltest.RequireOopsCode(t, err, "repl", "host_surface_empty")
}
