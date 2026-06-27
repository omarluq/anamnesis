package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/journal"
)

// Unit, boot, and cursor identifiers shared across this file's cases. They are
// constants both for readability and so the same string never appears as a
// repeated literal across the package.
const (
	unitPayments = "payments-api.service"
	unitSSH      = "sshd.service"
	unitProxy    = "nginx-edge.service"
	bootOld      = "boot-aa01"
	bootNew      = "boot-bb02"
	curOld1      = "s=a;i=1"
	curOld2      = "s=a;i=2"
	curOld3      = "s=a;i=3"
	curOld4      = "s=a;i=4"
	curNew1      = "s=b;i=1"
	curNew2      = "s=b;i=2"
	curNew3      = "s=b;i=3"
	curNew4      = "s=b;i=4"
)

// stampAt builds a UTC timestamp on the fixture's reference day, 2024-06-26, at
// the given hour and minute. Tests share it so the export they build and the
// bounds they assert against agree to the microsecond.
func stampAt(hour, minute int) time.Time {
	return time.Date(2024, time.June, 26, hour, minute, 0, 0, time.UTC)
}

// exportLine renders one journalctl --output=json record so the suite can build
// a FixtureJournal through the real ParseJournalExport decode path rather than
// hand-constructing Entry values.
func exportLine(cursor, bootID, unit, message string, priority, pid int, when time.Time) string {
	return fmt.Sprintf(
		`{"__CURSOR":%q,"__REALTIME_TIMESTAMP":"%d","_BOOT_ID":%q,"_SYSTEMD_UNIT":%q,`+
			`"_COMM":"proc","_HOSTNAME":"prod-1","PRIORITY":"%d","_PID":"%d","MESSAGE":%q}`,
		cursor, when.UnixMicro(), bootID, unit, priority, pid, message,
	)
}

// cursorsOf projects the __CURSOR of each entry, giving tests a stable, ordered
// handle to assert which records a query returned.
func cursorsOf(entries []journal.Entry) []string {
	return lo.Map(entries, func(entry journal.Entry, _ int) string {
		return entry.Cursor
	})
}

// newTestFixtureJournal builds a FixtureJournal from an eight-record export
// spanning two boots: the older bootOld at 08:00-08:10 and the newer bootNew at
// 09:00-09:30, with unitPayments appearing four times and unitSSH and unitProxy
// twice each. It is the shared corpus every case in this file asserts against.
func newTestFixtureJournal(t *testing.T) *FixtureJournal {
	t.Helper()

	exportLines := []string{
		exportLine(curOld1, bootOld, unitPayments, "listening on :8080", 6, 1000, stampAt(8, 0)),
		exportLine(curOld2, bootOld, unitSSH, "Accepted publickey for root", 6, 1010, stampAt(8, 5)),
		exportLine(curOld3, bootOld, unitPayments, "p99 latency above threshold", 4, 1000, stampAt(8, 7)),
		exportLine(curOld4, bootOld, unitProxy, "reloading configuration", 5, 1020, stampAt(8, 10)),
		exportLine(curNew1, bootNew, unitPayments, "Out of memory: killed worker", 2, 2000, stampAt(9, 0)),
		exportLine(curNew2, bootNew, unitPayments, "restarting after OOM", 4, 2001, stampAt(9, 1)),
		exportLine(curNew3, bootNew, unitSSH, "Accepted publickey for root", 6, 2010, stampAt(9, 5)),
		exportLine(curNew4, bootNew, unitProxy, "worker process exited", 3, 2020, stampAt(9, 30)),
	}

	entries, err := ParseJournalExport(strings.NewReader(strings.Join(exportLines, "\n") + "\n"))
	require.NoError(t, err)
	require.Len(t, entries, len(exportLines))

	return NewFixtureJournal(entries)
}

func TestFixtureJournalBootsAreOrderedMostRecentFirst(t *testing.T) {
	t.Parallel()

	fixture := newTestFixtureJournal(t)

	boots := fixture.Boots()
	require.Len(t, boots, 2)

	// The newer bootNew is the running boot at Index 0; the older bootOld trails
	// it at Index -1.
	assert.Equal(t, bootNew, boots[0].ID)
	assert.Equal(t, 0, boots[0].Index)
	assert.Equal(t, bootOld, boots[1].ID)
	assert.Equal(t, -1, boots[1].Index)

	// Each boot's span brackets the realtime range of just its own records.
	assert.Equal(t, stampAt(9, 0).UnixMicro(), boots[0].FirstSeen.UnixMicro())
	assert.Equal(t, stampAt(9, 30).UnixMicro(), boots[0].LastSeen.UnixMicro())
	assert.Equal(t, stampAt(8, 0).UnixMicro(), boots[1].FirstSeen.UnixMicro())
	assert.Equal(t, stampAt(8, 10).UnixMicro(), boots[1].LastSeen.UnixMicro())
}

func TestFixtureJournalQueryFiltersByUnit(t *testing.T) {
	t.Parallel()

	fixture := newTestFixtureJournal(t)

	var filter journal.QueryFilter

	filter.Unit = unitPayments

	matched := fixture.Query(&filter)
	assert.Len(t, matched, 4)

	for _, entry := range matched {
		assert.Equal(t, unitPayments, entry.Unit)
	}

	assert.ElementsMatch(t, []string{curOld1, curOld3, curNew1, curNew2}, cursorsOf(matched))
}

func TestFixtureJournalQueryFiltersByMaxPriority(t *testing.T) {
	t.Parallel()

	fixture := newTestFixtureJournal(t)

	var filter journal.QueryFilter

	filter.MaxPriority = new(3)

	matched := fixture.Query(&filter)
	require.Len(t, matched, 2)

	// Only PRIORITY <= 3 survives: the OOM kill (2) and the proxy worker exit (3).
	assert.ElementsMatch(t, []string{curNew1, curNew4}, cursorsOf(matched))
}

func TestFixtureJournalQueryFiltersByWindow(t *testing.T) {
	t.Parallel()

	fixture := newTestFixtureJournal(t)

	var filter journal.QueryFilter

	filter.Since = stampAt(8, 6)
	filter.Until = stampAt(9, 2)

	matched := fixture.Query(&filter)
	require.Len(t, matched, 4)

	// The 08:05 login falls below Since and the 09:05 login above Until, so the
	// inclusive [08:06, 09:02] window keeps exactly the four records between.
	assert.ElementsMatch(t, []string{curOld3, curOld4, curNew1, curNew2}, cursorsOf(matched))
}

func TestFixtureJournalQueryCombinesUnitPriorityAndWindow(t *testing.T) {
	t.Parallel()

	fixture := newTestFixtureJournal(t)

	var filter journal.QueryFilter

	filter.Unit = unitPayments
	filter.MaxPriority = new(4)
	filter.Since = stampAt(8, 6)
	filter.Until = stampAt(9, 0)

	matched := fixture.Query(&filter)
	require.Len(t, matched, 2)

	// Unit drops ssh/proxy, MaxPriority drops the 08:00 info line, Until drops the
	// 09:01 restart, leaving the 08:07 latency warning and the 09:00 OOM kill.
	assert.Equal(t, []string{curOld3, curNew1}, cursorsOf(matched))
}

func TestFixtureJournalQueryLimitCapsResults(t *testing.T) {
	t.Parallel()

	fixture := newTestFixtureJournal(t)

	var filter journal.QueryFilter

	filter.Limit = 3

	matched := fixture.Query(&filter)
	require.Len(t, matched, 3)

	// The cap keeps the earliest records in stored chronological order.
	assert.Equal(t, []string{curOld1, curOld2, curOld3}, cursorsOf(matched))
}

func TestFixtureJournalQueryLimitCapsAfterFiltering(t *testing.T) {
	t.Parallel()

	fixture := newTestFixtureJournal(t)

	var filter journal.QueryFilter

	filter.Unit = unitPayments
	filter.Limit = 2

	matched := fixture.Query(&filter)
	require.Len(t, matched, 2)

	// Limit applies after the Unit predicate, so the cap keeps the first two
	// matching payments records rather than the first two records overall.
	assert.Equal(t, []string{curOld1, curOld3}, cursorsOf(matched))
}

func TestFixtureJournalQueryNilFilterReturnsEverything(t *testing.T) {
	t.Parallel()

	fixture := newTestFixtureJournal(t)

	assert.Len(t, fixture.Query(nil), 8)
}

func TestFixtureJournalQueryFiltersByGrep(t *testing.T) {
	t.Parallel()

	fixture := newTestFixtureJournal(t)

	var filter journal.QueryFilter

	filter.Grep = "OOM"

	// Grep is a case-sensitive substring test, so the uppercase needle matches only
	// the "restarting after OOM" line, not the lowercase "Out of memory" kill.
	upper := fixture.Query(&filter)
	require.Len(t, upper, 1)
	assert.Equal(t, []string{curNew2}, cursorsOf(upper))

	filter.Grep = "Out of memory"

	// A distinct lowercase needle flips the match to the "Out of memory: killed
	// worker" kill line, confirming the contract is a plain Contains rather than a fold.
	lower := fixture.Query(&filter)
	require.Len(t, lower, 1)
	assert.Equal(t, []string{curNew1}, cursorsOf(lower))
}

func TestFixtureJournalQueryScopesByBootID(t *testing.T) {
	t.Parallel()

	fixture := newTestFixtureJournal(t)

	var filter journal.QueryFilter

	filter.BootID = bootNew

	// The BootID scope keeps only the four records from the newer boot.
	matched := fixture.Query(&filter)
	require.Len(t, matched, 4)
	assert.ElementsMatch(t, []string{curNew1, curNew2, curNew3, curNew4}, cursorsOf(matched))
}

func TestFixtureJournalQueryNonPositiveLimitReturnsEverything(t *testing.T) {
	t.Parallel()

	fixture := newTestFixtureJournal(t)

	var filter journal.QueryFilter

	filter.Limit = -1

	// A non-positive Limit resolves to the default cap, far above the eight-record
	// corpus, so every entry survives.
	assert.Len(t, fixture.Query(&filter), 8)
}

func TestFixtureJournalCountsGroupsByUnit(t *testing.T) {
	t.Parallel()

	fixture := newTestFixtureJournal(t)

	counts := fixture.Counts("", journal.FieldUnit)
	assert.Equal(t, map[string]int{
		unitPayments: 4,
		unitSSH:      2,
		unitProxy:    2,
	}, counts)
}

func TestFixtureJournalCountsIsScopedToBoot(t *testing.T) {
	t.Parallel()

	fixture := newTestFixtureJournal(t)

	counts := fixture.Counts(bootNew, journal.FieldUnit)
	assert.Equal(t, map[string]int{
		unitPayments: 2,
		unitSSH:      1,
		unitProxy:    1,
	}, counts)
}

func TestFixtureJournalUniqueReturnsDistinctUnits(t *testing.T) {
	t.Parallel()

	fixture := newTestFixtureJournal(t)

	// Unique returns the distinct values sorted ascending, so nginx-edge sorts
	// ahead of payments-api ahead of sshd.
	units := fixture.Unique(journal.FieldUnit, nil)
	assert.Equal(t, []string{unitProxy, unitPayments, unitSSH}, units)
}

func TestFixtureJournalUniqueReturnsDistinctBootIDs(t *testing.T) {
	t.Parallel()

	fixture := newTestFixtureJournal(t)

	bootIDs := fixture.Unique(journal.FieldBootID, nil)
	assert.Equal(t, []string{bootOld, bootNew}, bootIDs)
}

func TestFixtureJournalUniqueRespectsFilter(t *testing.T) {
	t.Parallel()

	fixture := newTestFixtureJournal(t)

	var filter journal.QueryFilter

	filter.MaxPriority = new(3)

	// Under the PRIORITY <= 3 ceiling only the OOM kill and the proxy exit remain,
	// so their two units are the distinct set, sorted ascending.
	units := fixture.Unique(journal.FieldUnit, &filter)
	assert.Equal(t, []string{unitProxy, unitPayments}, units)
}

func TestFixtureJournalIsInjectableAsJournal(t *testing.T) {
	t.Parallel()

	var sink Journal = newTestFixtureJournal(t)

	assert.Len(t, sink.Boots(), 2)
}
