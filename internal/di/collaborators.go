package di

import (
	"context"

	"github.com/samber/do/v2"
	"github.com/samber/lo"

	"github.com/omarluq/anamnesis/internal/ana/rlm"
	"github.com/omarluq/anamnesis/internal/openai"
)

// newOpenAIClient constructs the OpenAI API client the three RLM collaborators
// issue their controller, sub-LLM, and judge calls through. The API key is read
// from the environment (openai.NewClient's OPENAI_API_KEY lookup); construction
// issues no network call. The provider is lazy, so the container assembles and the
// collaborators resolve with no live call and no key present — the gpt-5.5 path
// stays gated behind the key, which a missing value fails on the first model call
// rather than at container assembly.
func newOpenAIClient(_ do.Injector) (*openai.Client, error) {
	return openai.NewClient()
}

// clientResolver lazily resolves the shared *openai.Client on demand. The three
// collaborator adapters hold one of these rather than the client itself, so
// resolving a collaborator never forces the client to be built — and so never
// needs the API key — while the first model call materializes and caches the
// client behind the injector.
type clientResolver func() (*openai.Client, error)

// newClientResolver binds injector into a clientResolver that invokes the
// registered *openai.Client lazily. do caches the client after the first
// successful build, so every collaborator shares one handle.
func newClientResolver(injector do.Injector) clientResolver {
	return func() (*openai.Client, error) {
		return do.Invoke[*openai.Client](injector)
	}
}

// controllerAdapter adapts the OpenAI controller call to the rlm.ControllerLLM
// seam the SPEC §6 turn loop drives. It frames the question and rendered
// transcript into the §6 controller input — the system prompt crosses as the
// call's instructions — and returns the decoded ControllerResponse, dropping the
// token usage the seam does not carry.
type controllerAdapter struct {
	// resolve lazily resolves the shared OpenAI client on the first turn.
	resolve clientResolver
}

// newControllerAdapter binds injector into the rlm.ControllerLLM seam, resolving
// the OpenAI client lazily so registering the collaborator never needs the key.
func newControllerAdapter(injector do.Injector) (rlm.ControllerLLM, error) {
	return &controllerAdapter{resolve: newClientResolver(injector)}, nil
}

// compile-time assertion that controllerAdapter satisfies the rlm controller seam.
var _ rlm.ControllerLLM = (*controllerAdapter)(nil)

// Respond resolves the shared client and issues one controller turn: systemPrompt
// crosses as the call's instructions and question and history are framed into the
// §6 input. It returns the decoded ControllerResponse, surfacing a missing client
// or a failed model call as the error the chat adapter renders as a failed run.
func (adapter *controllerAdapter) Respond(
	ctx context.Context,
	systemPrompt, question, history string,
) (openai.ControllerResponse, error) {
	client, err := adapter.resolve()
	if err != nil {
		return openai.ControllerResponse{Thinking: "", Code: "", Done: false}, err
	}

	result, err := client.Controller(ctx, systemPrompt, controllerInput(question, history))
	if err != nil {
		return openai.ControllerResponse{Thinking: "", Code: "", Done: false}, err
	}

	return result.Response, nil
}

// controllerInput frames the original question and the rendered turn history into
// the §6 controller input the model reads under the system-prompt instructions: a
// USER line carrying the question, followed by the rendered transcript once prior
// turns exist. The empty history of the first turn yields the question alone.
func controllerInput(question, history string) string {
	if history == "" {
		return "USER: " + question
	}

	return "USER: " + question + "\n\n" + history
}

// subAdapter adapts the OpenAI sub-call to the rlm.SubLLM seam agent.Query drives.
// It returns the model's trimmed reply text, dropping the token usage the seam
// does not carry.
type subAdapter struct {
	// resolve lazily resolves the shared OpenAI client on the first sub-call.
	resolve clientResolver
}

// newSubAdapter binds injector into the rlm.SubLLM seam, resolving the OpenAI
// client lazily so registering the collaborator never needs the key.
func newSubAdapter(injector do.Injector) (rlm.SubLLM, error) {
	return &subAdapter{resolve: newClientResolver(injector)}, nil
}

// compile-time assertion that subAdapter satisfies the rlm sub-LLM seam.
var _ rlm.SubLLM = (*subAdapter)(nil)

// Answer resolves the shared client and issues one bounded sub-call over the
// rendered evidence, returning the model's reply text. A missing client or a
// failed model call surfaces as the error agent.Query turns into a turn fault.
func (adapter *subAdapter) Answer(ctx context.Context, prompt, evidence string) (string, error) {
	client, err := adapter.resolve()
	if err != nil {
		return "", err
	}

	result, err := client.Sub(ctx, prompt, evidence)
	if err != nil {
		return "", err
	}

	return result.Text, nil
}

// judgeAdapter adapts the OpenAI judge pass to the rlm.Judger seam the §5 audit
// gate drives. The rlm seam carries the cited grounding as one rendered string and
// reads an empty critique as approval, so the adapter wraps that string as the
// judge's single cited block and collapses an approving verdict to the empty
// critique, dropping the token usage the seam does not carry.
type judgeAdapter struct {
	// resolve lazily resolves the shared OpenAI client on the audit pass.
	resolve clientResolver
}

// newJudgeAdapter binds injector into the rlm.Judger seam, resolving the OpenAI
// client lazily so registering the collaborator never needs the key.
func newJudgeAdapter(injector do.Injector) (rlm.Judger, error) {
	return &judgeAdapter{resolve: newClientResolver(injector)}, nil
}

// compile-time assertion that judgeAdapter satisfies the rlm judge seam.
var _ rlm.Judger = (*judgeAdapter)(nil)

// Judge resolves the shared client and runs the one-shot audit over the answer,
// wrapping the rendered cited transcript as the judge's single cited block — an
// empty cited string ships no block, so the judge sees its explicit no-grounding
// marker. It returns the empty critique on approval and the model's critique
// otherwise, matching the seam's empty-means-approve contract.
func (adapter *judgeAdapter) Judge(ctx context.Context, question, answer, cited string) (string, error) {
	client, err := adapter.resolve()
	if err != nil {
		return "", err
	}

	result, err := client.Judge(ctx, question, answer, citedBlocks(cited))
	if err != nil {
		return "", err
	}

	if result.Verdict.Approve {
		return "", nil
	}

	return result.Verdict.Critique, nil
}

// citedBlocks renders the rlm seam's single cited string as the judge's cited
// entries, yielding an empty slice when nothing was cited so the judge input falls
// back to its explicit no-grounding marker rather than a blank block.
func citedBlocks(cited string) []string {
	return lo.Filter([]string{cited}, func(entry string, _ int) bool {
		return entry != ""
	})
}
