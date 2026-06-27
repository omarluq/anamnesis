package journal_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/journal"
)

// Unit names the histogram and distinct-value assertions reuse; unitSSH and
// unitNginx are declared in the sibling fakereader test. Naming them keeps goconst
// quiet and the expected maps readable.
const (
	unitCron = "cron.service"
	unitOomd = "systemd-oomd.service"
)

// countErrCase drives one error-propagation scenario shared by Counts and Unique.
// arrange programs the factory (and any Reader it hands out) for the failing path,
// and invoke makes the call under test, returning its value and error so the table
// can assert uniform propagation regardless of which method failed.
type countErrCase struct {
	arrange func() *mockFactory
	invoke  func(client *journal.Client) (any, error)
	name    string
}

// failingFactory hands out an acquire failure: the pool's first open errors before
// any Reader exists.
func failingFactory() *mockFactory {
	factory := new(mockFactory)
	factory.On("NewReader").Return(nil, assert.AnError).Once()

	return factory
}

// seekFailFactory hands out a Reader whose SeekHead fails, so the scan errors after
// acquire but before any record is read.
func seekFailFactory() *mockFactory {
	reader := newPooledReader()
	reader.On("SeekHead").Return(assert.AnError)

	factory := new(mockFactory)
	factory.On("NewReader").Return(reader, nil).Once()

	return factory
}

// advanceFailFactory hands out a Reader that seeks cleanly but errors on the first
// Next, so the scan errors mid-walk.
func advanceFailFactory() *mockFactory {
	reader := newPooledReader()
	reader.On("SeekHead").Return(nil)
	reader.On("Next").Return(uint64(0), assert.AnError)

	factory := new(mockFactory)
	factory.On("NewReader").Return(reader, nil).Once()

	return factory
}

// invokeCounts calls Counts with an empty boot scope so no AddMatch precedes the
// failing SeekHead in the seek case.
func invokeCounts(client *journal.Client) (any, error) {
	return client.Counts("", journal.FieldUnit)
}

// invokeUnique calls Unique with a no-constraint filter so applyMatches is a no-op
// and the failing SeekHead is reached directly.
func invokeUnique(client *journal.Client) (any, error) {
	filter := zeroFilter()

	return client.Unique(journal.FieldUnit, &filter)
}

// countErrCases enumerates the acquire, seek and advance failures for both Counts
// and Unique as a package-level table so each scenario literal stays
// exhaustruct-complete.
var countErrCases = []countErrCase{
	{name: "counts_acquire_failure", arrange: failingFactory, invoke: invokeCounts},
	{name: "counts_seek_failure", arrange: seekFailFactory, invoke: invokeCounts},
	{name: "counts_advance_failure", arrange: advanceFailFactory, invoke: invokeCounts},
	{name: "unique_acquire_failure", arrange: failingFactory, invoke: invokeUnique},
	{name: "unique_seek_failure", arrange: seekFailFactory, invoke: invokeUnique},
	{name: "unique_advance_failure", arrange: advanceFailFactory, invoke: invokeUnique},
}

func TestCountsTalliesUnitsScopedToCurrentBoot(t *testing.T) {
	t.Parallel()

	client := journal.NewClientWithFactory(newFixtureFactory(t), 1)

	counts, err := client.Counts(bootCurrent, journal.FieldUnit)
	require.NoError(t, err)
	assert.Equal(t, map[string]int{
		unitSSH:   2,
		unitNginx: 1,
		unitCron:  1,
		unitOomd:  1,
	}, counts)

	require.NoError(t, client.Close())
}

func TestCountsTalliesPrioritiesWithinBoot(t *testing.T) {
	t.Parallel()

	client := journal.NewClientWithFactory(newFixtureFactory(t), 1)

	counts, err := client.Counts(bootCurrent, journal.FieldPriority)
	require.NoError(t, err)
	assert.Equal(t, map[string]int{"3": 1, "4": 1, "5": 1, "6": 3}, counts)

	require.NoError(t, client.Close())
}

func TestCountsScopeIsolatesBoots(t *testing.T) {
	t.Parallel()

	client := journal.NewClientWithFactory(newFixtureFactory(t), 1)

	first, err := client.Counts(bootFirst, journal.FieldUnit)
	require.NoError(t, err)
	assert.Equal(t, map[string]int{unitSSH: 1, unitCron: 1, unitOomd: 1}, first)

	second, err := client.Counts(bootSecond, journal.FieldUnit)
	require.NoError(t, err)
	assert.Equal(t, map[string]int{unitSSH: 1, unitNginx: 2, unitOomd: 1}, second)

	require.NoError(t, client.Close())
}

func TestCountsEmptyBootCountsEveryBootAndSkipsAbsentField(t *testing.T) {
	t.Parallel()

	client := journal.NewClientWithFactory(newFixtureFactory(t), 1)

	counts, err := client.Counts("", journal.FieldUnit)
	require.NoError(t, err)
	// Every boot is tallied and the kernel record, which carries no _SYSTEMD_UNIT,
	// is skipped rather than bucketed under the empty string.
	assert.Equal(t, map[string]int{
		unitSSH:   4,
		unitNginx: 3,
		unitCron:  2,
		unitOomd:  3,
	}, counts)
	assert.NotContains(t, counts, "")

	require.NoError(t, client.Close())
}

func TestCountsScopesToBootViaMatch(t *testing.T) {
	t.Parallel()

	reader := newPooledReader()
	reader.On("AddMatch", mock.Anything).Return(nil)
	reader.On("SeekHead").Return(nil)
	reader.On("Next").Return(uint64(0), nil)

	factory := new(mockFactory)
	factory.On("NewReader").Return(reader, nil).Once()

	client := journal.NewClientWithFactory(factory, 1)

	counts, err := client.Counts(bootCurrent, journal.FieldUnit)
	require.NoError(t, err)
	assert.Empty(t, counts)

	reader.AssertCalled(t, "AddMatch", journal.FieldBootID+"="+bootCurrent)
	reader.AssertNumberOfCalls(t, "AddMatch", 1)

	require.NoError(t, client.Close())
	factory.AssertExpectations(t)
}

func TestCountsEmptyBootAddsNoMatch(t *testing.T) {
	t.Parallel()

	reader := newPooledReader()
	reader.On("SeekHead").Return(nil)
	reader.On("Next").Return(uint64(0), nil)

	factory := new(mockFactory)
	factory.On("NewReader").Return(reader, nil).Once()

	client := journal.NewClientWithFactory(factory, 1)

	counts, err := client.Counts("", journal.FieldUnit)
	require.NoError(t, err)
	assert.Empty(t, counts)

	reader.AssertNotCalled(t, "AddMatch", mock.Anything)

	require.NoError(t, client.Close())
	factory.AssertExpectations(t)
}

func TestUniqueReturnsSortedDistinctUnits(t *testing.T) {
	t.Parallel()

	client := journal.NewClientWithFactory(newFixtureFactory(t), 1)

	filter := zeroFilter()

	values, err := client.Unique(journal.FieldUnit, &filter)
	require.NoError(t, err)
	assert.Equal(t, []string{unitCron, unitNginx, unitSSH, unitOomd}, values)

	require.NoError(t, client.Close())
}

func TestUniqueScopesToFilterBoot(t *testing.T) {
	t.Parallel()

	client := journal.NewClientWithFactory(newFixtureFactory(t), 1)

	filter := zeroFilter()
	filter.BootID = bootFirst

	values, err := client.Unique(journal.FieldUnit, &filter)
	require.NoError(t, err)
	// The oldest boot lacks nginx, so scoping by BootID drops it from the set.
	assert.Equal(t, []string{unitCron, unitSSH, unitOomd}, values)

	require.NoError(t, client.Close())
}

func TestUniqueHonorsGrepPredicate(t *testing.T) {
	t.Parallel()

	client := journal.NewClientWithFactory(newFixtureFactory(t), 1)

	filter := zeroFilter()
	filter.Grep = "memory pressure"

	values, err := client.Unique(journal.FieldUnit, &filter)
	require.NoError(t, err)
	// Only systemd-oomd logs the memory-pressure message, so the in-Go Grep
	// predicate narrows the distinct units to that one unit.
	assert.Equal(t, []string{unitOomd}, values)

	require.NoError(t, client.Close())
}

func TestUniqueDistinctPrioritiesInBoot(t *testing.T) {
	t.Parallel()

	client := journal.NewClientWithFactory(newFixtureFactory(t), 1)

	filter := zeroFilter()
	filter.BootID = bootCurrent

	values, err := client.Unique(journal.FieldPriority, &filter)
	require.NoError(t, err)
	assert.Equal(t, []string{"3", "4", "5", "6"}, values)

	require.NoError(t, client.Close())
}

func TestUniqueSpansEveryProcessComm(t *testing.T) {
	t.Parallel()

	client := journal.NewClientWithFactory(newFixtureFactory(t), 1)

	filter := zeroFilter()

	values, err := client.Unique(journal.FieldComm, &filter)
	require.NoError(t, err)
	// _COMM is present on every record, including the kernel entry that has no
	// unit, so the distinct set spans all five process names.
	assert.Equal(t, []string{"cron", "kernel", "nginx", "sshd", "systemd-oomd"}, values)

	require.NoError(t, client.Close())
}

func TestCountsAndUniquePropagateReaderErrors(t *testing.T) {
	t.Parallel()

	for _, testCase := range countErrCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			factory := testCase.arrange()
			client := journal.NewClientWithFactory(factory, 1)

			value, err := testCase.invoke(client)
			assert.Nil(t, value)
			require.ErrorIs(t, err, assert.AnError)

			require.NoError(t, client.Close())
			factory.AssertExpectations(t)
		})
	}
}
