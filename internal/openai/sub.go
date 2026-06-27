package openai

import (
	"context"
	"strings"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/samber/oops"
)

// MaxSubEvidenceBytes is the byte ceiling for the evidence a single sub-call
// ships. SPEC §7 requires the ctx value handed to agent.Query to render under
// ~16 KB; Sub truncates oversized evidence to this ceiling before the request
// leaves the process so one sub-call honors that render bound. The ceiling bounds
// bytes, not tokens: 16 KB already exceeds the §6 4000-input-token budget once the
// §15 system prompt and PROMPT/CONTEXT framing are added, so staying within the
// token budget is the caller's job, not this ceiling's.
const MaxSubEvidenceBytes = 16 * 1024

// maxSubOutputTokens caps a sub-call's output at the §6 budget (2000 output
// tokens). Sub-LLM replies are meant to be terse (50-200 words per §15), so the
// ceiling bounds cost without truncating a well-behaved answer.
const maxSubOutputTokens = 2000

// truncationMarker is appended in place of the evidence Sub had to cut, so the
// model can tell its context was shortened rather than silently losing the tail.
const truncationMarker = "\n…[evidence truncated to fit the sub-call input budget]"

// subSystemPrompt is the sub-LLM system prompt from SPEC §15, reproduced
// verbatim. It scopes a sub-call to answer the prompt using only the supplied
// context, to invent nothing, and to refuse when the context is insufficient.
const subSystemPrompt = "" +
	"You are a focused analysis sub-call in a recursive investigation.\n" +
	"\n" +
	"You will receive a prompt and a context (Go value rendered as text). Your job " +
	"is to produce a structured, terse response answering the prompt using ONLY the " +
	"information in the context. Do not invent fields, units, timestamps, or causes " +
	"not present in the context. If the context does not contain enough information, " +
	"say \"context insufficient to answer.\"\n" +
	"\n" +
	"Respond in 50-200 words. No preamble. No closing. No filler. Markdown allowed " +
	"but kept minimal."

// SubResult is one sub-LLM call's outcome: the model's text reply plus the token
// usage the call consumed, so the agent layer can both return the answer string
// and bill the sub-call against the session budget.
type SubResult struct {
	// Text is the sub-LLM's reply with surrounding whitespace trimmed.
	Text string
	// Usage is the input/output token count the API reported for this call.
	Usage Usage
}

// Sub makes one bounded, isolated sub-LLM call backing agent.Query: it sends the
// §15 sub-LLM instructions and an input framing the prompt over the rendered
// evidence, then returns the model's text reply paired with the call's token
// usage. Oversized evidence is truncated to MaxSubEvidenceBytes before the request
// goes out so a single sub-call honors the §7 ~16 KB render bound. The call runs
// on the same flagship Model as the controller — there is no cheaper sub-call
// model — so a key without gpt-5.5 access fails loudly here too.
func (client *Client) Sub(ctx context.Context, prompt, evidence string) (SubResult, error) {
	input := buildSubInput(prompt, evidence)

	resp, err := client.api.Responses.New(ctx, responses.ResponseNewParams{
		Model:           Model,
		Instructions:    openaisdk.String(subSystemPrompt),
		MaxOutputTokens: openaisdk.Int(maxSubOutputTokens),
		Input:           responses.ResponseNewParamsInputUnion{OfString: openaisdk.String(input)},
	})
	if err != nil {
		return failedSubCall(oops.
			In("openai").
			Code("sub_call_failed").
			Wrapf(err, "sub responses call on model %s", Model))
	}

	return SubResult{
		Text:  strings.TrimSpace(resp.OutputText()),
		Usage: usageFrom(&resp.Usage),
	}, nil
}

// buildSubInput renders the sub-call input from the prompt and the evidence,
// truncating the evidence to MaxSubEvidenceBytes first so the rendered input
// honors the §7 ~16 KB render bound. The PROMPT/CONTEXT framing keeps the two
// halves legible to the model without a structured-output schema.
func buildSubInput(prompt, evidence string) string {
	return "PROMPT:\n" + prompt + "\n\nCONTEXT:\n" + truncateEvidence(evidence)
}

// truncateEvidence caps evidence at MaxSubEvidenceBytes, replacing the overflow
// with truncationMarker so the model still sees that its context was cut. The cut
// lands on a valid UTF-8 boundary so the rendered text never carries a split rune.
func truncateEvidence(evidence string) string {
	if len(evidence) <= MaxSubEvidenceBytes {
		return evidence
	}

	keep := MaxSubEvidenceBytes - len(truncationMarker)

	return strings.ToValidUTF8(evidence[:keep], "") + truncationMarker
}

// failedSubCall pairs the zero SubResult with err so Sub's error path returns a
// fully-initialized result without an inline literal. Sub has a single error path
// today; the helper exists for parity with failedControllerTurn and failedJudgePass.
func failedSubCall(err error) (SubResult, error) {
	return SubResult{
		Text:  "",
		Usage: Usage{TokensIn: 0, TokensOut: 0},
	}, err
}
