package openai

import (
	"context"
	"fmt"
	"strings"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/samber/lo"
	"github.com/samber/oops"
)

// maxJudgeOutputTokens caps the judge pass's output. The verdict is a small
// structured reply — an approve flag plus at most a 1-3 sentence critique per
// SPEC §16 — so this ceiling bounds cost without ever truncating a well-formed
// verdict. There is no §6 budget row for the judge because it runs once per
// session and emits far less than a controller turn.
const maxJudgeOutputTokens = 500

// judgeSchemaName names the structured-output schema sent to the Responses API
// for the judge pass. It is restricted to the [A-Za-z0-9_-] characters the API
// allows.
const judgeSchemaName = "judge_verdict"

// noCitationsMarker stands in for the cited-entries block when the answer cited
// nothing, so the judge sees an explicit "no grounding" signal rather than an
// empty section it might mistake for a rendering bug. SPEC §16 directs the judge
// to be strict on ungrounded claims, and an answer with zero citations is the
// extreme of that case.
const noCitationsMarker = "(none — the answer cited no journal entries)"

// judgeSystemPrompt is the audit-judge system prompt from SPEC §16, reproduced
// verbatim. It scopes the pass to checking that every factual claim is supported
// by a cited entry, that citations are relevant, that nothing obvious is omitted,
// and that the answer is specific — strict on ungrounded claims, lenient on style.
const judgeSystemPrompt = "" +
	"You are an audit judge reviewing a Linux system investigation.\n" +
	"\n" +
	"You will receive:\n" +
	"1. The original user question.\n" +
	"2. The investigator's final answer (markdown).\n" +
	"3. The list of journal entries the investigator cited.\n" +
	"\n" +
	"Your job is to check:\n" +
	"- Does every factual claim in the answer have at least one supporting cited entry?\n" +
	"- Are the cited entries actually relevant to the claim they support?\n" +
	"- Are there obvious omissions (e.g., the user asked about boot timing, " +
	"the answer covers a different window)?\n" +
	"- Is the answer specific and actionable, or vague?\n" +
	"\n" +
	"Respond in JSON:\n" +
	"\n" +
	"{\n" +
	"  \"approve\": true | false,\n" +
	"  \"critique\": \"\" (empty if approve, else 1-3 sentences explaining what to fix)\n" +
	"}\n" +
	"\n" +
	"Be strict on ungrounded claims. Be lenient on style."

// JudgeVerdict is the structured reply the audit judge returns once per session.
// The judge emits it as JSON constrained by GenerateSchema so the loop can read
// the approve/critique decision without parsing free-form text.
type JudgeVerdict struct {
	// Critique is empty on approval, else a 1-3 sentence explanation of what to fix.
	Critique string `json:"critique" jsonschema:"description=Empty if approve, else 1-3 sentences on what to fix"`
	// Approve is true when every factual claim is grounded in a cited entry.
	Approve bool `json:"approve" jsonschema:"description=True iff every factual claim is supported by a cited entry"`
}

// JudgeResult is the judge pass's outcome: the parsed verdict the model produced,
// so the loop can act on the verdict (render the answer or hand the controller a
// critique to retry).
type JudgeResult struct {
	// Verdict is the decoded approve/critique reply for this pass.
	Verdict JudgeVerdict
}

// Judge runs the post-FINAL audit pass against gpt-5.5: it sends the §16 judge
// instructions and an input framing the original question, the investigator's
// final answer, and the cited journal entries under the JudgeVerdict
// structured-output schema, then decodes the model's JSON reply into a
// JudgeVerdict. It runs on the same flagship Model as the controller and sub-LLM
// — there is no cheaper judge model — so a key without gpt-5.5 access fails
// loudly here too.
func (client *Client) Judge(
	ctx context.Context,
	question, answer string,
	citations []string,
) (JudgeResult, error) {
	format, err := structuredFormat[JudgeVerdict](judgeSchemaName, "judge_schema")
	if err != nil {
		return failedJudgePass(err)
	}

	input := buildJudgeInput(question, answer, citations)

	resp, err := client.api.Responses.New(ctx, responses.ResponseNewParams{
		Model:           Model,
		Instructions:    openaisdk.String(judgeSystemPrompt),
		MaxOutputTokens: openaisdk.Int(maxJudgeOutputTokens),
		Input:           responses.ResponseNewParamsInputUnion{OfString: openaisdk.String(input)},
		Text:            responses.ResponseTextConfigParam{Format: format},
	})
	if err != nil {
		return failedJudgePass(oops.
			In("openai").
			Code("judge_call_failed").
			Wrapf(err, "judge responses call on model %s", Model))
	}

	parsed, err := decodeStructured[JudgeVerdict](resp.OutputText(), "judge_decode")
	if err != nil {
		return failedJudgePass(err)
	}

	return JudgeResult{Verdict: parsed}, nil
}

// buildJudgeInput renders the judge input from the original question, the
// investigator's final answer, and the cited entries, framed under labeled
// sections so the model can tell the three apart without a structured-output
// schema on the input side.
func buildJudgeInput(question, answer string, citations []string) string {
	return "USER QUESTION:\n" + question +
		"\n\nFINAL ANSWER:\n" + answer +
		"\n\nCITED ENTRIES:\n" + renderCitations(citations)
}

// renderCitations renders the cited entries as a numbered list the judge can map
// claims onto, falling back to noCitationsMarker when the answer cited nothing so
// the absence of grounding is explicit rather than an empty block.
func renderCitations(citations []string) string {
	if len(citations) == 0 {
		return noCitationsMarker
	}

	numbered := lo.Map(citations, func(entry string, index int) string {
		return fmt.Sprintf("%d. %s", index+1, entry)
	})

	return strings.Join(numbered, "\n")
}

// failedJudgePass pairs the zero JudgeResult with err so each error path in Judge
// returns a fully-initialized result without repeating the literal.
func failedJudgePass(err error) (JudgeResult, error) {
	return JudgeResult{
		Verdict: JudgeVerdict{Approve: false, Critique: ""},
	}, err
}
