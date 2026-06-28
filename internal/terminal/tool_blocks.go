package terminal

import (
	"strings"

	"github.com/gdamore/tcell/v3"

	"github.com/omarluq/anamnesis/internal/tui"
)

const (
	// Section headers in the transcript tool-event wire format.
	sectionTool      = "tool"
	sectionArguments = "arguments"
	sectionOutput    = "output"
	sectionError     = "error"

	// queryExpandHint and queryCollapseHint label the ctrl+o toggle in collapsed
	// and expanded query blocks.
	queryExpandHint   = "press ctrl+o to expand"
	queryCollapseHint = "press ctrl+o to collapse"
	// queryPendingGlyph marks an in-flight query block's header.
	queryPendingGlyph = "◌ "
	// labelArgs and labelOutput head the expanded query block's sections.
	labelArgs   = "args"
	labelOutput = "output"
)

// parsedQuery is the decoded view of a query block's wire content.
type parsedQuery struct {
	Name   string
	Args   string
	Output string
	Error  string
}

// renderQueryBlock renders a recursive agent.Query sub-call as a color-coded box in one
// of three modes — pending, collapsed, or expanded. Every block is laid out at the same
// left margin regardless of recursion depth, so the transcript reads as a flat sequence
// of blocks rather than a nested tree.
func (app *App) renderQueryBlock(width int, message chatMessage) []tui.Line {
	parsed := parseQueryContent(message.Content)
	style := queryBlockStyle(app.theme, message, parsed)

	return app.renderQueryMode(width, message, parsed, style)
}

// renderQueryMode selects the block layout for the query's current state: a pending
// sub-call (header only, or header plus its args when the user has expanded blocks),
// the settled expanded args/output view, or the collapsed preview.
func (app *App) renderQueryMode(
	width int,
	message chatMessage,
	parsed parsedQuery,
	style tcell.Style,
) []tui.Line {
	switch {
	case message.Pending && app.toolsExpanded:
		return app.renderExpandedPendingQuery(width, parsed, style)
	case message.Pending:
		return renderPendingQuery(width, parsed, style)
	case app.toolsExpanded:
		return app.renderExpandedQuery(width, parsed, style)
	default:
		return app.renderCollapsedQuery(width, parsed, style)
	}
}

// renderPendingQuery renders a collapsed in-flight query block: the box and its
// header only.
func renderPendingQuery(width int, parsed parsedQuery, style tcell.Style) []tui.Line {
	lines := queryBlockStart(width, style)
	lines = append(lines, queryHeaderLines(width, parsed, true, style)...)
	lines = append(lines, queryBlockEnd(width, style)...)

	return lines
}

// renderExpandedPendingQuery renders an in-flight query block in full while it is
// still running: the pending header, the collapse hint, and the args section. The
// block has not settled, so there is no output yet to show — only the args — which
// lets the user read the running code or sub-call prompt instead of just the
// one-line header.
func (app *App) renderExpandedPendingQuery(width int, parsed parsedQuery, style tcell.Style) []tui.Line {
	lines := make([]tui.Line, 0, initialQueryBlockLines)
	lines = append(lines, queryBlockStart(width, style)...)
	lines = append(lines, queryHeaderLines(width, parsed, true, style)...)
	lines = append(lines, paddedQueryLine(width, queryCollapseHint, style.Foreground(app.theme.Muted)))
	lines = append(lines, querySectionLines(width, labelArgs, parsed.Args, style)...)
	lines = append(lines, queryBlockEnd(width, style)...)

	return lines
}

// renderCollapsedQuery renders a settled query block as its header plus the last
// few output lines, eliding any earlier output behind the expand hint.
func (app *App) renderCollapsedQuery(width int, parsed parsedQuery, style tcell.Style) []tui.Line {
	lines := queryBlockStart(width, style)
	lines = append(lines, queryHeaderLines(width, parsed, false, style)...)

	preview, hidden := tailQueryLines(width, queryOutput(parsed), style, maxCollapsedQueryOutputLines)
	if hidden > 0 {
		hint := hiddenQueryLinesText(hidden, queryExpandHint)
		lines = append(lines, paddedQueryLine(width, hint, style.Foreground(app.theme.Muted)))
	}

	lines = append(lines, preview...)
	lines = append(lines, queryBlockEnd(width, style)...)

	return lines
}

// renderExpandedQuery renders a settled query block in full: header, collapse
// hint, then the args and output sections.
func (app *App) renderExpandedQuery(width int, parsed parsedQuery, style tcell.Style) []tui.Line {
	lines := make([]tui.Line, 0, initialQueryBlockLines)
	lines = append(lines, queryBlockStart(width, style)...)
	lines = append(lines, queryHeaderLines(width, parsed, false, style)...)
	lines = append(lines, paddedQueryLine(width, queryCollapseHint, style.Foreground(app.theme.Muted)))
	lines = append(lines, querySectionLines(width, labelArgs, parsed.Args, style)...)
	lines = append(lines, querySectionLines(width, labelOutput, queryOutput(parsed), style)...)
	lines = append(lines, queryBlockEnd(width, style)...)

	return lines
}

// queryBlockStyle picks the box background from the block's state: pending, a
// judge critique asking for a revision (amber — a critique is a revision directive,
// not a failure), errored (red), or successfully completed (green). An approving
// judge block carries the standing approved output, so it falls through to the
// green success branch alongside a settled query.
func queryBlockStyle(theme Theme, message chatMessage, parsed parsedQuery) tcell.Style {
	switch {
	case message.Pending:
		return theme.bg(theme.ToolPendingBg)
	case parsed.Name == judgeName && parsed.Output != judgeApprovedOutput:
		return theme.bg(theme.ToolReviseBg)
	case parsed.Error != "":
		return theme.bg(theme.ToolErrorBg)
	default:
		return theme.bg(theme.ToolSuccessBg)
	}
}

// queryHeaderLines renders the bold block header: the query name, an args summary,
// and a leading glyph while the sub-call is still pending.
func queryHeaderLines(width int, parsed parsedQuery, pending bool, style tcell.Style) []tui.Line {
	title := parsed.Name
	if summary := querySummary(parsed.Args); summary != "" {
		title += "  " + summary
	}

	if pending {
		title = queryPendingGlyph + title
	}

	return queryContentLines(width, title, style.Bold(true))
}

// querySectionLines renders a labeled section (args or output) of an expanded
// block, or nothing when the section is empty.
func querySectionLines(width int, label, content string, style tcell.Style) []tui.Line {
	contentLines := queryContentLines(width, content, style)
	if len(contentLines) == 0 {
		return nil
	}

	lines := make([]tui.Line, 0, len(contentLines)+1)
	lines = append(lines, paddedQueryLine(width, label+":", style.Bold(true)))
	lines = append(lines, contentLines...)

	return lines
}

// queryBlockStart opens a block with a blank separator and a painted top edge.
func queryBlockStart(width int, style tcell.Style) []tui.Line {
	return []tui.Line{
		tui.NewLine(tcell.StyleDefault, ""),
		paddedQueryLine(width, "", style),
	}
}

// queryBlockEnd closes a block with a painted bottom edge and a blank separator.
func queryBlockEnd(width int, style tcell.Style) []tui.Line {
	return []tui.Line{
		paddedQueryLine(width, "", style),
		tui.NewLine(tcell.StyleDefault, ""),
	}
}

// queryContentLines wraps content into painted, padded block lines, one logical
// line at a time, or nothing when content is blank.
func queryContentLines(width int, content string, style tcell.Style) []tui.Line {
	if strings.TrimSpace(content) == "" {
		return nil
	}

	contentWidth := queryContentWidth(width)
	lines := make([]tui.Line, 0, 1)

	for line := range strings.SplitSeq(content, "\n") {
		for _, wrapped := range tui.Wrap(line, contentWidth) {
			lines = append(lines, paddedQueryLine(width, wrapped, style))
		}
	}

	return lines
}

// tailQueryLines returns the last limit content lines of output and how many
// earlier lines were elided.
func tailQueryLines(width int, content string, style tcell.Style, limit int) (tail []tui.Line, hidden int) {
	lines := queryContentLines(width, content, style)
	if limit <= 0 || len(lines) <= limit {
		return lines, 0
	}

	hiddenLines := len(lines) - limit

	return lines[hiddenLines:], hiddenLines
}

// paddedQueryLine paints one block line: a gutter, the width-padded content, and a
// trailing gutter, all in style.
func paddedQueryLine(width int, content string, style tcell.Style) tui.Line {
	gutter := strings.Repeat(" ", messageHorizontalPadding)

	return tui.NewLine(style, gutter+tui.PadRight(content, queryContentWidth(width))+gutter)
}

// queryContentWidth is the writable width inside a block's gutters.
func queryContentWidth(width int) int {
	return max(1, width-messageBoxHorizontalPadding)
}

// queryOutput is the block's displayable output: the result, prefixed with the
// error message when the sub-call failed.
func queryOutput(parsed parsedQuery) string {
	output := strings.Trim(parsed.Output, "\n")
	if parsed.Error != "" {
		output = strings.Trim(parsed.Error+"\n"+output, "\n")
	}

	return output
}

// querySummary is the first line of the prompt, shown beside the block header.
func querySummary(args string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(args), "\n")

	return first
}

// hiddenQueryLinesText is the elision notice shown above a collapsed block's
// output preview.
func hiddenQueryLinesText(hidden int, hint string) string {
	unit := "lines"
	if hidden == 1 {
		unit = "line"
	}

	text := "… " + tui.Int(hidden) + " earlier output " + unit + " hidden"
	if hint != "" {
		text += "  " + hint
	}

	return text
}

// parseQueryContent decodes a query block's wire content into its name, prompt,
// output, and any error.
func parseQueryContent(content string) parsedQuery {
	parsed := parsedQuery{Name: queryName, Args: "", Output: "", Error: ""}
	sections := map[string][]string{}
	current := ""

	for line := range strings.SplitSeq(content, "\n") {
		// output is the terminal free-text section — a sub-answer that may itself
		// contain lines that look like "output:"/"error:"/"tool:" markers — so once
		// it opens, every remaining line is output content, not a new section header.
		if current == sectionOutput {
			sections[sectionOutput] = append(sections[sectionOutput], line)

			continue
		}

		name, value, ok := parseQuerySectionHeader(line)
		if !ok {
			if current != "" {
				sections[current] = append(sections[current], line)
			}

			continue
		}

		current = collectQuerySection(&parsed, sections, name, value)
	}

	parsed.Args = strings.TrimSpace(strings.Join(sections[sectionArguments], "\n"))
	parsed.Output = strings.Trim(strings.Join(sections[sectionOutput], "\n"), "\n")
	parsed.Error = strings.Trim(strings.Join(sections[sectionError], "\n"), "\n")

	return parsed
}

// collectQuerySection records a parsed section header — assigning the tool name or
// opening a content section — and returns the section subsequent lines belong to.
func collectQuerySection(parsed *parsedQuery, sections map[string][]string, name, value string) string {
	if name == sectionTool {
		parsed.Name = value

		return ""
	}

	if value != "" {
		sections[name] = append(sections[name], value)
	}

	return name
}

// parseQuerySectionHeader reports whether line opens a known section, returning the
// section name and any inline value.
func parseQuerySectionHeader(line string) (name, value string, ok bool) {
	left, right, found := strings.Cut(line, ":")
	if !found {
		return "", "", false
	}

	name = strings.TrimSpace(left)
	switch name {
	case sectionTool, sectionArguments, sectionOutput, sectionError:
		return name, strings.TrimSpace(right), true
	default:
		return "", "", false
	}
}
