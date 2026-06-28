package rlm

import (
	"context"

	"github.com/omarluq/anamnesis/internal/ana/citations"
	"github.com/omarluq/anamnesis/internal/openai"
)

// ControllerLLM is the controller-model seam the session loop drives once per
// turn. The rlm package owns this interface; the openai controller layer
// satisfies it structurally, so the loop depends on a narrow contract rather
// than on the concrete OpenAI client.
type ControllerLLM interface {
	// Respond returns the structured controller reply for the next turn, given
	// the controller system prompt, the original user question, and the rendered
	// transcript of prior turns. onReasoning receives the model's reasoning-summary
	// deltas as they stream so the caller can render thinking live; it may be nil to
	// ignore them. It returns an error when the model call fails.
	Respond(
		ctx context.Context,
		systemPrompt, question, history string,
		onReasoning func(string),
	) (openai.ControllerResponse, error)
}

// SubLLM is the recursive sub-call seam agent.Query drives. The rlm package owns
// this interface; the openai sub-LLM layer satisfies it structurally. Each call
// answers one bounded prompt against one rendered evidence context and counts as
// a single sub-call against the budget.
type SubLLM interface {
	// Answer returns the sub-LLM's terse response to prompt, reasoning only over
	// the rendered evidence context. It returns an error when the model call
	// fails.
	Answer(ctx context.Context, prompt, evidence string) (string, error)
}

// Judger is the audit seam that reviews a finished investigation once, after the
// controller calls agent.FINAL but before the answer renders. The rlm package
// owns this interface; the openai judge layer satisfies it structurally.
type Judger interface {
	// Judge reviews the final answer to question against the rendered cited
	// entries, returning an empty critique to approve the answer or a short
	// critique describing what to fix. It returns an error when the model call
	// fails.
	Judge(ctx context.Context, question, answer, cited string) (string, error)
}

// Session is the controller spine for one investigation: the three model
// collaborators the loop drives plus the budget, citation store, trace emitter,
// turn history, and the prompts that frame every controller call. It is a plain
// aggregate the controller loop operates on; the zero value is not usable
// because every collaborator must be wired before a run begins.
type Session struct {
	// Controller produces the next ControllerResponse on every turn.
	Controller ControllerLLM
	// Sub answers the recursive sub-calls agent.Query fans out.
	Sub SubLLM
	// Judge audits the final answer once before it renders.
	Judge Judger
	// Budget enforces the turn, depth, and sub-call hard limits in code.
	Budget *Budget
	// Store tracks session-visible cursors and validates the final citations.
	Store *citations.Store
	// Emitter publishes this run's trace events onto the shell's trace channel.
	Emitter *Emitter
	// Question is the original user question the investigation answers.
	Question string
	// SystemPrompt is the controller system prompt every turn is framed with.
	SystemPrompt string
	// History is the ordered record of controller turns observed so far.
	History []ControllerTurn
}
