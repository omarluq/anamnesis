package terminal

import "github.com/omarluq/anamnesis/internal/tui"

// cachedMessage memoizes one history message's rendered lines together with the
// inputs that produced them: the message value, the render width, and the expand
// state. A frame reuses Lines only when all three still match the live message, so any
// mutation — a streamed thinking delta, a pending block settling, a resize, or a
// ctrl+o expand toggle — changes a key field and forces a fresh render. Because a
// chatMessage is a comparable value (a role, content, query id, depth, and pending
// flag), equality is the whole invalidation rule: there is no separate dirty bookkeeping
// to fall out of sync with the transcript.
type cachedMessage struct {
	lines    []tui.Line
	message  chatMessage
	width    int
	expanded bool
	valid    bool
}

// transcriptCache holds the per-message render cache plus a memoized flatten of the
// whole transcript. The per-message layer skips re-wrapping and re-highlighting an
// unchanged message; the flatten layer skips even re-concatenating the stacked line
// slice when every message, the width, and the expand state are all unchanged since the
// last frame. Together they turn an idle redraw (a spinner tick mid-run) into O(1) work
// and a single streamed delta into one message's re-render instead of the whole
// history's, replacing the previous O(history × output-size) cost per frame.
type transcriptCache struct {
	items        []cachedMessage
	flat         []tui.Line
	flatWidth    int
	flatLen      int
	flatExpanded bool
	flatValid    bool
}

// emptyTranscriptCache returns the zero-valued cache newApp seeds the App with. It uses
// a zero value rather than a composite literal so exhaustruct stays satisfied without
// naming fields that are all zero.
func emptyTranscriptCache() transcriptCache {
	var cache transcriptCache

	return cache
}

// zeroCachedMessage returns an empty, invalid cache slot. resize appends these as the
// history grows, again via a zero value to keep exhaustruct happy.
func zeroCachedMessage() cachedMessage {
	var slot cachedMessage

	return slot
}

// resize grows or shrinks the per-message cache to match the history length, dropping
// slots for removed messages. anamnesis only appends to or mutates history in place, so
// a shrink never happens today, but handling it keeps the cache correct if that ever
// changes. Any length change invalidates the memoized flatten.
func (cache *transcriptCache) resize(length int) {
	if len(cache.items) == length {
		return
	}

	if length < len(cache.items) {
		cache.items = cache.items[:length]
	}

	for len(cache.items) < length {
		cache.items = append(cache.items, zeroCachedMessage())
	}

	cache.flatValid = false
}

// messageLines returns the rendered lines for history[index], serving them from the
// per-message cache when the message value, width, and expand state are all unchanged
// and re-rendering (then re-caching) otherwise. The caller guarantees index is in range
// and the cache has been resized to the history length.
func (cache *transcriptCache) messageLines(app *App, width, index int) []tui.Line {
	message := app.history[index]
	slot := &cache.items[index]

	if slot.valid && slot.width == width && slot.expanded == app.toolsExpanded && slot.message == message {
		return slot.lines
	}

	lines := app.renderMessage(width, message)
	*slot = cachedMessage{
		lines:    lines,
		message:  message,
		width:    width,
		expanded: app.toolsExpanded,
		valid:    true,
	}

	return lines
}

// allCached reports whether every history message is already served by a matching
// per-message cache entry for width and the current expand state — the precondition for
// returning the memoized flatten without rebuilding it. Each comparison short-circuits on
// the message value, and a content string that has not been reassigned shares its backing
// with the cached copy, so an unchanged message compares in constant time.
func (cache *transcriptCache) allCached(app *App, width int) bool {
	for index := range app.history {
		slot := &cache.items[index]
		if !slot.valid || slot.width != width || slot.expanded != app.toolsExpanded ||
			slot.message != app.history[index] {
			return false
		}
	}

	return true
}

// lines flattens the transcript into one stacked line slice. It returns the memoized
// flatten untouched when every message, the width, and the expand state are unchanged
// since the last build, and otherwise rebuilds it from the per-message cache so only
// genuinely changed messages re-render. The returned slice is read-only to its caller —
// the viewport windows it and the drawer reads it — so handing back the memoized backing
// array is safe.
func (cache *transcriptCache) lines(app *App, width int) []tui.Line {
	cache.resize(len(app.history))

	if cache.flatValid && cache.flatWidth == width && cache.flatExpanded == app.toolsExpanded &&
		cache.flatLen == len(app.history) && cache.allCached(app, width) {
		return cache.flat
	}

	flat := make([]tui.Line, 0, len(app.history)*messageMetadataRows)
	for index := range app.history {
		flat = append(flat, cache.messageLines(app, width, index)...)
	}

	cache.flat = flat
	cache.flatWidth = width
	cache.flatExpanded = app.toolsExpanded
	cache.flatLen = len(app.history)
	cache.flatValid = true

	return flat
}
