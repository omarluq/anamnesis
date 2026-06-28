package terminal

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A re-rendered message stores a freshly allocated line slice, so the address of its
// first cached line (&app.cache.items[i].lines[0]) changes when the renderer reruns and
// stays identical when the cache serves the message untouched. The tests below compare
// those addresses with assert.Same / assert.NotSame to detect recomputation directly.

// TestTranscriptCacheServesUnchangedMessageFromCache proves an unchanged message is
// served from the per-message cache and an unchanged transcript returns the memoized
// flatten, so neither the per-message render nor the flatten concatenation re-runs.
func TestTranscriptCacheServesUnchangedMessageFromCache(t *testing.T) {
	t.Parallel()

	app := newApp(newFakeScreen(80, 24), RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})
	app.appendUser("hello world")

	first := app.transcriptLines(80)
	require.NotEmpty(t, first, "the transcript renders the user message")

	cachedHead := &app.cache.items[0].lines[0]
	second := app.transcriptLines(80)

	assert.Same(t, cachedHead, &app.cache.items[0].lines[0],
		"an unchanged message keeps its cached lines instead of re-rendering")
	assert.Same(t, &first[0], &second[0],
		"an unchanged transcript returns the memoized flatten without rebuilding it")
}

// TestTranscriptCacheReRendersStreamedDelta proves a streamed thinking delta, which
// appends to the open pending block in place, invalidates that block's cache entry so the
// appended text renders.
func TestTranscriptCacheReRendersStreamedDelta(t *testing.T) {
	t.Parallel()

	app := newApp(newFakeScreen(80, 24), RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})
	app.appendThinkingDelta("first chunk ")
	app.transcriptLines(80)

	before := &app.cache.items[0].lines[0]

	app.appendThinkingDelta("second chunk")
	rendered := app.transcriptLines(80)

	assert.NotSame(t, before, &app.cache.items[0].lines[0],
		"a streamed delta mutates the pending block, so it re-renders")
	assert.Contains(t, strings.Join(nonBlankLineTexts(rendered), "\n"), "second chunk",
		"the re-rendered block shows the appended delta")
}

// TestTranscriptCacheReRendersSettledQueryBlock proves settling a pending query block
// (pending → settled, with the result filled in) invalidates its cache entry so the
// settled output renders instead of the stale pending header.
func TestTranscriptCacheReRendersSettledQueryBlock(t *testing.T) {
	t.Parallel()

	app := newApp(newFakeScreen(80, 24), RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})
	app.toolsExpanded = true
	app.appendQueryStart(1, "find boots", 0)
	app.transcriptLines(80)

	require.True(t, app.history[0].Pending, "the query block starts pending")

	before := &app.cache.items[0].lines[0]

	app.completeQuery(1, "found three boots")
	rendered := app.transcriptLines(80)

	require.False(t, app.history[0].Pending, "completing the query settles the block")
	assert.NotSame(t, before, &app.cache.items[0].lines[0],
		"settling the pending block invalidates its cache entry")
	assert.Contains(t, strings.Join(nonBlankLineTexts(rendered), "\n"), "found three boots",
		"the settled block renders its result")
}

// TestTranscriptCacheReRendersOnToolsExpandToggle proves toggling toolsExpanded
// re-renders query/code blocks, revealing the args body that the collapsed form hides.
func TestTranscriptCacheReRendersOnToolsExpandToggle(t *testing.T) {
	t.Parallel()

	app := newApp(newFakeScreen(80, 24), RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})
	app.appendCodeStart("boots := journal.Boots()\nfmt.Println(len(boots))")

	app.toolsExpanded = false
	collapsed := strings.Join(nonBlankLineTexts(app.transcriptLines(80)), "\n")
	before := &app.cache.items[0].lines[0]

	app.toolsExpanded = true
	expanded := strings.Join(nonBlankLineTexts(app.transcriptLines(80)), "\n")

	assert.NotSame(t, before, &app.cache.items[0].lines[0],
		"toggling tools-expanded re-renders the code block")
	assert.NotContains(t, collapsed, "fmt.Println", "the collapsed block hides its args body")
	assert.Contains(t, expanded, "fmt.Println(len(boots))", "the expanded block reveals its args body")
}

// TestTranscriptCacheReRendersOnWidthChange proves a width change re-wraps and
// re-renders a message: the same content wraps to more lines at a narrower width.
func TestTranscriptCacheReRendersOnWidthChange(t *testing.T) {
	t.Parallel()

	app := newApp(newFakeScreen(80, 24), RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})
	app.appendUser("a sufficiently long user message that must wrap onto several rows once the window narrows")

	wide := app.transcriptLines(80)
	before := &app.cache.items[0].lines[0]

	narrow := app.transcriptLines(30)

	assert.NotSame(t, before, &app.cache.items[0].lines[0],
		"a width change re-renders the message")
	assert.Greater(t, len(narrow), len(wide),
		"the message wraps onto more lines at the narrower width")
}

// TestTranscriptCacheKeepsUnchangedMessagesWhileOneMutates proves the cache is granular:
// when one message mutates, only that message re-renders while its untouched neighbors
// keep their cached lines.
func TestTranscriptCacheKeepsUnchangedMessagesWhileOneMutates(t *testing.T) {
	t.Parallel()

	app := newApp(newFakeScreen(80, 24), RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})
	app.appendUser("stable message")
	app.appendThinkingDelta("streaming ")
	app.transcriptLines(80)

	stableBefore := &app.cache.items[0].lines[0]
	thinkingBefore := &app.cache.items[1].lines[0]

	app.appendThinkingDelta("more reasoning")
	app.transcriptLines(80)

	assert.Same(t, stableBefore, &app.cache.items[0].lines[0],
		"the untouched user message stays served from cache")
	assert.NotSame(t, thinkingBefore, &app.cache.items[1].lines[0],
		"only the mutated thinking block re-renders")
}
