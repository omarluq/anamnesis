//go:build linux

package journal

import (
	"strconv"

	"github.com/coreos/go-systemd/v22/sdjournal"
	"github.com/samber/oops"
)

// DefaultMaxIdle is the idle-Reader pool size the production Client keeps for
// reuse, so a burst of queries reuses a handful of warm sdjournal cursors rather
// than reopening one per call.
const DefaultMaxIdle = 4

// NewClient builds the production journal Client backed by libsystemd's sdjournal
// reader. Construction opens no journal handle: the pool lazily opens an
// sdjournal-backed Reader on the first Acquire, so a Client assembles even where
// the journal is unreadable and the open — together with any libsystemd failure it
// raises — is deferred to the first query rather than to construction. This is the
// host-surface counterpart of openai.NewClient: the real handle, lazily realized.
func NewClient() *Client {
	return NewClientWithFactory(sdjournalFactory{}, DefaultMaxIdle)
}

// sdjournalFactory opens sdjournal-backed Readers for a Client's pool, wrapping
// libsystemd's journal cursor behind the package's Reader seam.
type sdjournalFactory struct{}

// compile-time assertion that sdjournalFactory satisfies the pool's factory seam.
var _ ReaderFactory = sdjournalFactory{}

// NewReader opens a fresh libsystemd journal cursor positioned at the head, or
// returns an oops error tagged with the journal domain when the journal cannot be
// opened — typically a missing libsystemd or a caller outside the systemd-journal
// group.
func (sdjournalFactory) NewReader() (Reader, error) {
	handle, err := sdjournal.NewJournal()
	if err != nil {
		return nil, oops.In("journal").Code("open").Wrapf(err, "open sdjournal reader")
	}

	return &sdjournalReader{handle: handle}, nil
}

// sdjournalReader adapts one libsystemd journal cursor to the Reader seam the
// Client pool drives, forwarding each match, seek and advance to the underlying
// handle and decoding the current record into the raw field map the parser reads.
type sdjournalReader struct {
	// handle is the underlying libsystemd journal cursor.
	handle *sdjournal.Journal
}

// compile-time assertion that sdjournalReader satisfies the Reader seam.
var _ Reader = (*sdjournalReader)(nil)

// AddMatch records a "FIELD=value" constraint on the cursor, wrapping any
// libsystemd rejection with journal context.
func (reader *sdjournalReader) AddMatch(match string) error {
	if err := reader.handle.AddMatch(match); err != nil {
		return oops.In("journal").Code("add_match").Wrapf(err, "add match %q", match)
	}

	return nil
}

// SeekHead positions the cursor before the first matching record, wrapping any
// libsystemd rejection with journal context.
func (reader *sdjournalReader) SeekHead() error {
	if err := reader.handle.SeekHead(); err != nil {
		return oops.In("journal").Code("seek_head").Wrapf(err, "seek to journal head")
	}

	return nil
}

// SeekRealtime positions the cursor by wall-clock time via libsystemd's
// sd_journal_seek_realtime_usec, so the next Next yields the first matching record at
// or after usec; it wraps any rejection with journal context.
func (reader *sdjournalReader) SeekRealtime(usec uint64) error {
	if err := reader.handle.SeekRealtimeUsec(usec); err != nil {
		return oops.In("journal").Code("seek_realtime").Wrapf(err, "seek to realtime %d", usec)
	}

	return nil
}

// Next advances the cursor to the next matching record, returning the number of
// records advanced — zero at the end of the journal — or an oops error wrapping a
// libsystemd advance failure.
func (reader *sdjournalReader) Next() (uint64, error) {
	advanced, err := reader.handle.Next()
	if err != nil {
		return 0, oops.In("journal").Code("next").Wrapf(err, "advance journal cursor")
	}

	return advanced, nil
}

// Fields reads the current record and returns its raw journald field map ready for
// the parser, or an oops error wrapping a libsystemd read failure.
func (reader *sdjournalReader) Fields() (map[string]any, error) {
	entry, err := reader.handle.GetEntry()
	if err != nil {
		return nil, oops.In("journal").Code("get_entry").Wrapf(err, "read current journal entry")
	}

	return decodeEntry(entry), nil
}

// FlushMatches drops every recorded match so the pooled cursor starts its next
// query clean.
func (reader *sdjournalReader) FlushMatches() {
	reader.handle.FlushMatches()
}

// Close releases the underlying libsystemd journal handle, wrapping any close
// failure with journal context.
func (reader *sdjournalReader) Close() error {
	if err := reader.handle.Close(); err != nil {
		return oops.In("journal").Code("close").Wrapf(err, "close sdjournal reader")
	}

	return nil
}

// decodeEntry projects an sdjournal entry into the raw field map the parser reads:
// the journald fields cross unchanged, and the cursor and realtime timestamp the
// sdjournal entry carries beside its field map are reinstated under their __CURSOR
// and __REALTIME_TIMESTAMP keys so parseEntry recovers the entry handle and clock.
func decodeEntry(entry *sdjournal.JournalEntry) map[string]any {
	fields := make(map[string]any, len(entry.Fields)+2)

	for key, value := range entry.Fields {
		fields[key] = value
	}

	fields[FieldCursor] = entry.Cursor
	fields[FieldRealtime] = strconv.FormatUint(entry.RealtimeTimestamp, 10)

	return fields
}
