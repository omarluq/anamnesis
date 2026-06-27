package journal

import (
	"strconv"
	"strings"

	"github.com/samber/oops"
)

const (
	// defaultLimit caps a Query whose filter leaves Limit unset (zero or negative).
	defaultLimit = 1000
	// maxLimit is the hard ceiling Query clamps any larger filter Limit down to.
	maxLimit = 10000
	// maxPriorityLevel is the highest (least severe) syslog PRIORITY, debug; it
	// bounds the PRIORITY disjunction so an out-of-range MaxPriority never adds
	// matches for levels journald can never emit.
	maxPriorityLevel = 7
)

// Query runs filter against the journal and returns the matching entries, oldest
// first, each carrying its __CURSOR for citation. Unit, BootID and MaxPriority
// translate to journald matches — MaxPriority expands to a PRIORITY disjunction of
// every level from 0 up to the ceiling — while Grep, Since and Until are applied in
// Go over the decoded entries. The result is capped at filter.Limit, which defaults
// to 1000 when unset and is clamped to a maximum of 10000. Query borrows a pooled
// Reader for the call and releases it before returning. filter crosses by pointer
// so the heavy struct is not copied, matching the host surface contract.
func (client *Client) Query(filter *QueryFilter) ([]Entry, error) {
	return withReader(client, "query", func(reader Reader) ([]Entry, error) {
		return runQuery(reader, filter)
	})
}

// runQuery applies the filter's matches to reader, seeks to the head and collects
// the decoded entries, wrapping any seek failure with journal context.
func runQuery(reader Reader, filter *QueryFilter) ([]Entry, error) {
	if err := applyMatches(reader, filter); err != nil {
		return nil, err
	}

	if err := seekHead(reader); err != nil {
		return nil, err
	}

	return collect(reader, filter)
}

// applyMatches records the journald-native constraints — Unit, BootID and the
// MaxPriority disjunction — on reader so it yields only the records the filter
// admits at the journal layer; Grep and the time window are left to collect.
func applyMatches(reader Reader, filter *QueryFilter) error {
	if filter.Unit != "" {
		if err := addMatch(reader, FieldUnit, filter.Unit); err != nil {
			return err
		}
	}

	if filter.BootID != "" {
		if err := addMatch(reader, FieldBootID, filter.BootID); err != nil {
			return err
		}
	}

	return applyPriorityMatches(reader, filter.MaxPriority)
}

// applyPriorityMatches adds one PRIORITY=level match for every level from 0 up to
// the ceiling. Matches on the same field OR together, so the group expresses
// "PRIORITY <= ceiling". A nil maxPriority imposes no ceiling; otherwise the
// ceiling is clamped to [0, maxPriorityLevel] so an out-of-range value still emits
// the single most-restrictive match rather than disabling the filter.
func applyPriorityMatches(reader Reader, maxPriority *int) error {
	if maxPriority == nil {
		return nil
	}

	ceiling := max(0, min(*maxPriority, maxPriorityLevel))

	for level := range ceiling + 1 {
		if err := addMatch(reader, FieldPriority, strconv.Itoa(level)); err != nil {
			return err
		}
	}

	return nil
}

// addMatch records a single "field=value" exact-match constraint on reader,
// wrapping any rejection with journal context.
func addMatch(reader Reader, field, value string) error {
	if err := reader.AddMatch(field + "=" + value); err != nil {
		return oops.In("journal").Code("add_match").Wrapf(err, "add %s match", field)
	}

	return nil
}

// collect walks reader from the current position, decoding each matching record
// and keeping those that pass the in-Go predicate, until the clamped limit is
// reached or the journal is exhausted.
func collect(reader Reader, filter *QueryFilter) ([]Entry, error) {
	limit := clampLimit(filter.Limit)
	entries := make([]Entry, 0, min(limit, defaultLimit))

	for len(entries) < limit {
		advanced, err := reader.Next()
		if err != nil {
			return nil, oops.In("journal").Code("advance").Wrapf(err, "advance journal cursor")
		}

		if advanced == 0 {
			break
		}

		fields, err := reader.Fields()
		if err != nil {
			return nil, oops.In("journal").Code("read_fields").Wrapf(err, "read current record fields")
		}

		entry := parseEntry(fields)
		if keep(&entry, filter) {
			entries = append(entries, entry)
		}
	}

	return entries, nil
}

// keep reports whether entry survives the filter's in-Go predicates: a MESSAGE
// substring match for Grep and the inclusive [Since, Until] realtime window. Zero
// value filter fields impose no constraint.
func keep(entry *Entry, filter *QueryFilter) bool {
	if filter.Grep != "" && !strings.Contains(entry.Message, filter.Grep) {
		return false
	}

	if !filter.Since.IsZero() && entry.Timestamp.Before(filter.Since) {
		return false
	}

	if !filter.Until.IsZero() && entry.Timestamp.After(filter.Until) {
		return false
	}

	return true
}

// clampLimit resolves a filter's Limit into the effective cap: the default of 1000
// for a zero or negative request, otherwise the request bounded at maxLimit.
func clampLimit(limit int) int {
	if limit <= 0 {
		return defaultLimit
	}

	return min(limit, maxLimit)
}

// seekHead positions reader before the first matching record, wrapping any failure
// with journal context. Query, Counts, Unique and Boots share it so the seek_head
// error code is defined once.
func seekHead(reader Reader) error {
	if err := reader.SeekHead(); err != nil {
		return oops.In("journal").Code("seek_head").Wrapf(err, "seek to journal head")
	}

	return nil
}

// forEachRecord walks reader from the current position, handing each record's raw
// field map to visit, until the journal is exhausted. It wraps any cursor-advance
// or field-read failure with journal context, so every full scan in the package
// shares one walk skeleton and one pair of error codes.
func forEachRecord(reader Reader, visit func(fields map[string]any)) error {
	for {
		advanced, err := reader.Next()
		if err != nil {
			return oops.In("journal").Code("advance").Wrapf(err, "advance journal cursor")
		}

		if advanced == 0 {
			break
		}

		fields, err := reader.Fields()
		if err != nil {
			return oops.In("journal").Code("read_fields").Wrapf(err, "read current record fields")
		}

		visit(fields)
	}

	return nil
}
