package terminal

import (
	"strings"

	"github.com/gdamore/tcell/v3"

	"github.com/omarluq/anamnesis/internal/transcript"
	"github.com/omarluq/anamnesis/internal/tui"
)

// markdownRenderMaxHeight is the effectively unbounded row budget the transcript
// renders each markdown message at; the viewport clips the assembled lines later.
const markdownRenderMaxHeight = 1_000_000

// renderMessage dispatches a transcript message to the renderer that owns its
// role, returning the styled lines the viewport stacks. Roles without a bespoke
// renderer fall back to assistant markdown.
func (app *App) renderMessage(width int, message chatMessage) []tui.Line {
	switch message.Role {
	case transcript.RoleUser:
		return app.renderUserMessage(width, message.Content)
	case transcript.RoleAssistant:
		return app.renderAssistantMessage(width, message.Content)
	case transcript.RoleThinking:
		return app.renderThinkingMessage(width, message.Content)
	case transcript.RoleToolResult:
		return app.renderQueryBlock(width, message)
	case transcript.RoleCustom,
		transcript.RoleBashExecution,
		transcript.RoleBranchSummary,
		transcript.RoleCompactionSummary:
		return app.renderAssistantMessage(width, message.Content)
	default:
		return app.renderAssistantMessage(width, message.Content)
	}
}

// renderUserMessage renders content as a full-width box painted with the user
// message background, two-space indented and wrapped, with a blank spacer above
// and below.
func (app *App) renderUserMessage(width int, content string) []tui.Line {
	innerWidth := max(1, width-messageBoxHorizontalPadding)
	wrapped := tui.Wrap(content, innerWidth)
	background := app.theme.bg(app.theme.UserMessageBg)
	lines := make([]tui.Line, 0, len(wrapped)+defaultMessageExtraRows)

	lines = append(lines,
		tui.NewLine(app.theme.fg(app.theme.Dim), ""),
		tui.NewLine(background, tui.PadRight("", width)),
	)

	for _, line := range wrapped {
		text := "  " + tui.PadRight(line, innerWidth) + "  "
		lines = append(lines, tui.NewLine(background, text))
	}

	lines = append(lines,
		tui.NewLine(background, tui.PadRight("", width)),
		tui.NewLine(app.theme.fg(app.theme.Dim), ""),
	)

	return lines
}

// renderAssistantMessage renders content as markdown with a blank spacer above
// and below.
func (app *App) renderAssistantMessage(width int, content string) []tui.Line {
	markdownLines := app.renderMarkdown(strings.TrimSpace(content), width)
	lines := make([]tui.Line, 0, len(markdownLines)+messageOuterRows)

	lines = append(lines, tui.NewLine(app.theme.fg(app.theme.Dim), ""))
	lines = append(lines, markdownLines...)
	lines = append(lines, tui.NewLine(app.theme.fg(app.theme.Dim), ""))

	return lines
}

// renderThinkingMessage renders a reasoning turn. When collapsed it is the single
// dim/italic "thinking…" stand-in; when expanded it is the markdown reasoning with
// the dim/italic overlay merged onto every line under a bold "thinking" label.
func (app *App) renderThinkingMessage(width int, content string) []tui.Line {
	style := app.theme.fg(app.theme.ThinkingText).Italic(true)
	if app.hideThinking {
		return []tui.Line{
			tui.NewLine(tcell.StyleDefault, ""),
			tui.NewLine(style, thinkingCollapsed),
			tui.NewLine(tcell.StyleDefault, ""),
		}
	}

	markdownLines := app.renderMarkdown(strings.TrimSpace(content), width)
	lines := make([]tui.Line, 0, len(markdownLines)+messageMetadataRows)

	lines = append(lines,
		tui.NewLine(tcell.StyleDefault, ""),
		tui.NewLine(style.Bold(true), thinkingLabel),
	)

	for _, line := range markdownLines {
		lines = append(lines, mergeLineStyle(line, style))
	}

	lines = append(lines, tui.NewLine(app.theme.fg(app.theme.Dim), ""))

	return lines
}

// renderMarkdown parses content into styled terminal lines using the shared
// markdown engine and lexer the renderer caches across frames.
func (app *App) renderMarkdown(content string, width int) []tui.Line {
	view := tui.MarkdownView{
		Engine: &app.renderer.Markdown,
		Lexer:  &app.renderer.Lexer,
		Text:   content,
		Styles: app.theme.MarkdownStyles(),
	}

	return view.Render(width, markdownRenderMaxHeight)
}

// mergeLineStyle overlays style onto every span of line, so a thinking block can
// tint syntax-highlighted markdown dim/italic without discarding its structure.
func mergeLineStyle(line tui.Line, style tcell.Style) tui.Line {
	merged := line
	merged.Style = mergeStyles(line.Style, style)

	if len(line.Spans) == 0 {
		return merged
	}

	merged.Spans = make([]tui.Span, len(line.Spans))
	for index, span := range line.Spans {
		merged.Spans[index] = tui.Span{Style: mergeStyles(span.Style, style), Text: span.Text}
	}

	return merged
}

// mergeStyles layers the attribute and color overrides of overlay onto base,
// leaving base untouched where overlay carries no override.
func mergeStyles(base, overlay tcell.Style) tcell.Style {
	merged := base
	if overlay.HasBold() {
		merged = merged.Bold(true)
	}

	if overlay.HasItalic() {
		merged = merged.Italic(true)
	}

	if foreground := overlay.GetForeground(); foreground != tcell.ColorDefault {
		merged = merged.Foreground(foreground)
	}

	if background := overlay.GetBackground(); background != tcell.ColorDefault {
		merged = merged.Background(background)
	}

	return merged
}
