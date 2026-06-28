package terminal

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/tui"
)

// nonBlankLineTexts returns the trimmed text of every rendered line that carries
// visible content, so an assertion can read a message as its sequence of rows.
func nonBlankLineTexts(lines []tui.Line) []string {
	texts := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line.Text); trimmed != "" {
			texts = append(texts, trimmed)
		}
	}

	return texts
}

// TestRenderAssistantMessagePreservesIndentedCodeBlock proves the renderer parses
// the markdown SOURCE untrimmed: a four-space-indented code block stays two code
// lines. Trimming the source (as the old code did) would strip the first line's
// indent and collapse the block into one lazily-continued paragraph,
// "line one line two".
func TestRenderAssistantMessagePreservesIndentedCodeBlock(t *testing.T) {
	t.Parallel()

	app := newApp(newFakeScreen(80, 24), RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})

	content := "    line one\n    line two"

	texts := nonBlankLineTexts(app.renderAssistantMessage(80, content))

	require.Equal(t, []string{"line one", "line two"}, texts,
		"the indented code block survives as two separate lines instead of one merged paragraph")
}

// TestRenderThinkingMessagePreservesIndentedCodeBlock proves the same
// source-untrimmed parsing for reasoning turns, which share the markdown path
// under a bold "thinking" label.
func TestRenderThinkingMessagePreservesIndentedCodeBlock(t *testing.T) {
	t.Parallel()

	app := newApp(newFakeScreen(80, 24), RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})

	content := "    line one\n    line two"

	texts := nonBlankLineTexts(app.renderThinkingMessage(80, content))

	require.Contains(t, texts, thinkingLabel, "the reasoning turn keeps its thinking label")
	require.Subset(t, texts, []string{"line one", "line two"},
		"the indented code block survives as two separate lines under the thinking label")
	require.NotContains(t, texts, "line one line two",
		"the source stays untrimmed, so the code block never collapses into a merged paragraph")
}

// TestRenderAssistantMessageTrimsRenderedBlankRows proves leading and trailing
// blank rows are trimmed from the rendered output (not the source), so the
// message's own spacer rows remain the only padding even when the markdown body
// renders surrounding blank lines.
func TestRenderAssistantMessageTrimsRenderedBlankRows(t *testing.T) {
	t.Parallel()

	app := newApp(newFakeScreen(80, 24), RunOptions{Trace: nil, Controller: nil, Title: defaultTitle})

	// Leading and trailing blank source lines plus a body line; the rendered body
	// must not gain extra blank rows beyond the two surrounding spacers.
	rendered := app.renderAssistantMessage(80, "\n\nhello world\n\n")

	require.GreaterOrEqual(t, len(rendered), 3, "a message keeps a spacer above and below its body")
	require.Empty(t, strings.TrimSpace(rendered[0].Text), "the first row is the leading spacer")
	require.Empty(t, strings.TrimSpace(rendered[len(rendered)-1].Text), "the last row is the trailing spacer")
	require.Equal(t, []string{"hello world"}, nonBlankLineTexts(rendered),
		"the body renders as a single content row with no stray blank rows")
}
