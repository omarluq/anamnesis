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
	// interruptedNote settles a tool block whose run ended before its matching end
	// event arrived, so a force-finished run leaves no block spinning forever.
	interruptedNote = "interrupted — the run ended before this step finished"
)

// chatMessage is one entry in the scrolling transcript: a role plus its rendered
// content. Query blocks (RoleToolResult) and per-turn code blocks (RoleBashExecution)
// carry a Pending flag toggled between a start event (TraceKindQueryStart /
// TraceKindCodeStart) and its matching end (TraceKindQueryEnd / TraceKindCodeEnd). A
// query block also carries the QueryID its start event minted, so completeQuery settles
// an end onto its own start even when parallel fan-out completes out of order; non-query
// blocks leave it 0. A query or code block also keeps its raw Args — the agent.Query
// prompt or the turn's Go source — so completeQuery and completeCode re-render the
// settled block from that stored payload instead of parsing it back out of the
// already-rendered Content, which is ambiguous when the prompt or code contains a line
// that looks like a wire-format section marker (output:, error:, details:); other blocks
// leave it empty.
type chatMessage struct {
	Role    transcript.Role
	Content string
	Args    string
	QueryID uint64
	Pending bool
}

// newChatMessage builds a settled, top-level message of role carrying content.
func newChatMessage(role transcript.Role, content string) chatMessage {
	return chatMessage{Role: role, Content: content, Args: "", QueryID: 0, Pending: false}
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

// maxPriorExchanges caps how many recent question/answer pairs the follow-up
// preamble carries, and maxPriorAnswerRunes truncates each prior question and answer,
// so a long session can never grow the preamble without bound.
const (
	maxPriorExchanges   = 6
	maxPriorAnswerRunes = 800
)

// priorExchange is one completed question/answer pair from earlier in the session.
type priorExchange struct {
	question string
	answer   string
}

// priorConversation renders the completed question/answer pairs in history as a
// short "Earlier in this session" preamble the next investigation folds ahead of
// its question, so a follow-up can resolve a reference like "that ssh issue"
// without the run carrying any interpreter or transcript state forward. It pairs
// each user message with the assistant answer that follows it, keeps only the last
// maxPriorExchanges pairs, truncates each answer to maxPriorAnswerRunes on a rune
// boundary, and returns the empty string when no exchange has completed yet — the
// first question of a session, or one still in flight. Only distilled answers cross
// forward, never the raw per-turn REPL output the loop keeps out of context.
func priorConversation(history []chatMessage) string {
	var (
		exchanges       []priorExchange
		pendingQuestion string
		havePending     bool
	)

	for _, message := range history {
		switch {
		case message.Role == transcript.RoleUser:
			pendingQuestion = message.Content
			havePending = true
		case message.Role == transcript.RoleAssistant && havePending:
			exchanges = append(exchanges, priorExchange{question: pendingQuestion, answer: message.Content})
			havePending = false
		}
	}

	if len(exchanges) == 0 {
		return ""
	}

	if len(exchanges) > maxPriorExchanges {
		exchanges = exchanges[len(exchanges)-maxPriorExchanges:]
	}

	var builder strings.Builder

	builder.WriteString("Earlier in this session:\n")

	for _, exchange := range exchanges {
		builder.WriteString("\nQ: ")
		builder.WriteString(truncateRunes(exchange.question, maxPriorAnswerRunes))
		builder.WriteString("\nA: ")
		builder.WriteString(truncateRunes(exchange.answer, maxPriorAnswerRunes))
		builder.WriteString("\n")
	}

	return builder.String()
}

// truncateRunes returns text unchanged when it fits within limit runes, else its
// first limit runes with an ellipsis, cutting on a rune boundary so a multi-byte
// character is never split.
func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}

	return string(runes[:limit]) + "…"
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
		Args:    "",
		QueryID: 0,
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

// appendQueryStart opens a pending query block for recursive agent.Query sub-call
// queryID, carrying prompt. The id is stored so completeQuery settles this block's own
// end rather than the newest pending block, even when parallel fan-out completes out of
// order.
func (app *App) appendQueryStart(queryID uint64, prompt string) {
	app.history = append(app.history, chatMessage{
		Role:    transcript.RoleToolResult,
		Content: queryContent(prompt, ""),
		Args:    prompt,
		QueryID: queryID,
		Pending: true,
	})
}

// completeQuery fills the pending query block carrying queryID with result, settling
// it. Matching by QueryID — not by recency — pairs each end with its own start even when
// parallel fan-out completes out of order. A QueryEnd with no matching open block is
// ignored so a stray end event cannot corrupt the transcript.
func (app *App) completeQuery(queryID uint64, result string) {
	for index, message := range slices.Backward(app.history) {
		if !message.Pending || message.Role != transcript.RoleToolResult || message.QueryID != queryID {
			continue
		}

		app.history[index] = chatMessage{
			Role:    transcript.RoleToolResult,
			Content: queryContent(message.Args, result),
			Args:    message.Args,
			QueryID: queryID,
			Pending: false,
		}

		return
	}
}

// queryContent renders a query block's prompt and result into the transcript's
// tool-event wire format, reusing the shared transcript formatter so the on-screen
// block and any persisted transcript stay in lockstep.
func queryContent(prompt, result string) string {
	return toolEventContent(queryName, prompt, result)
}

// toolEventContent renders a named tool block's args and result into the
// transcript's tool-event wire format, the shared formatter query blocks
// round-trip through so the on-screen block and any persisted transcript stay
// in lockstep.
func toolEventContent(name, args, result string) string {
	return transcript.FormatToolEventDisplay(&transcript.ToolEvent{
		Name:          name,
		ArgumentsJSON: args,
		DetailsJSON:   "",
		Result:        result,
		Error:         "",
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
		Args:    code,
		QueryID: 0,
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

		app.history[index] = chatMessage{
			Role:    transcript.RoleBashExecution,
			Content: codeContent(message.Args, output, errText),
			Args:    message.Args,
			QueryID: 0,
			Pending: false,
		}

		return
	}
}

// settlePending finalizes every block still marked pending when a run ends. A
// force-finish — the §6 soft deadline, the wall-clock timeout, or an eval timeout —
// cancels the run while a code or agent.Query block is in flight, so its CodeEnd or
// QueryEnd never reaches the shell: the emitter drops a post-cancel send rather than
// panicking on the teardown-closed trace channel. Without this the dropped end would
// leave the block's pending spinner turning forever; here each open block settles with
// an interrupted note, re-rendered from its stored raw Args so the unfinished prompt or
// code stays intact.
func (app *App) settlePending() {
	for index, message := range app.history {
		if message.Pending {
			app.history[index] = settleInterrupted(message)
		}
	}
}

// settleInterrupted returns message settled with the interrupted note: a code block and
// an agent.Query block re-render from their raw Args so the in-flight payload survives,
// and any non-tool role just clears its pending flag.
func settleInterrupted(message chatMessage) chatMessage {
	settled := message
	settled.Pending = false

	switch message.Role {
	case transcript.RoleBashExecution:
		settled.Content = codeContent(message.Args, interruptedNote, "")
	case transcript.RoleToolResult:
		settled.Content = queryContent(message.Args, interruptedNote)
	case transcript.RoleUser,
		transcript.RoleAssistant,
		transcript.RoleThinking:
		// these roles never carry a pending tool block; clearing Pending settles them.
	}

	return settled
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
	})
}
