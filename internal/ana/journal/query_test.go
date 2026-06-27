package journal_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/journal"
)

// bootSecond is the _BOOT_ID of the middle boot in the fixture; the current boot
// (bootCurrent) and ssh unit (unitSSH) constants come from fakereader_test.go.
const bootSecond = "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2"

// filterMaxPriority is the PRIORITY ceiling the match-translation case requests; it
// expands to the disjunction PRIORITY=0 .. PRIORITY=filterMaxPriority.
const filterMaxPriority = 4

// Query result cardinalities the behavioral cases assert against, derived from
// testdata/sample.journal.ndjson.
const (
	memoryPressureCount = 3
	windowedCount       = 2
	nginxErrInSecond    = 1
	explicitLimit       = 5
)

// zeroFilter returns an exhaustruct-complete QueryFilter with every field at its
// no-constraint zero value, so a case can set just the fields it exercises while
// the literal stays fully initialized.
func zeroFilter() journal.QueryFilter {
	return journal.QueryFilter{
		Since:       time.Time{},
		Until:       time.Time{},
		MaxPriority: nil,
		Unit:        "",
		BootID:      "",
		Grep:        "",
		Limit:       0,
	}
}

// newEndlessReader returns a testify mockReader that advances forever over one
// synthetic record, so a Query can be driven until its limit clamp halts the walk.
// It also tolerates the pool's FlushMatches and Close via newPooledReader.
func newEndlessReader() *mockReader {
	reader := newPooledReader()
	reader.On("SeekHead").Return(nil)
	reader.On("Next").Return(uint64(1), nil)
	reader.On("Fields").Return(map[string]any{
		journal.FieldCursor:  "synthetic-cursor",
		journal.FieldMessage: "synthetic record",
	}, nil)

	return reader
}

func TestQueryRecordsUnitBootAndPriorityMatches(t *testing.T) {
	t.Parallel()

	reader := newPooledReader()
	reader.On("AddMatch", mock.Anything).Return(nil)
	reader.On("SeekHead").Return(nil)
	reader.On("Next").Return(uint64(0), nil)

	factory := new(mockFactory)
	factory.On("NewReader").Return(reader, nil).Once()

	client := journal.NewClientWithFactory(factory, 1)

	filter := zeroFilter()
	filter.Unit = unitSSH
	filter.BootID = bootCurrent
	filter.MaxPriority = new(filterMaxPriority)

	entries, err := client.Query(&filter)
	require.NoError(t, err)
	assert.Empty(t, entries)

	reader.AssertCalled(t, "AddMatch", journal.FieldUnit+"="+unitSSH)
	reader.AssertCalled(t, "AddMatch", journal.FieldBootID+"="+bootCurrent)

	for level := range filterMaxPriority + 1 {
		reader.AssertCalled(t, "AddMatch", journal.FieldPriority+"="+strconv.Itoa(level))
	}

	// unit + boot + one match per priority level 0..filterMaxPriority.
	reader.AssertNumberOfCalls(t, "AddMatch", 2+filterMaxPriority+1)

	require.NoError(t, client.Close())
	factory.AssertExpectations(t)
}

func TestQueryGrepFiltersByMessageSubstring(t *testing.T) {
	t.Parallel()

	client := journal.NewClientWithFactory(newFixtureFactory(t), 1)

	filter := zeroFilter()
	filter.Grep = "memory pressure"

	entries, err := client.Query(&filter)
	require.NoError(t, err)
	assert.Len(t, entries, memoryPressureCount)

	for index := range entries {
		assert.Contains(t, entries[index].Message, "memory pressure")
	}

	require.NoError(t, client.Close())
}

func TestQuerySinceUntilBoundTheWindow(t *testing.T) {
	t.Parallel()

	since := time.UnixMicro(1782453630000000).UTC()
	until := time.UnixMicro(1782453660000000).UTC()

	client := journal.NewClientWithFactory(newFixtureFactory(t), 1)

	filter := zeroFilter()
	filter.Since = since
	filter.Until = until

	entries, err := client.Query(&filter)
	require.NoError(t, err)
	assert.Len(t, entries, windowedCount)

	for index := range entries {
		stamp := entries[index].Timestamp
		assert.False(t, stamp.Before(since), "entry predates the Since bound")
		assert.False(t, stamp.After(until), "entry postdates the Until bound")
	}

	require.NoError(t, client.Close())
}

func TestQueryUnitBootPriorityFilterSelectsExpectedEntry(t *testing.T) {
	t.Parallel()

	client := journal.NewClientWithFactory(newFixtureFactory(t), 1)

	filter := zeroFilter()
	filter.Unit = unitNginx
	filter.BootID = bootSecond
	filter.MaxPriority = new(3)

	entries, err := client.Query(&filter)
	require.NoError(t, err)
	require.Len(t, entries, nginxErrInSecond)

	only := entries[0]
	assert.Equal(t, unitNginx, only.Unit)
	assert.Equal(t, bootSecond, only.BootID)
	assert.Equal(t, 3, only.Priority)
	assert.Contains(t, only.Message, "connect() failed")

	require.NoError(t, client.Close())
}

func TestQueryDefaultsLimitToOneThousand(t *testing.T) {
	t.Parallel()

	reader := newEndlessReader()

	factory := new(mockFactory)
	factory.On("NewReader").Return(reader, nil).Once()

	client := journal.NewClientWithFactory(factory, 1)

	filter := zeroFilter()

	entries, err := client.Query(&filter)
	require.NoError(t, err)
	assert.Len(t, entries, 1000)

	reader.AssertNumberOfCalls(t, "Next", 1000)

	require.NoError(t, client.Close())
	factory.AssertExpectations(t)
}

func TestQueryClampsLimitAtTenThousand(t *testing.T) {
	t.Parallel()

	reader := newEndlessReader()

	factory := new(mockFactory)
	factory.On("NewReader").Return(reader, nil).Once()

	client := journal.NewClientWithFactory(factory, 1)

	filter := zeroFilter()
	filter.Limit = 50000

	entries, err := client.Query(&filter)
	require.NoError(t, err)
	assert.Len(t, entries, 10000)

	reader.AssertNumberOfCalls(t, "Next", 10000)

	require.NoError(t, client.Close())
	factory.AssertExpectations(t)
}

func TestQueryHonorsExplicitLimit(t *testing.T) {
	t.Parallel()

	client := journal.NewClientWithFactory(newFixtureFactory(t), 1)

	filter := zeroFilter()
	filter.Limit = explicitLimit

	entries, err := client.Query(&filter)
	require.NoError(t, err)
	assert.Len(t, entries, explicitLimit)

	require.NoError(t, client.Close())
}

func TestQueryEntriesCarryCursor(t *testing.T) {
	t.Parallel()

	client := journal.NewClientWithFactory(newFixtureFactory(t), 1)

	filter := zeroFilter()

	entries, err := client.Query(&filter)
	require.NoError(t, err)
	require.Len(t, entries, fixtureRecordCount)

	for index := range entries {
		assert.NotEmptyf(t, entries[index].Cursor, "entry %d carries no __CURSOR", index)
	}

	require.NoError(t, client.Close())
}

func TestQueryPropagatesAcquireFailure(t *testing.T) {
	t.Parallel()

	factory := new(mockFactory)
	factory.On("NewReader").Return(nil, assert.AnError).Once()

	client := journal.NewClientWithFactory(factory, 1)

	filter := zeroFilter()

	entries, err := client.Query(&filter)
	assert.Nil(t, entries)
	require.ErrorIs(t, err, assert.AnError)

	factory.AssertExpectations(t)
}

// TestQueryNilFilterMatchesEverything pins the nil-filter contract for Client.Query:
// the shared helpers normalize a nil *QueryFilter to an empty one, so the call cannot
// panic and behaves like the zero-value "match everything" filter, returning every
// fixture record.
func TestQueryNilFilterMatchesEverything(t *testing.T) {
	t.Parallel()

	client := journal.NewClientWithFactory(newFixtureFactory(t), 1)

	entries, err := client.Query(nil)
	require.NoError(t, err)
	assert.Len(t, entries, fixtureRecordCount)

	require.NoError(t, client.Close())
}

// TestUniqueNilFilterMatchesEverything pins the same contract for Client.Unique,
// whose nil filter reaches applyMatches and keep through counts.go: normalizing nil
// to an empty filter keeps the scan panic-free and yields the distinct field values
// across every entry, identical to the explicit empty-filter result.
func TestUniqueNilFilterMatchesEverything(t *testing.T) {
	t.Parallel()

	client := journal.NewClientWithFactory(newFixtureFactory(t), 1)

	values, err := client.Unique(journal.FieldUnit, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{unitCron, unitNginx, unitSSH, unitOomd}, values)

	require.NoError(t, client.Close())
}

// queryErrCase drives one journald error-propagation branch of Query: script
// programs the mock Reader for the failing call and adjust sets the filter fields
// that branch needs, so the table covers add_match, seek_head, advance and
// read_fields with one uniform propagation assertion.
type queryErrCase struct {
	script func(reader *mockReader)
	adjust func(filter *journal.QueryFilter)
	name   string
}

// queryErrCases enumerates the four Reader-failure branches Query must surface, as a
// package-level table so each scenario literal stays exhaustruct-complete.
var queryErrCases = []queryErrCase{
	{
		name:   "add_match_failure",
		script: func(reader *mockReader) { reader.On("AddMatch", mock.Anything).Return(assert.AnError) },
		adjust: func(filter *journal.QueryFilter) { filter.Unit = unitSSH },
	},
	{
		name:   "seek_head_failure",
		script: func(reader *mockReader) { reader.On("SeekHead").Return(assert.AnError) },
		adjust: func(_ *journal.QueryFilter) {},
	},
	{
		name: "advance_failure",
		script: func(reader *mockReader) {
			reader.On("SeekHead").Return(nil)
			reader.On("Next").Return(uint64(0), assert.AnError)
		},
		adjust: func(_ *journal.QueryFilter) {},
	},
	{
		name: "read_fields_failure",
		script: func(reader *mockReader) {
			reader.On("SeekHead").Return(nil)
			reader.On("Next").Return(uint64(1), nil)
			reader.On("Fields").Return(nil, assert.AnError)
		},
		adjust: func(_ *journal.QueryFilter) {},
	},
}

func TestQueryPropagatesReaderErrors(t *testing.T) {
	t.Parallel()

	for _, testCase := range queryErrCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			reader := newPooledReader()
			testCase.script(reader)

			factory := new(mockFactory)
			factory.On("NewReader").Return(reader, nil).Once()

			client := journal.NewClientWithFactory(factory, 1)

			filter := zeroFilter()
			testCase.adjust(&filter)

			entries, err := client.Query(&filter)
			assert.Nil(t, entries)
			require.ErrorIs(t, err, assert.AnError)

			require.NoError(t, client.Close())
			factory.AssertExpectations(t)
		})
	}
}
