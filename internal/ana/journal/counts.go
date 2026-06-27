package journal

import (
	"slices"

	"github.com/samber/lo"
)

// Counts tallies, for the entries in bootID, how many carry each distinct value of
// byField, returning that value→count histogram. An empty bootID imposes no scope
// and counts across every boot; records that lack byField are skipped rather than
// bucketed under the empty string. Counts is an O(n) scan: it decodes every
// matching record but allocates no []Entry, so it is cheaper than Query yet not
// algorithmically faster — budget for the walk on large boots. Counts borrows a
// pooled Reader for the call and releases it before returning.
func (client *Client) Counts(bootID, byField string) (map[string]int, error) {
	return withReader(client, "counts", func(reader Reader) (map[string]int, error) {
		return tallyField(reader, bootID, byField)
	})
}

// Unique returns the distinct values of field across the entries that filter
// admits, in ascending order, with absent values skipped. It scopes the scan with
// the same Unit, BootID and MaxPriority matches as Query and applies the Grep and
// realtime-window predicate in Go, so the result reflects the filter range.
//
// Caveat: the obvious libsystemd primitive for a field's distinct values,
// sd_journal_enumerate_unique, ignores the installed matches and enumerates the
// values across the entire journal, so it cannot scope to filter. Unique therefore
// scans the matching entries (an O(n) walk) instead of calling enumerate_unique,
// trading the primitive's speed for filter fidelity. Unique borrows a pooled
// Reader for the call and releases it before returning.
func (client *Client) Unique(field string, filter *QueryFilter) ([]string, error) {
	return withReader(client, "unique", func(reader Reader) ([]string, error) {
		return distinctField(reader, field, filter)
	})
}

// tallyField scopes reader to bootID, seeks to the head and folds each matching
// record's byField value into the histogram, wrapping any seek failure with
// journal context. An empty bootID adds no scope so every boot is counted.
func tallyField(reader Reader, bootID, byField string) (map[string]int, error) {
	if bootID != "" {
		if err := addMatch(reader, FieldBootID, bootID); err != nil {
			return nil, err
		}
	}

	if err := seekHead(reader); err != nil {
		return nil, err
	}

	return foldCounts(reader, byField)
}

// foldCounts walks reader from the current position, decoding each record and
// incrementing the count of its byField value, skipping records that lack the
// field, until the journal is exhausted.
func foldCounts(reader Reader, byField string) (map[string]int, error) {
	counts := make(map[string]int)

	err := forEachRecord(reader, func(fields map[string]any) {
		if value := fieldString(fields[byField]); value != "" {
			counts[value]++
		}
	})
	if err != nil {
		return nil, err
	}

	return counts, nil
}

// distinctField applies filter's matches to reader, seeks to the head and gathers
// the sorted distinct values of field over the entries that pass the in-Go
// predicate, wrapping any seek failure with journal context.
func distinctField(reader Reader, field string, filter *QueryFilter) ([]string, error) {
	if err := applyMatches(reader, filter); err != nil {
		return nil, err
	}

	if err := seekHead(reader); err != nil {
		return nil, err
	}

	return gatherUnique(reader, field, filter)
}

// gatherUnique walks reader from the current position, collecting each admitted
// record's field value into a set, until the journal is exhausted, then returns
// the set's members in ascending order.
func gatherUnique(reader Reader, field string, filter *QueryFilter) ([]string, error) {
	seen := make(map[string]struct{})

	err := forEachRecord(reader, func(fields map[string]any) {
		collectUnique(seen, fields, field, filter)
	})
	if err != nil {
		return nil, err
	}

	return sortedKeys(seen), nil
}

// collectUnique records the record's field value in seen when the record passes
// the filter's in-Go predicate and actually carries the field.
func collectUnique(seen map[string]struct{}, fields map[string]any, field string, filter *QueryFilter) {
	entry := parseEntry(fields)
	if !keep(&entry, filter) {
		return
	}

	if value := fieldString(fields[field]); value != "" {
		seen[value] = struct{}{}
	}
}

// sortedKeys returns the keys of set in ascending order.
func sortedKeys(set map[string]struct{}) []string {
	keys := lo.Keys(set)
	slices.Sort(keys)

	return keys
}
