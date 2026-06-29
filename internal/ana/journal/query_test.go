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

// TestQuerySeeksByRealtimeForAWindowedFilter proves a query carrying a Since lower
// bound positions the reader with SeekRealtime — the libsystemd time seek that skips
// straight to the window — rather than SeekHead, so a recent narrow window does not
// decode the whole journal from the head. This is the fix for the full-journal-scan
// hang; a regression that reverted seekWindow to SeekHead would still pass the result
// tests but fail here.
func TestQuerySeeksByRealtimeForAWindowedFilter(t *testing.T) {
	t.Parallel()

	reader := newPooledReader()
	reader.On("SeekRealtime", mock.Anything).Return(nil)
	reader.On("Next").Return(uint64(0), nil)

	client, _ := clientWithReader(t, reader, 1)

	filter := zeroFilter()
	filter.Since = windowSince

	_, err := client.Query(&filter)
	require.NoError(t, err)

	reader.AssertCalled(t, "SeekRealtime", uint64(windowSince.UnixMicro()))
	reader.AssertNotCalled(t, "SeekHead")
}

// TestQuerySeeksToHeadWhenFilterHasNoLowerBound proves a query with no Since still
// seeks to the head, so the seek-by-realtime path is taken only when there is a window
// to skip to.
func TestQuerySeeksToHeadWhenFilterHasNoLowerBound(t *testing.T) {
	t.Parallel()

	reader := newPooledReader()
	reader.On("SeekHead").Return(nil)
	reader.On("Next").Return(uint64(0), nil)

	client, _ := clientWithReader(t, reader, 1)

	filter := zeroFilter()

	_, err := client.Query(&filter)
	require.NoError(t, err)

	reader.AssertCalled(t, "SeekHead")
	reader.AssertNotCalled(t, "SeekRealtime", mock.Anything)
}

func TestQueryRecordsUnitBootAndPriorityMatches(t *testing.T) {
	t.Parallel()

	reader := newPooledReader()
	reader.On("AddMatch", mock.Anything).Return(nil)
	reader.On("SeekHead").Return(nil)
	reader.On("Next").Return(uint64(0), nil)

	client, factory := clientWithReader(t, reader, 1)

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

	factory.AssertExpectations(t)
}

// Realtime bounds of the fixture window the Since/Until behavior selects, shared by
// the case's filter setup and its per-entry bounds check.
var (
	windowSince = time.UnixMicro(1782453630000000).UTC()
	windowUntil = time.UnixMicro(1782453660000000).UTC()
)

// queryBehaviorCases drives the fixture-backed Query behaviors that share the shape
// "build a filter, assert the matched count, then check a per-entry invariant", so
// the grep, time-window, explicit-limit and cursor behaviors read as one table. A
// nil perEntry asserts the count alone.
var queryBehaviorCases = []struct {
	adjust   func(filter *journal.QueryFilter)
	perEntry func(t *testing.T, entry *journal.Entry)
	name     string
	want     int
}{
	{
		name:   "grep_filters_by_message_substring",
		adjust: func(filter *journal.QueryFilter) { filter.Grep = "memory pressure" },
		perEntry: func(t *testing.T, entry *journal.Entry) {
			t.Helper()
			assert.Contains(t, entry.Message, "memory pressure")
		},
		want: memoryPressureCount,
	},
	{
		name: "since_until_bound_the_window",
		adjust: func(filter *journal.QueryFilter) {
			filter.Since = windowSince
			filter.Until = windowUntil
		},
		perEntry: func(t *testing.T, entry *journal.Entry) {
			t.Helper()
			assert.False(t, entry.Timestamp.Before(windowSince), "entry predates the Since bound")
			assert.False(t, entry.Timestamp.After(windowUntil), "entry postdates the Until bound")
		},
		want: windowedCount,
	},
	{
		name:     "explicit_limit_caps_the_result",
		adjust:   func(filter *journal.QueryFilter) { filter.Limit = explicitLimit },
		perEntry: nil,
		want:     explicitLimit,
	},
	{
		name:   "entries_carry_cursor",
		adjust: func(_ *journal.QueryFilter) {},
		perEntry: func(t *testing.T, entry *journal.Entry) {
			t.Helper()
			assert.NotEmpty(t, entry.Cursor, "entry carries no __CURSOR")
		},
		want: fixtureRecordCount,
	},
}

func TestQueryBehaviors(t *testing.T) {
	t.Parallel()

	for _, testCase := range queryBehaviorCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			client := newFixtureClient(t)

			filter := zeroFilter()
			testCase.adjust(&filter)

			entries, err := client.Query(&filter)
			require.NoError(t, err)
			require.Len(t, entries, testCase.want)

			if testCase.perEntry != nil {
				for index := range entries {
					testCase.perEntry(t, &entries[index])
				}
			}
		})
	}
}

func TestQueryUnitBootPriorityFilterSelectsExpectedEntry(t *testing.T) {
	t.Parallel()

	client := newFixtureClient(t)

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
}

func TestQueryDefaultsLimitToOneThousand(t *testing.T) {
	t.Parallel()

	reader := newEndlessReader()
	client, factory := clientWithReader(t, reader, 1)

	filter := zeroFilter()

	entries, err := client.Query(&filter)
	require.NoError(t, err)
	assert.Len(t, entries, 1000)

	reader.AssertNumberOfCalls(t, "Next", 1000)

	factory.AssertExpectations(t)
}

func TestQueryClampsLimitAtTenThousand(t *testing.T) {
	t.Parallel()

	reader := newEndlessReader()
	client, factory := clientWithReader(t, reader, 1)

	filter := zeroFilter()
	filter.Limit = 50000

	entries, err := client.Query(&filter)
	require.NoError(t, err)
	assert.Len(t, entries, 10000)

	reader.AssertNumberOfCalls(t, "Next", 10000)

	factory.AssertExpectations(t)
}

func TestQueryPropagatesAcquireFailure(t *testing.T) {
	t.Parallel()

	factory := failingFactory()
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

	client := newFixtureClient(t)

	entries, err := client.Query(nil)
	require.NoError(t, err)
	assert.Len(t, entries, fixtureRecordCount)
}

// TestUniqueNilFilterMatchesEverything pins the same contract for Client.Unique,
// whose nil filter reaches applyMatches and keep through counts.go: normalizing nil
// to an empty filter keeps the scan panic-free and yields the distinct field values
// across every entry, identical to the explicit empty-filter result.
func TestUniqueNilFilterMatchesEverything(t *testing.T) {
	t.Parallel()

	client := newFixtureClient(t)

	values, err := client.Unique(journal.FieldUnit, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{unitCron, unitNginx, unitSSH, unitOomd}, values)
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

			client, factory := clientWithReader(t, reader, 1)

			filter := zeroFilter()
			testCase.adjust(&filter)

			entries, err := client.Query(&filter)
			assert.Nil(t, entries)
			require.ErrorIs(t, err, assert.AnError)

			factory.AssertExpectations(t)
		})
	}
}
