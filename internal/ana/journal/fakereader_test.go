package journal_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/samber/oops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/journal"
)

// fixturePath points at the stored "journalctl --output=json" export the fake
// reader replays; the path is relative to the package directory under testdata.
const fixturePath = "testdata/sample.journal.ndjson"

// Fixture cardinalities the self-tests assert against, derived from
// testdata/sample.journal.ndjson. They double as readable names for the otherwise
// opaque match counts.
const (
	fixtureRecordCount = 13
	distinctBootCount  = 3
	sshRecordCount     = 4
	bootCurrentCount   = 6
	sshInCurrentCount  = 2
)

// Field values reused across the match scenarios; declared once so the scenarios
// read clearly and goconst stays quiet.
const (
	unitSSH     = "ssh.service"
	unitNginx   = "nginx.service"
	bootCurrent = "c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3"
)

// matchGroup is the set of "FIELD=value" constraints of a journald match
// expression: matches on the same field OR together; matches on different fields
// AND together.
type matchGroup struct {
	byField map[string][]string
}

// newMatchGroup returns an empty match group ready to accumulate field matches.
func newMatchGroup() matchGroup {
	return matchGroup{byField: make(map[string][]string)}
}

// fixtureReader is a real, fixture-backed journal.Reader: it replays the decoded
// records of a stored journalctl JSON export and applies journald's match algebra
// (AddMatch) over them, so the Client and host surface can be exercised without
// cgo or a live journal. It performs genuine filtering; it is not a stand-in test
// double.
type fixtureReader struct {
	records  []map[string]any
	group    matchGroup
	filtered []map[string]any
	pos      int
}

// newFixtureReader builds a reader over the shared, immutable fixture records.
// The records slice is never mutated, so one slice can back many readers.
func newFixtureReader(records []map[string]any) *fixtureReader {
	return &fixtureReader{
		records:  records,
		group:    newMatchGroup(),
		filtered: nil,
		pos:      -1,
	}
}

// AddMatch adds a "FIELD=value" exact-match constraint to the active query,
// OR-ing it with any prior matches on the same field. A match missing the "="
// separator is rejected.
func (reader *fixtureReader) AddMatch(match string) error {
	field, value, found := strings.Cut(match, "=")
	if !found {
		return oops.In("journal").Code("invalid_match").Errorf("match %q is not in FIELD=value form", match)
	}

	reader.group.byField[field] = append(reader.group.byField[field], value)

	return nil
}

// SeekHead materializes the records matching the active query, oldest first, and
// positions the cursor before the first of them.
func (reader *fixtureReader) SeekHead() error {
	filtered := make([]map[string]any, 0, len(reader.records))

	for _, record := range reader.records {
		if reader.matches(record) {
			filtered = append(filtered, record)
		}
	}

	reader.filtered = filtered
	reader.pos = -1

	return nil
}

// SeekRealtime positions the fixture reader for a windowed read. The fixture is small
// and read in full, and collect applies the [Since, Until] window in Go, so seeking to
// the head returns the correct window; the production sdjournalReader does the real
// libsystemd time seek for speed.
func (reader *fixtureReader) SeekRealtime(_ uint64) error {
	return reader.SeekHead()
}

// Next advances to the next matching record, returning 1 when it moved and 0 at
// the end of the matched set.
func (reader *fixtureReader) Next() (uint64, error) {
	if reader.pos+1 >= len(reader.filtered) {
		return 0, nil
	}

	reader.pos++

	return 1, nil
}

// Fields returns the raw field map of the record at the cursor. It errors when no
// record is current.
func (reader *fixtureReader) Fields() (map[string]any, error) {
	if reader.pos < 0 || reader.pos >= len(reader.filtered) {
		return nil, oops.In("journal").
			Code("no_current_record").
			Errorf("no record at cursor; call SeekHead then Next first")
	}

	return reader.filtered[reader.pos], nil
}

// FlushMatches drops every match and clears the materialized set so a pooled
// reader starts its next query clean.
func (reader *fixtureReader) FlushMatches() {
	reader.group = newMatchGroup()
	reader.filtered = nil
	reader.pos = -1
}

// Close releases the reader's state. The fixture reader holds no OS handle, so it
// only resets the cursor and always succeeds.
func (reader *fixtureReader) Close() error {
	reader.filtered = nil
	reader.pos = -1

	return nil
}

// matches reports whether record satisfies the active query: true when it
// satisfies every field constraint, or when no constraint has been added at all.
func (reader *fixtureReader) matches(record map[string]any) bool {
	if len(reader.group.byField) == 0 {
		return true
	}

	return groupMatches(reader.group, record)
}

// groupMatches reports whether record satisfies every field constraint in group,
// AND-ing across fields while OR-ing the allowed values within a field.
func groupMatches(group matchGroup, record map[string]any) bool {
	for field, values := range group.byField {
		if !lo.Contains(values, recordField(record, field)) {
			return false
		}
	}

	return true
}

// recordField renders a record's field as text, yielding "" when the field is
// absent or not a string (the only shapes journalctl matches against here).
func recordField(record map[string]any, field string) string {
	value, ok := record[field].(string)
	if !ok {
		return ""
	}

	return value
}

// compile-time assertion that fixtureReader satisfies the Reader seam.
var _ journal.Reader = (*fixtureReader)(nil)

// fixtureFactory hands the Client fresh fixture readers over one shared, decoded
// copy of the export, satisfying journal.ReaderFactory.
type fixtureFactory struct {
	records []map[string]any
}

// newFixtureFactory loads the export once and returns a factory that replays it.
func newFixtureFactory(t *testing.T) *fixtureFactory {
	t.Helper()

	return &fixtureFactory{records: loadFixture(t)}
}

// NewReader returns a fresh reader over the shared records; it never fails.
func (factory *fixtureFactory) NewReader() (journal.Reader, error) {
	return newFixtureReader(factory.records), nil
}

// compile-time assertion that fixtureFactory satisfies the ReaderFactory seam.
var _ journal.ReaderFactory = (*fixtureFactory)(nil)

// loadFixture reads and decodes the stored NDJSON export, failing the test if the
// file is unreadable or any line is not a JSON object.
func loadFixture(t *testing.T) []map[string]any {
	t.Helper()

	data, err := os.ReadFile(fixturePath)
	require.NoError(t, err)

	return parseNDJSON(t, data)
}

// parseNDJSON decodes one JSON object per non-blank line, the shape
// "journalctl --output=json" emits, and fails the test on any malformed line.
func parseNDJSON(t *testing.T, data []byte) []map[string]any {
	t.Helper()

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	records := make([]map[string]any, 0, len(lines))

	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		record := make(map[string]any)
		err := json.Unmarshal([]byte(trimmed), &record)
		require.NoErrorf(t, err, "line %d is not valid JSON", index+1)

		records = append(records, record)
	}

	return records
}

// matchedRecords drives reader through SeekHead and Next and returns the raw field
// maps of the records the active query matched, so a self-test can assert the
// fixtureReader filtered to the right records — by count and by raw field — without
// a decode seam. The end-to-end decode path is covered through Client.Query.
func matchedRecords(t *testing.T, reader journal.Reader) []map[string]any {
	t.Helper()

	require.NoError(t, reader.SeekHead())

	records := make([]map[string]any, 0)

	for {
		advanced, err := reader.Next()
		require.NoError(t, err)

		if advanced == 0 {
			break
		}

		fields, err := reader.Fields()
		require.NoError(t, err)

		records = append(records, fields)
	}

	return records
}

// newFixtureClient builds a pooled journal.Client backed by the shared fixture
// factory — the setup nearly every fixture-driven test shares — keeping a single
// idle Reader since the tests borrow one Reader at a time. The Client needs no
// teardown: it holds no OS handle and the fixture Readers carry none, so the
// caller just lets it fall out of scope.
func newFixtureClient(t *testing.T) *journal.Client {
	t.Helper()

	return journal.NewClientWithFactory(newFixtureFactory(t), 1)
}

// clientWithReader builds a pooled journal.Client whose mock factory hands out
// reader exactly once, the single-mock-reader setup the behavioral and
// error-propagation tests share. It returns the factory too so a caller can assert
// the pool opened exactly one Reader.
func clientWithReader(t *testing.T, reader journal.Reader, maxIdle int) (*journal.Client, *mockFactory) {
	t.Helper()

	factory := new(mockFactory)
	factory.On("NewReader").Return(reader, nil).Once()

	return journal.NewClientWithFactory(factory, maxIdle), factory
}

// matchCase drives one filtering scenario through the fixture reader. configure
// programs the matches; wantUnits/wantBoot are per-entry invariants the result
// must satisfy and wantCount is the expected number of matched entries.
type matchCase struct {
	name      string
	configure func(reader *fixtureReader) error
	wantBoot  string
	wantUnits []string
	wantCount int
}

// matchCases is a package-level table so the test function stays small and each
// scenario literal is written exhaustruct-complete.
var matchCases = []matchCase{
	{
		name: "unit_match_keeps_only_that_unit",
		configure: func(reader *fixtureReader) error {
			return reader.AddMatch(journal.FieldUnit + "=" + unitSSH)
		},
		wantBoot:  "",
		wantUnits: []string{unitSSH},
		wantCount: sshRecordCount,
	},
	{
		name: "boot_match_keeps_only_that_boot",
		configure: func(reader *fixtureReader) error {
			return reader.AddMatch(journal.FieldBootID + "=" + bootCurrent)
		},
		wantBoot:  bootCurrent,
		wantUnits: nil,
		wantCount: bootCurrentCount,
	},
	{
		name: "different_fields_and_together",
		configure: func(reader *fixtureReader) error {
			if err := reader.AddMatch(journal.FieldUnit + "=" + unitSSH); err != nil {
				return err
			}

			return reader.AddMatch(journal.FieldBootID + "=" + bootCurrent)
		},
		wantBoot:  bootCurrent,
		wantUnits: []string{unitSSH},
		wantCount: sshInCurrentCount,
	},
	{
		name: "no_match_returns_every_record",
		configure: func(_ *fixtureReader) error {
			return nil
		},
		wantBoot:  "",
		wantUnits: nil,
		wantCount: fixtureRecordCount,
	},
}

func TestFixtureLoadsValidMultiBootNDJSON(t *testing.T) {
	t.Parallel()

	records := loadFixture(t)
	require.Len(t, records, fixtureRecordCount)

	for index, record := range records {
		require.Containsf(t, record, journal.FieldCursor, "record %d missing __CURSOR", index)
		require.Containsf(t, record, journal.FieldBootID, "record %d missing _BOOT_ID", index)
	}

	boots := lo.Uniq(lo.Map(records, func(record map[string]any, _ int) string {
		return recordField(record, journal.FieldBootID)
	}))
	assert.Len(t, boots, distinctBootCount)
}

func TestFixtureReaderHonorsMatches(t *testing.T) {
	t.Parallel()

	records := loadFixture(t)

	for _, testCase := range matchCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			reader := newFixtureReader(records)
			require.NoError(t, testCase.configure(reader))

			matched := matchedRecords(t, reader)
			assert.Len(t, matched, testCase.wantCount)

			assertRecordsMatch(t, matched, testCase)
		})
	}
}

// assertRecordsMatch checks every matched record against the scenario invariants,
// reading the raw _SYSTEMD_UNIT and _BOOT_ID fields so the check proves the matches
// filtered by field rather than returning arbitrary records.
func assertRecordsMatch(t *testing.T, records []map[string]any, testCase matchCase) {
	t.Helper()

	for _, record := range records {
		if len(testCase.wantUnits) > 0 {
			assert.Contains(t, testCase.wantUnits, recordField(record, journal.FieldUnit))
		}

		if testCase.wantBoot != "" {
			assert.Equal(t, testCase.wantBoot, recordField(record, journal.FieldBootID))
		}
	}
}

func TestFixtureQueryDecodesEntriesViaParser(t *testing.T) {
	t.Parallel()

	client := newFixtureClient(t)

	filter := zeroFilter()
	filter.Unit = unitSSH

	entries, err := client.Query(&filter)
	require.NoError(t, err)
	require.Len(t, entries, sshRecordCount)

	first := entries[0]
	assert.Equal(t, unitSSH, first.Unit)
	assert.Equal(t, 6, first.Priority)
	assert.Equal(t, "sshd", first.Comm)
	assert.Equal(t, 1001, first.PID)
	assert.NotEmpty(t, first.Cursor)
	assert.Equal(t, 2026, first.Timestamp.Year())
	assert.Equal(t, time.June, first.Timestamp.Month())
	assert.Contains(t, first.Message, "Accepted password for root")
}

func TestFixtureReaderServesClientPool(t *testing.T) {
	t.Parallel()

	client := newFixtureClient(t)

	first, err := client.Acquire()
	require.NoError(t, err)
	require.NoError(t, first.AddMatch(journal.FieldUnit+"="+unitSSH))

	assert.Len(t, matchedRecords(t, first), sshRecordCount)

	require.NoError(t, client.Release(first))

	second, err := client.Acquire()
	require.NoError(t, err)
	assert.Same(t, first, second, "the pool reuses the flushed reader")

	assert.Len(t, matchedRecords(t, second), fixtureRecordCount)

	require.NoError(t, client.Release(second))
}
