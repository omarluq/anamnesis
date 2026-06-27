package citations_test

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/samber/oops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/citations"
	"github.com/omarluq/anamnesis/internal/ana/journal"
)

// cursorOne is the cursor reused across the acceptance table rows; hoisting it
// keeps goconst happy and the table readable.
const cursorOne = "cur-1"

// entryAt is a fixed instant feeding the exhaustruct-complete Entry helper, so
// the cursor under test is the only field that varies between entries.
var entryAt = time.Date(2026, time.June, 26, 9, 0, 0, 0, time.UTC)

// entryWithCursor builds an exhaustruct-complete journal.Entry carrying cursor,
// letting tests speak in cursors without restating every journald field.
func entryWithCursor(cursor string) journal.Entry {
	return journal.Entry{
		Timestamp: entryAt,
		Cursor:    cursor,
		BootID:    "boot-alpha",
		Unit:      "ssh.service",
		Comm:      "sshd",
		Hostname:  "host-a",
		Message:   "log line for " + cursor,
		Priority:  6,
		PID:       4242,
	}
}

// entriesWithCursors maps cursors to entries via entryWithCursor in order.
func entriesWithCursors(cursors ...string) []journal.Entry {
	entries := make([]journal.Entry, 0, len(cursors))
	for _, cursor := range cursors {
		entries = append(entries, entryWithCursor(cursor))
	}

	return entries
}

func TestStoreValidateAcceptsRecordedCursors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		visible []string
		cited   []string
	}{
		{name: "single_visible_cited", visible: []string{cursorOne}, cited: []string{cursorOne}},
		{
			name:    "subset_of_visible",
			visible: []string{cursorOne, "cur-2", "cur-3"},
			cited:   []string{cursorOne, "cur-3"},
		},
		{name: "no_citations", visible: []string{cursorOne}, cited: nil},
		{name: "repeated_citation", visible: []string{cursorOne}, cited: []string{cursorOne, cursorOne}},
		{name: "empty_cursor_ignored", visible: []string{cursorOne}, cited: []string{""}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			store := citations.NewStore()
			store.RecordVisible(entriesWithCursors(testCase.visible...))
			store.Cite(entriesWithCursors(testCase.cited...))

			assert.NoError(t, store.Validate())
		})
	}
}

func TestStoreValidateRejectsFabricatedCursors(t *testing.T) {
	t.Parallel()

	store := citations.NewStore()
	store.RecordVisible(entriesWithCursors("cur-real-1", "cur-real-2"))
	store.Cite(entriesWithCursors("cur-real-1", "cur-fake-9", "cur-fake-3"))

	err := store.Validate()
	require.Error(t, err)

	var oopsErr oops.OopsError
	require.ErrorAs(t, err, &oopsErr)
	assert.Equal(t, "fabricated_cursor", oopsErr.Code())

	// The contiguous substring proves both fabricated cursors appear and that they
	// are listed sorted for a stable, deterministic message, while the real cursor
	// is never named.
	require.ErrorContains(t, err, "cur-fake-3, cur-fake-9")
	assert.NotContains(t, err.Error(), "cur-real-1")
	assert.Equal(t, []string{"cur-fake-3", "cur-fake-9"}, oopsErr.Context()["cursors"])
}

func TestStoreValidateDeduplicatesFabricatedCursor(t *testing.T) {
	t.Parallel()

	store := citations.NewStore()
	store.Cite(entriesWithCursors("cur-fake", "cur-fake"))

	err := store.Validate()
	require.Error(t, err)

	var oopsErr oops.OopsError
	require.ErrorAs(t, err, &oopsErr)
	assert.Equal(t, []string{"cur-fake"}, oopsErr.Context()["cursors"])
}

func TestStoreValidateEmptyStore(t *testing.T) {
	t.Parallel()

	store := citations.NewStore()

	assert.NoError(t, store.Validate())
}

func TestStoreConcurrentRecordAndCite(t *testing.T) {
	t.Parallel()

	store := citations.NewStore()

	const goroutines = 128

	var waitGroup sync.WaitGroup

	waitGroup.Add(goroutines)

	for index := range goroutines {
		go func() {
			defer waitGroup.Done()

			cursor := "cur-" + strconv.Itoa(index)
			store.RecordVisible(entriesWithCursors(cursor))
			store.Cite(entriesWithCursors(cursor))
		}()
	}

	waitGroup.Wait()

	// Every cited cursor was recorded visible by its own goroutine, so a clean
	// run under -race must validate with no fabricated cursors.
	assert.NoError(t, store.Validate())
}
