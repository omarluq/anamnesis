// Package citations collects the journal cursors a controller session has made
// visible and validates that every cited cursor was actually returned by a
// journal query, so the final answer can only reference real evidence and never
// a cursor the model fabricated.
package citations

import (
	"slices"
	"strings"
	"sync"

	"github.com/samber/lo"
	"github.com/samber/oops"

	"github.com/omarluq/anamnesis/internal/ana/journal"
)

// Store tracks the journal cursors a session has made visible and the entries it
// has cited, so Validate can reject any citation whose cursor was never seen. The
// zero value is safe to use; NewStore merely preallocates the visible set. A Store
// is safe for concurrent use and must not be copied after first use.
type Store struct {
	visible map[string]struct{}
	cited   []journal.Entry
	mutex   sync.Mutex
}

// NewStore returns an empty Store ready to record visible entries and citations.
func NewStore() *Store {
	store := new(Store)
	store.visible = make(map[string]struct{})

	return store
}

// RecordVisible marks each entry's cursor as session-visible, meaning a real
// journal query returned it and the controller may legitimately cite it. Entries
// with an empty cursor are ignored because they carry no citable handle.
func (store *Store) RecordVisible(entries []journal.Entry) {
	store.mutex.Lock()
	defer store.mutex.Unlock()

	if store.visible == nil {
		store.visible = make(map[string]struct{})
	}

	for index := range entries {
		cursor := entries[index].Cursor
		if cursor == "" {
			continue
		}

		store.visible[cursor] = struct{}{}
	}
}

// Cite records entries the controller attached to its final answer. Repeated
// calls accumulate; Validate later checks every cited cursor against the
// session-visible set.
func (store *Store) Cite(entries []journal.Entry) {
	store.mutex.Lock()
	defer store.mutex.Unlock()

	store.cited = append(store.cited, entries...)
}

// Validate returns nil when every cited cursor was previously recorded visible.
// Otherwise it returns an oops error naming the fabricated cursors — those cited
// without ever being returned by a journal query — sorted for a stable message.
func (store *Store) Validate() error {
	store.mutex.Lock()
	defer store.mutex.Unlock()

	fabricated := store.fabricatedCursors()
	if len(fabricated) == 0 {
		return nil
	}

	return oops.
		In("citations").
		Code("fabricated_cursor").
		With("cursors", fabricated).
		Errorf("cited cursors were never returned by a journal query: %s", strings.Join(fabricated, ", "))
}

// fabricatedCursors returns the sorted, de-duplicated cursors that were cited but
// never recorded visible. Empty cursors are skipped. The caller must hold mutex.
func (store *Store) fabricatedCursors() []string {
	fabricated := lo.Uniq(lo.FilterMap(store.cited, func(entry journal.Entry, _ int) (string, bool) {
		_, visible := store.visible[entry.Cursor]

		return entry.Cursor, entry.Cursor != "" && !visible
	}))

	slices.Sort(fabricated)

	return fabricated
}
