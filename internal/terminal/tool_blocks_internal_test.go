package terminal

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseQueryContentKeepsMarkerLikeOutputLines proves a sub-answer whose own
// lines start with reserved section markers ("output:", "error:", "tool:") stays
// verbatim output content once the output section opens, rather than re-opening a
// section and corrupting the rendered query block (a real risk on journald data).
func TestParseQueryContentKeepsMarkerLikeOutputLines(t *testing.T) {
	t.Parallel()

	content := "tool: agent.Query\narguments:\n{\"prompt\":\"why\"}\noutput:\n" +
		"the unit logged:\noutput: disk full\nerror: i/o timeout\ntool: restart attempted"

	parsed := parseQueryContent(content)

	assert.Equal(t, "agent.Query", parsed.Name, "the tool header still names the query")
	assert.JSONEq(t, `{"prompt":"why"}`, parsed.Args, "the arguments section is parsed before the output opens")
	assert.Equal(t,
		"the unit logged:\noutput: disk full\nerror: i/o timeout\ntool: restart attempted",
		parsed.Output,
		"marker-like lines inside the output stay verbatim output content")
	assert.Empty(t, parsed.Error,
		"a marker-like 'error:' line inside the output does not populate the error section")
}

// TestRenderPendingBlockRespectsToolsExpanded proves a still-running (pending) block
// honors the ctrl+o expansion toggle. While collapsed it shows the pending header
// only; while expanded it additionally reveals the args section — the running code or
// sub-call prompt — alongside the collapse hint. This is what lets a user read the
// full in-flight code while a minutes-long turn is still executing, rather than only
// its one-line header summary.
func TestRenderPendingBlockRespectsToolsExpanded(t *testing.T) {
	t.Parallel()

	app := newTestApp()

	// The first source line surfaces in the header summary; the second only ever shows
	// inside the expanded args body, so it is the discriminator between the two modes.
	code := "boots := journal.Boots()\nfmt.Println(len(boots))"
	app.appendCodeStart(code)

	require.Len(t, app.history, 1)
	require.True(t, app.history[0].Pending, "a code start opens a pending block")

	glyph := strings.TrimSpace(queryPendingGlyph)

	// Collapsed: a pending block shows its header only — no args label, no args body.
	app.toolsExpanded = false
	collapsed := strings.Join(nonBlankLineTexts(app.renderMessage(80, app.history[0])), "\n")
	assert.Contains(t, collapsed, codeName, "a collapsed pending block still shows its header")
	assert.Contains(t, collapsed, glyph, "the pending glyph marks the header")
	assert.NotContains(t, collapsed, labelArgs+":", "a collapsed pending block hides its args section label")
	assert.NotContains(t, collapsed, "fmt.Println", "a collapsed pending block hides its args body")

	// Expanded: the same pending block now reveals its args section so the user can read
	// the in-flight code before it has produced any output.
	app.toolsExpanded = true
	expanded := strings.Join(nonBlankLineTexts(app.renderMessage(80, app.history[0])), "\n")
	assert.Contains(t, expanded, glyph, "the header stays a pending header when expanded")
	assert.Contains(t, expanded, labelArgs+":", "an expanded pending block shows its args section")
	assert.Contains(t, expanded, "fmt.Println(len(boots))", "an expanded pending block reveals the running code")
	assert.Contains(t, expanded, queryCollapseHint, "an expanded pending block shows the collapse hint")
}
