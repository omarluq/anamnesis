package tui_test

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/tui"
)

const headingMarkdown = "# Heading"

func testMarkdownStyles() tui.MarkdownStyles {
	return tui.MarkdownStyles{
		Text:      tcell.StyleDefault,
		Accent:    tcell.StyleDefault,
		Muted:     tcell.StyleDefault,
		Code:      tcell.StyleDefault,
		CodeTheme: testCodeTheme(),
	}
}

func TestMarkdownViewRendersCommonBlocks(t *testing.T) {
	t.Parallel()

	markdown := strings.Join([]string{
		headingMarkdown,
		"",
		"Paragraph with [link](https://example.com) and `code`.",
		"",
		"> quoted text",
		"",
		"- bullet item",
		"- second item",
		"",
		"- [x] done task",
		"- [ ] pending task",
		"",
		"1. ordered item",
		"2. next ordered",
		"",
		"---",
		"",
		"    indented code",
		"",
		"| Name | Count |",
		"| :--- | ----: |",
		"| Alpha | 10 |",
	}, "\n")
	view := &tui.MarkdownView{Text: markdown, Styles: testMarkdownStyles(), Engine: nil, Lexer: nil}
	text := strings.Join(lineTexts(view.Render(40, 100)), "\n")

	tests := []struct {
		name string
		want string
	}{
		{name: "heading", want: headingMarkdown},
		{name: "link label", want: "link"},
		{name: "link destination", want: "(https://example.com)"},
		{name: "inline code", want: "`code`"},
		{name: "blockquote", want: "┃ quoted text"},
		{name: "bullet list", want: "• bullet item"},
		{name: "checked task", want: "☑ done task"},
		{name: "unchecked task", want: "☐ pending task"},
		{name: "ordered list", want: "1. ordered item"},
		{name: "thematic break", want: "────────"},
		{name: "indented code", want: "indented code"},
		{name: "table cell", want: "Alpha"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			require.Contains(t, text, test.want)
		})
	}
}

func TestMarkdownViewDrawsHeadingIntoBuffer(t *testing.T) {
	t.Parallel()

	buffer := tui.NewCellBuffer(40, 2, tcell.StyleDefault)
	view := &tui.MarkdownView{Text: headingMarkdown, Styles: testMarkdownStyles(), Engine: nil, Lexer: nil}
	tui.DrawLines(buffer, testRect(0, 40, 2), view.Render(40, 2))

	require.Equal(t, '#', buffer.Cell(1, 0).Rune)
}

func TestMarkdownCodeBlockWrapsInsteadOfSwallowingSymbols(t *testing.T) {
	t.Parallel()

	markdown := "```go\nfunc Fib(n int) int {\n    if n < 2 {\n        return n\n    }\n}\n```"
	view := tui.MarkdownView{
		Text:   markdown,
		Styles: testMarkdownStyles(),
		Engine: nil,
		Lexer:  nil,
	}

	lines := view.Render(20, 100)
	joined := lineText(lines)

	require.Contains(t, joined, "<")
	require.Contains(t, joined, "2")
	require.Contains(t, joined, "{")
	require.Contains(t, joined, "return n")
	assertNoLineWiderThan(t, lines, 20)
}
