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
	sshOrNginxCount    = 7
)

// Field values reused across the match scenarios; declared once so the scenarios
// read clearly and goconst stays quiet.
const (
	unitSSH     = "ssh.service"
	unitNginx   = "nginx.service"
	bootCurrent = "c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3"
)

// matchGroup is one term of the journald match expression: a set of
// "FIELD=value" constraints. Matches on the same field OR together; matches on
// different fields AND together within the group.
type matchGroup struct {
	byField map[string][]string
}

// newMatchGroup returns an empty match group ready to accumulate field matches.
func newMatchGroup() matchGroup {
	return matchGroup{byField: make(map[string][]string)}
}

// fixtureReader is a real, fixture-backed journal.Reader: it replays the decoded
// records of a stored journalctl JSON export and applies journald's match algebra
// (AddMatch/AddDisjunction) over them, so the Client and host surface can be
// exercised without cgo or a live journal. It performs genuine filtering; it is
// not a stand-in test double.
type fixtureReader struct {
	records  []map[string]any
	groups   []matchGroup
	filtered []map[string]any
	pos      int
}

// newFixtureReader builds a reader over the shared, immutable fixture records.
// The records slice is never mutated, so one slice can back many readers.
func newFixtureReader(records []map[string]any) *fixtureReader {
	return &fixtureReader{
		records:  records,
		groups:   []matchGroup{newMatchGroup()},
		filtered: nil,
		pos:      -1,
	}
}

// AddMatch adds a "FIELD=value" exact-match constraint to the current match
// group, OR-ing it with any prior matches on the same field. A match missing the
// "=" separator is rejected.
func (reader *fixtureReader) AddMatch(match string) error {
	field, value, found := strings.Cut(match, "=")
	if !found {
		return oops.In("journal").Code("invalid_match").Errorf("match %q is not in FIELD=value form", match)
	}

	group := reader.groups[len(reader.groups)-1]
	group.byField[field] = append(group.byField[field], value)

	return nil
}

// AddDisjunction starts a fresh match group so subsequent matches are OR-ed with
// the matches added so far, mirroring sd_journal_add_disjunction.
func (reader *fixtureReader) AddDisjunction() error {
	reader.groups = append(reader.groups, newMatchGroup())

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

// Next advances to the next matching record, returning 1 when it moved and 0 at
// the end of the matched set.
func (reader *fixtureReader) Next() (uint64, error) {
	if reader.pos+1 >= len(reader.filtered) {
		return 0, nil
	}

	reader.pos++

	return 1, nil
}

// Fields returns the raw field map of the record at the cursor, ready to decode
// with journal.DecodeFields. It errors when no record is current.
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
	reader.groups = []matchGroup{newMatchGroup()}
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

// matches reports whether record satisfies the active query: true when it matches
// any non-empty group, or when no constraint has been added at all.
func (reader *fixtureReader) matches(record map[string]any) bool {
	constrained := false

	for _, group := range reader.groups {
		if len(group.byField) == 0 {
			continue
		}

		constrained = true

		if groupMatches(group, record) {
			return true
		}
	}

	return !constrained
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

// walk drives reader through SeekHead and Next, decoding each matching record
// into an Entry via the JNL-02 parser exposed as journal.DecodeFields.
func walk(t *testing.T, reader journal.Reader) []journal.Entry {
	t.Helper()

	require.NoError(t, reader.SeekHead())

	entries := make([]journal.Entry, 0)

	for {
		advanced, err := reader.Next()
		require.NoError(t, err)

		if advanced == 0 {
			break
		}

		fields, err := reader.Fields()
		require.NoError(t, err)

		entries = append(entries, journal.DecodeFields(fields))
	}

	return entries
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
		name: "disjunction_ors_the_groups",
		configure: func(reader *fixtureReader) error {
			if err := reader.AddMatch(journal.FieldUnit + "=" + unitSSH); err != nil {
				return err
			}

			if err := reader.AddDisjunction(); err != nil {
				return err
			}

			return reader.AddMatch(journal.FieldUnit + "=" + unitNginx)
		},
		wantBoot:  "",
		wantUnits: []string{unitSSH, unitNginx},
		wantCount: sshOrNginxCount,
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

			entries := walk(t, reader)
			assert.Len(t, entries, testCase.wantCount)

			assertEntriesMatch(t, entries, testCase)
		})
	}
}

// assertEntriesMatch checks every returned entry against the scenario invariants,
// proving the matches filtered by field rather than returning arbitrary records.
func assertEntriesMatch(t *testing.T, entries []journal.Entry, testCase matchCase) {
	t.Helper()

	for index := range entries {
		entry := &entries[index]

		if len(testCase.wantUnits) > 0 {
			assert.Contains(t, testCase.wantUnits, entry.Unit)
		}

		if testCase.wantBoot != "" {
			assert.Equal(t, testCase.wantBoot, entry.BootID)
		}
	}
}

func TestFixtureReaderDecodesEntriesViaParser(t *testing.T) {
	t.Parallel()

	reader := newFixtureReader(loadFixture(t))
	require.NoError(t, reader.AddMatch(journal.FieldUnit+"="+unitSSH))

	entries := walk(t, reader)
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

	factory := newFixtureFactory(t)
	client := journal.NewClientWithFactory(factory, 1)

	first, err := client.Acquire()
	require.NoError(t, err)
	require.NoError(t, first.AddMatch(journal.FieldUnit+"="+unitSSH))

	entries := walk(t, first)
	assert.Len(t, entries, sshRecordCount)

	require.NoError(t, client.Release(first))

	second, err := client.Acquire()
	require.NoError(t, err)
	assert.Same(t, first, second, "the pool reuses the flushed reader")

	rest := walk(t, second)
	assert.Len(t, rest, fixtureRecordCount)

	require.NoError(t, client.Release(second))
	require.NoError(t, client.Close())
}
