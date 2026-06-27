package main

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/omarluq/anamnesis/internal/ana/journal"
)

const (
	// defaultQueryLimit is the entry cap Query applies when a QueryFilter leaves
	// Limit at zero, matching the journal package's documented default.
	defaultQueryLimit = 1000
	// maxQueryLimit is the hard ceiling Query clamps any larger Limit down to,
	// matching the journal package's documented maximum.
	maxQueryLimit = 10000
)

// Journal is the read-only journald query surface the eval harness depends on.
// It mirrors the package journal accessor set so an in-memory fixture can be
// injected wherever the live sdjournal-backed client is expected, keeping eval
// runs deterministic and free of cgo, libsystemd, or a host journal. Filters
// are taken by pointer because a QueryFilter is a wide value; a nil filter is
// read as the zero-value filter that matches every entry.
type Journal interface {
	// Boots returns one BootInfo per distinct boot, most-recent first, with the
	// running boot at Index 0 and older boots at decreasing negative indexes.
	Boots() []journal.BootInfo
	// Query returns the entries matching filter in stored chronological order,
	// capped at the filter's effective Limit.
	Query(filter *journal.QueryFilter) []journal.Entry
	// Counts returns a histogram of byField values across the entries scoped to
	// bootID, or across every boot when bootID is empty.
	Counts(bootID, byField string) map[string]int
	// Unique returns the sorted distinct values of field across the entries that
	// match filter.
	Unique(field string, filter *journal.QueryFilter) []string
}

// FixtureJournal is an in-memory Journal backed by a fixed slice of journal
// entries, typically decoded from a stored journalctl --output=json export by
// ParseJournalExport. It answers Boots, Query, Counts, and Unique against that
// slice with no journald, cgo, or network dependency, which is what makes an
// eval run reproducible.
type FixtureJournal struct {
	entries []journal.Entry
}

// bootSpan is the realtime range a single boot's entries cover, accumulated as
// the fixture is scanned for Boots.
type bootSpan struct {
	first time.Time
	last  time.Time
}

// Ensure FixtureJournal satisfies the Journal interface it is injected behind.
var _ Journal = (*FixtureJournal)(nil)

// NewFixtureJournal builds a FixtureJournal over entries. The slice is retained
// by reference and treated as read-only, so callers must not mutate it after
// handing it over.
func NewFixtureJournal(entries []journal.Entry) *FixtureJournal {
	return &FixtureJournal{entries: entries}
}

// Boots returns one BootInfo per distinct _BOOT_ID, most-recent first. The
// running boot carries Index 0 and older boots carry decreasing negative
// indexes, and each boot's FirstSeen and LastSeen bound the realtime span of
// its entries. Entries without a _BOOT_ID contribute to no boot.
func (fixture *FixtureJournal) Boots() []journal.BootInfo {
	spans := fixture.bootSpans()
	ordered := orderedBootIDs(spans)
	boots := make([]journal.BootInfo, 0, len(ordered))

	for position, bootID := range ordered {
		span := spans[bootID]
		boots = append(boots, journal.BootInfo{
			FirstSeen: span.first,
			LastSeen:  span.last,
			ID:        bootID,
			Index:     position - (len(ordered) - 1),
		})
	}

	slices.Reverse(boots)

	return boots
}

// Query returns every entry matching filter, preserving the journal's stored
// chronological order and capping the result at the filter's effective Limit. A
// nil or zero-value filter matches all entries; see journal.QueryFilter for the
// per-field semantics each predicate applies.
func (fixture *FixtureJournal) Query(filter *journal.QueryFilter) []journal.Entry {
	return capEntries(fixture.matching(filter), orZeroFilter(filter).Limit)
}

// Counts tallies a histogram of the byField value over the entries scoped to
// bootID, or over every boot when bootID is empty. Entries whose byField is
// absent (the empty string) are skipped so they never form a phantom bucket.
func (fixture *FixtureJournal) Counts(bootID, byField string) map[string]int {
	keys := lo.FilterMap(fixture.entries, func(entry journal.Entry, _ int) (string, bool) {
		if !entryInBoot(&entry, bootID) {
			return "", false
		}

		value := fieldValue(&entry, byField)

		return value, value != ""
	})

	return lo.CountValues(keys)
}

// Unique returns the sorted distinct values of field across the entries that
// match filter. Entries whose field is absent contribute no value, so the empty
// string never appears in the result.
func (fixture *FixtureJournal) Unique(field string, filter *journal.QueryFilter) []string {
	values := lo.FilterMap(fixture.matching(filter), func(entry journal.Entry, _ int) (string, bool) {
		value := fieldValue(&entry, field)

		return value, value != ""
	})

	distinct := lo.Uniq(values)
	slices.Sort(distinct)

	return distinct
}

// matching returns the fixture entries that satisfy filter, resolving a nil
// filter to the zero-value filter that matches every entry. It is the shared
// match pipeline behind Query and Unique.
func (fixture *FixtureJournal) matching(filter *journal.QueryFilter) []journal.Entry {
	effective := orZeroFilter(filter)

	return lo.Filter(fixture.entries, func(entry journal.Entry, _ int) bool {
		return matchesFilter(&entry, effective)
	})
}

// bootSpans groups the fixture's entries by _BOOT_ID and records the earliest
// and latest realtime timestamp seen for each boot. Entries without a boot id
// are ignored.
func (fixture *FixtureJournal) bootSpans() map[string]bootSpan {
	spans := make(map[string]bootSpan)

	for index := range fixture.entries {
		entry := &fixture.entries[index]
		if entry.BootID == "" {
			continue
		}

		span, found := spans[entry.BootID]
		if !found {
			spans[entry.BootID] = bootSpan{first: entry.Timestamp, last: entry.Timestamp}

			continue
		}

		spans[entry.BootID] = widenSpan(span, entry.Timestamp)
	}

	return spans
}

// orZeroFilter returns filter unchanged when non-nil, or a pointer to a fresh
// zero-value filter when nil, so the predicates never dereference a nil filter.
func orZeroFilter(filter *journal.QueryFilter) *journal.QueryFilter {
	if filter != nil {
		return filter
	}

	var zero journal.QueryFilter

	return &zero
}

// widenSpan returns span stretched to include stamp, moving first earlier or
// last later as needed.
func widenSpan(span bootSpan, stamp time.Time) bootSpan {
	if stamp.Before(span.first) {
		span.first = stamp
	}

	if stamp.After(span.last) {
		span.last = stamp
	}

	return span
}

// orderedBootIDs returns the boot ids from spans sorted by their FirstSeen
// timestamp ascending, breaking ties on the id so the order is deterministic.
func orderedBootIDs(spans map[string]bootSpan) []string {
	ids := lo.Keys(spans)

	slices.SortFunc(ids, func(left, right string) int {
		if byFirst := spans[left].first.Compare(spans[right].first); byFirst != 0 {
			return byFirst
		}

		return cmp.Compare(left, right)
	})

	return ids
}

// matchesFilter reports whether entry satisfies every clause of filter: its boot
// and unit scope, its priority ceiling, its realtime window, and its message
// grep. A clause left at its zero value imposes no constraint.
func matchesFilter(entry *journal.Entry, filter *journal.QueryFilter) bool {
	return matchesScope(entry, filter) &&
		matchesPriority(entry, filter.MaxPriority) &&
		matchesWindow(entry, filter.Since, filter.Until) &&
		matchesGrep(entry, filter.Grep)
}

// matchesScope reports whether entry falls within the BootID and Unit scope of
// filter. An empty BootID or Unit imposes no constraint on that dimension.
func matchesScope(entry *journal.Entry, filter *journal.QueryFilter) bool {
	if filter.BootID != "" && entry.BootID != filter.BootID {
		return false
	}

	if filter.Unit != "" && entry.Unit != filter.Unit {
		return false
	}

	return true
}

// matchesPriority reports whether entry is within the priority ceiling. A nil
// ceiling imposes no constraint; otherwise the entry's PRIORITY must be at most
// the dereferenced value (numerically lower priorities are more severe).
func matchesPriority(entry *journal.Entry, maxPriority *int) bool {
	return maxPriority == nil || entry.Priority <= *maxPriority
}

// matchesWindow reports whether entry's timestamp lies within the inclusive
// [since, until] realtime window. A zero since or until drops that bound.
func matchesWindow(entry *journal.Entry, since, until time.Time) bool {
	if !since.IsZero() && entry.Timestamp.Before(since) {
		return false
	}

	if !until.IsZero() && entry.Timestamp.After(until) {
		return false
	}

	return true
}

// matchesGrep reports whether entry's message contains grep. An empty grep
// imposes no constraint.
func matchesGrep(entry *journal.Entry, grep string) bool {
	return grep == "" || strings.Contains(entry.Message, grep)
}

// entryInBoot reports whether entry belongs to bootID. An empty bootID matches
// every boot.
func entryInBoot(entry *journal.Entry, bootID string) bool {
	return bootID == "" || entry.BootID == bootID
}

// capEntries returns entries truncated to the effective limit derived from the
// requested limit, leaving a short slice untouched.
func capEntries(entries []journal.Entry, limit int) []journal.Entry {
	effective := effectiveLimit(limit)
	if len(entries) <= effective {
		return entries
	}

	return entries[:effective]
}

// effectiveLimit resolves a requested limit to the value Query enforces: the
// default for a non-positive request and the maximum for anything above it.
func effectiveLimit(limit int) int {
	if limit <= 0 {
		return defaultQueryLimit
	}

	return min(limit, maxQueryLimit)
}

// fieldValue renders the journald field named field for entry as the string key
// a Counts histogram or a Unique set groups on. Only the fields the harness
// groups by are mapped; any other name yields the empty string.
func fieldValue(entry *journal.Entry, field string) string {
	switch field {
	case journal.FieldUnit:
		return entry.Unit
	case journal.FieldBootID:
		return entry.BootID
	case journal.FieldComm:
		return entry.Comm
	case journal.FieldHostname:
		return entry.Hostname
	case journal.FieldPriority:
		return strconv.Itoa(entry.Priority)
	case journal.FieldPID:
		return strconv.Itoa(entry.PID)
	default:
		return ""
	}
}
