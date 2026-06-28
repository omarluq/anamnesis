package terminal

import (
	"slices"
	"strings"

	"github.com/omarluq/anamnesis/internal/transcript"
)

const (
	// welcomeText is shown in the empty transcript before the first turn.
	welcomeText = "Type a message and press Enter to begin."
	// queryName labels every recursive sub-call block as the agent.Query primitive.
	queryName = "agent.Query"
	// codeName labels every per-turn code-evaluation block as the Go the controller
	// ran in the embedded interpreter.
	codeName = "code"
	// thinkingLabel heads a thinking block (always shown in full).
	thinkingLabel = "thinking"
)

// chatMessage is one entry in the scrolling transcript: a role plus its rendered
// content. Query blocks (RoleToolResult) and per-turn code blocks
// (RoleBashExecution) additionally carry their recursion Depth for indentation and a
// Pending flag toggled between a start event (TraceKindQueryStart / TraceKindCodeStart)
// and its matching end (TraceKindQueryEnd / TraceKindCodeEnd).
type chatMessage struct {
	Role    transcript.Role
	Content string
	Depth   int
	Pending bool
}

// newChatMessage builds a settled, top-level message of role carrying content.
func newChatMessage(role transcript.Role, content string) chatMessage {
	return chatMessage{Role: role, Content: content, Depth: 0, Pending: false}
}

// appendUser appends the user's submitted prompt as a user message and returns
// the trimmed text, or the empty string when the submission was only whitespace.
func (app *App) appendUser(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}

	app.history = append(app.history, newChatMessage(transcript.RoleUser, trimmed))

	return trimmed
}

// appendAssistant appends a controller's FINAL answer as assistant markdown,
// ignoring a blank answer so an empty FINAL leaves the transcript untouched.
func (app *App) appendAssistant(markdown string) {
	trimmed := strings.TrimSpace(markdown)
	if trimmed == "" {
		return
	}

	app.history = append(app.history, newChatMessage(transcript.RoleAssistant, trimmed))
}

// appendThinking appends a reasoning turn as a thinking message, ignoring blank
// reasoning text.
func (app *App) appendThinking(text string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}

	app.history = append(app.history, newChatMessage(transcript.RoleThinking, trimmed))
}

// appendThinkingDelta grows the turn's live thinking block with one streamed
// reasoning chunk: it appends delta to the open pending thinking block, or opens a
// fresh pending block when none is open (the first delta of a turn). The streamed
// deltas arrive contiguously, so the open block is always the most recent message.
func (app *App) appendThinkingDelta(delta string) {
	if last := len(app.history) - 1; last >= 0 &&
		app.history[last].Role == transcript.RoleThinking && app.history[last].Pending {
		app.history[last].Content += delta

		return
	}

	app.history = append(app.history, chatMessage{
		Role:    transcript.RoleThinking,
		Content: delta,
		Depth:   0,
		Pending: true,
	})
}

// settleThinking finalizes the turn's thinking block. When reasoning streamed live a
// pending block is already open, so this replaces its text with the authoritative
// summary and settles it — no duplicate block. When nothing streamed (the model
// returned no reasoning summary) it appends text as a fresh thinking block, ignoring
// blank text exactly as appendThinking does.
func (app *App) settleThinking(text string) {
	last := len(app.history) - 1
	if last < 0 || app.history[last].Role != transcript.RoleThinking || !app.history[last].Pending {
		app.appendThinking(text)

		return
	}

	if settled := strings.TrimSpace(text); settled != "" {
		app.history[last].Content = settled
	}

	app.history[last].Pending = false
}

// appendQueryStart opens a pending query block for a recursive agent.Query
// sub-call carrying prompt at the given recursion depth.
func (app *App) appendQueryStart(prompt string, depth int) {
	app.history = append(app.history, chatMessage{
		Role:    transcript.RoleToolResult,
		Content: queryContent(prompt, ""),
		Depth:   depth,
		Pending: true,
	})
}

// completeQuery fills the most recent pending query block at depth with result,
// settling it. A QueryEnd with no matching open block is ignored so a stray end
// event cannot corrupt the transcript.
func (app *App) completeQuery(result string, depth int) {
	for index, message := range slices.Backward(app.history) {
		if !message.Pending || message.Role != transcript.RoleToolResult || message.Depth != depth {
			continue
		}

		parsed := parseQueryContent(message.Content)
		app.history[index] = chatMessage{
			Role:    transcript.RoleToolResult,
			Content: queryContent(parsed.Args, result),
			Depth:   depth,
			Pending: false,
		}

		return
	}
}

// queryContent renders a query block's prompt and result into the transcript's
// tool-event wire format, reusing the shared transcript formatter so the on-screen
// block and any persisted transcript stay in lockstep.
func queryContent(prompt, result string) string {
	return transcript.FormatToolEventDisplay(&transcript.ToolEvent{
		Name:          queryName,
		ArgumentsJSON: prompt,
		DetailsJSON:   "",
		Result:        result,
		Error:         "",
		IsError:       false,
	})
}

// appendCodeStart opens a pending code-execution block carrying the turn's Go
// source, settled by the matching completeCode once the interpreter returns. The
// block sits at top level: the recursion structure is carried by the query blocks a
// turn's agent.Query calls open, not by the code blocks themselves.
func (app *App) appendCodeStart(code string) {
	app.history = append(app.history, chatMessage{
		Role:    transcript.RoleBashExecution,
		Content: codeContent(code, "", ""),
		Depth:   0,
		Pending: true,
	})
}

// completeCode fills the most recent pending code block with output, settling it. A
// non-empty errText routes into the block's error: section so the block renders red;
// a CodeEnd with no matching open block is ignored so a stray end event cannot
// corrupt the transcript.
func (app *App) completeCode(output, errText string) {
	for index, message := range slices.Backward(app.history) {
		if !message.Pending || message.Role != transcript.RoleBashExecution {
			continue
		}

		parsed := parseQueryContent(message.Content)
		app.history[index] = chatMessage{
			Role:    transcript.RoleBashExecution,
			Content: codeContent(parsed.Args, output, errText),
			Depth:   0,
			Pending: false,
		}

		return
	}
}

// codeContent renders a code block's Go source, captured output, and any error into
// the transcript's tool-event wire format, reusing the shared transcript formatter so
// a code block parses and renders through the same path as a query block. A non-empty
// errText fills the error: section, which paints the settled block red.
func codeContent(code, output, errText string) string {
	return transcript.FormatToolEventDisplay(&transcript.ToolEvent{
		Name:          codeName,
		ArgumentsJSON: code,
		DetailsJSON:   "",
		Result:        output,
		Error:         errText,
		IsError:       errText != "",
	})
}
