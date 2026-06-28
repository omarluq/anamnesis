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

// countsByScopeCases drives Counts over the fixture across boot scope and tallied
// field: an explicit boot id scopes the tally to that boot, and an empty id tallies
// every boot. Each want map omits the empty-string key, so asserting equality also
// proves the unitless kernel record is skipped rather than bucketed under "".
var countsByScopeCases = []struct {
	want    map[string]int
	name    string
	bootID  string
	byField string
}{
	{
		name:    "units_scoped_to_current_boot",
		bootID:  bootCurrent,
		byField: journal.FieldUnit,
		want:    map[string]int{unitSSH: 2, unitNginx: 1, unitCron: 1, unitOomd: 1},
	},
	{
		name:    "priorities_within_current_boot",
		bootID:  bootCurrent,
		byField: journal.FieldPriority,
		want:    map[string]int{"3": 1, "4": 1, "5": 1, "6": 3},
	},
	{
		name:    "units_in_first_boot",
		bootID:  bootFirst,
		byField: journal.FieldUnit,
		want:    map[string]int{unitSSH: 1, unitCron: 1, unitOomd: 1},
	},
	{
		name:    "units_in_second_boot",
		bootID:  bootSecond,
		byField: journal.FieldUnit,
		want:    map[string]int{unitSSH: 1, unitNginx: 2, unitOomd: 1},
	},
	{
		name:    "units_across_every_boot_skip_unitless_records",
		bootID:  "",
		byField: journal.FieldUnit,
		want:    map[string]int{unitSSH: 4, unitNginx: 3, unitCron: 2, unitOomd: 3},
	},
}

func TestCountsTalliesByScope(t *testing.T) {
	t.Parallel()

	for _, testCase := range countsByScopeCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			client := newFixtureClient(t)

			counts, err := client.Counts(testCase.bootID, testCase.byField)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, counts)
		})
	}
}

func TestCountsScopesToBootViaMatch(t *testing.T) {
	t.Parallel()

	reader := newPooledReader()
	reader.On("AddMatch", mock.Anything).Return(nil)
	reader.On("SeekHead").Return(nil)
	reader.On("Next").Return(uint64(0), nil)

	client, factory := clientWithReader(t, reader, 1)

	counts, err := client.Counts(bootCurrent, journal.FieldUnit)
	require.NoError(t, err)
	assert.Empty(t, counts)

	reader.AssertCalled(t, "AddMatch", journal.FieldBootID+"="+bootCurrent)
	reader.AssertNumberOfCalls(t, "AddMatch", 1)

	factory.AssertExpectations(t)
}

func TestCountsEmptyBootAddsNoMatch(t *testing.T) {
	t.Parallel()

	reader := newPooledReader()
	reader.On("SeekHead").Return(nil)
	reader.On("Next").Return(uint64(0), nil)

	client, factory := clientWithReader(t, reader, 1)

	counts, err := client.Counts("", journal.FieldUnit)
	require.NoError(t, err)
	assert.Empty(t, counts)

	reader.AssertNotCalled(t, "AddMatch", mock.Anything)

	factory.AssertExpectations(t)
}

// uniqueByFilterCases drives Unique over the fixture across the field enumerated
// and the filter that scopes the scan, so the boot-scope, grep and process-name
// behaviors read as one table. Each want slice is the ascending distinct set the
// scoped scan yields.
var uniqueByFilterCases = []struct {
	adjust func(filter *journal.QueryFilter)
	name   string
	field  string
	want   []string
}{
	{
		name:   "sorted_distinct_units",
		field:  journal.FieldUnit,
		adjust: func(_ *journal.QueryFilter) {},
		want:   []string{unitCron, unitNginx, unitSSH, unitOomd},
	},
	{
		// The oldest boot lacks nginx, so scoping by BootID drops it from the set.
		name:   "units_scoped_to_filter_boot",
		field:  journal.FieldUnit,
		adjust: func(filter *journal.QueryFilter) { filter.BootID = bootFirst },
		want:   []string{unitCron, unitSSH, unitOomd},
	},
	{
		// Only systemd-oomd logs the memory-pressure message, so the in-Go Grep
		// predicate narrows the distinct units to that one unit.
		name:   "units_narrowed_by_grep_predicate",
		field:  journal.FieldUnit,
		adjust: func(filter *journal.QueryFilter) { filter.Grep = "memory pressure" },
		want:   []string{unitOomd},
	},
	{
		name:   "distinct_priorities_in_boot",
		field:  journal.FieldPriority,
		adjust: func(filter *journal.QueryFilter) { filter.BootID = bootCurrent },
		want:   []string{"3", "4", "5", "6"},
	},
	{
		// _COMM is present on every record, including the kernel entry that has no
		// unit, so the distinct set spans all five process names.
		name:   "every_process_comm",
		field:  journal.FieldComm,
		adjust: func(_ *journal.QueryFilter) {},
		want:   []string{"cron", "kernel", "nginx", "sshd", "systemd-oomd"},
	},
}

func TestUniqueByFilter(t *testing.T) {
	t.Parallel()

	for _, testCase := range uniqueByFilterCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			client := newFixtureClient(t)

			filter := zeroFilter()
			testCase.adjust(&filter)

			values, err := client.Unique(testCase.field, &filter)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, values)
		})
	}
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

			factory.AssertExpectations(t)
		})
	}
}
