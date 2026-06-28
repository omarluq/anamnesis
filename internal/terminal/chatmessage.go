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
	// thinkingLabel heads an expanded thinking block.
	thinkingLabel = "thinking"
	// thinkingCollapsed is the one-line stand-in for a collapsed thinking block.
	thinkingCollapsed = "thinking…"
)

// chatMessage is one entry in the scrolling transcript: a role plus its rendered
// content. Query blocks (RoleToolResult) additionally carry their recursion Depth
// for indentation and a Pending flag toggled between a TraceKindQueryStart and its
// matching TraceKindQueryEnd.
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
