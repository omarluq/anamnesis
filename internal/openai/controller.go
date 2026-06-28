package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/samber/oops"
)

// maxControllerOutputTokens caps a controller turn's output. gpt-5.5 is a reasoning
// model whose hidden reasoning tokens draw from this same budget, so the cap must
// cover reasoning plus the structured reply (thinking + a multi-line Go code block);
// the former 1500 truncated reasoning turns into a partial reply that decoded to
// "unexpected EOF". 4096 matches librecode's default output cap. The matching
// 8000-input budget is the caller's job: it compacts history before the call.
const maxControllerOutputTokens = 4096

// controllerSchemaName names the structured-output schema sent to the Responses
// API. It is restricted to the [A-Za-z0-9_-] characters the API allows.
const controllerSchemaName = "controller_response"

// reasoningOutputItemType is the Type discriminator the Responses API stamps on a
// reasoning output item, the item whose summary parts carry the turn's reasoning
// summary prose.
const reasoningOutputItemType = "reasoning"

// ControllerResult is one controller turn's outcome: the parsed structured reply
// the model produced, so the loop can act on the next step.
type ControllerResult struct {
	// Response is the decoded structured reply for this turn.
	Response ControllerResponse
}

// Controller runs one controller turn against gpt-5.5: it sends instructions (the
// §14 system prompt) and input (the §6 rendered history) under the
// ControllerResponse structured-output schema, then decodes the model's JSON reply
// into a ControllerResponse. There is no fallback model — if gpt-5.5 rejects the
// request (for example because the key lacks access) the wrapped error surfaces
// immediately so the caller fails loudly rather than silently downgrading to a
// weaker model.
func (client *Client) Controller(ctx context.Context, instructions, input string) (ControllerResult, error) {
	format, err := structuredFormat[ControllerResponse](controllerSchemaName, "controller_schema")
	if err != nil {
		return failedControllerTurn(err)
	}

	resp, err := client.api.Responses.New(ctx, responses.ResponseNewParams{
		Model:           Model,
		Instructions:    openaisdk.String(instructions),
		MaxOutputTokens: openaisdk.Int(maxControllerOutputTokens),
		Input:           responses.ResponseNewParamsInputUnion{OfString: openaisdk.String(input)},
		Text:            responses.ResponseTextConfigParam{Format: format},
		// Ask gpt-5.5 for a reasoning summary alongside the structured reply. gpt-5.5
		// already reasons by default (its hidden reasoning tokens draw from the output
		// budget above), so "auto" surfaces a readable prose summary of that reasoning
		// in the response's reasoning output item, which renders as the turn's thinking
		// far better than the terse structured Thinking field.
		Reasoning: responses.ReasoningParam{Summary: responses.ReasoningSummaryAuto},
	})
	if err != nil {
		return failedControllerTurn(oops.
			In("openai").
			Code("controller_call_failed").
			Wrapf(err, "controller responses call on model %s", Model))
	}

	// A reply that did not fit in MaxOutputTokens comes back with status "incomplete"
	// and a partial output_text; decoding that truncated JSON would surface as a
	// cryptic "unexpected EOF". Detect the truncation up front and report it as an
	// actionable error (mirrors librecode's Responses finish-reason classification).
	if resp.Status == responses.ResponseStatusIncomplete {
		return failedControllerTurn(oops.
			In("openai").
			Code("controller_incomplete").
			Errorf("controller reply truncated (reason %q): too large for max_output_tokens=%d",
				resp.IncompleteDetails.Reason, maxControllerOutputTokens))
	}

	parsed, err := decodeStructured[ControllerResponse](resp.OutputText(), "controller_decode")
	if err != nil {
		return failedControllerTurn(err)
	}

	// The reasoning summary travels in a separate reasoning output item, not in the
	// structured output_text the decoder read, so attach it to the parsed reply for
	// the loop to render as this turn's thinking. It is empty when the model returned
	// no summary, in which case the caller falls back to the terse Thinking field.
	parsed.Reasoning = reasoningSummary(resp.Output)

	return ControllerResult{Response: parsed}, nil
}

// reasoningSummary joins the summary prose from every reasoning output item the
// Responses API returned for a turn (each holds zero or more summary_text parts),
// separating parts with a blank line so a multi-section summary reads as
// paragraphs. It returns the empty string when the response carried no reasoning
// summary — gpt-5.5 may answer a simple turn without one — so the caller can fall
// back to the terse structured Thinking field.
func reasoningSummary(output []responses.ResponseOutputItemUnion) string {
	// Indexed iteration, not range-by-value or lo: ResponseOutputItemUnion is a
	// ~3 KB flattened union, so copying one per element (range value or an lo
	// iteratee param) is the kind of heavy copy gocritic rejects.
	parts := make([]string, 0, len(output))

	for index := range output {
		item := &output[index]
		if item.Type != reasoningOutputItemType {
			continue
		}

		for summaryIndex := range item.Summary {
			if text := strings.TrimSpace(item.Summary[summaryIndex].Text); text != "" {
				parts = append(parts, text)
			}
		}
	}

	return strings.Join(parts, "\n\n")
}

// structuredFormat builds the Responses API structured-output format that
// constrains a reply to T's JSON schema, with strict adherence enabled so the
// model cannot return fields outside the schema. name is the schema name the API
// echoes back and code is the oops code a schema-build failure surfaces under. The
// controller and judge roles share it so the structured-output wiring lives in one
// place.
func structuredFormat[T any](name, code string) (responses.ResponseFormatTextConfigUnionParam, error) {
	schema, err := GenerateSchema[T]()
	if err != nil {
		return responses.ResponseFormatTextConfigUnionParam{}, oops.
			In("openai").
			Code(code).
			Wrapf(err, "build %s schema", name)
	}

	return responses.ResponseFormatTextConfigUnionParam{
		OfJSONSchema: &responses.ResponseFormatTextJSONSchemaConfigParam{
			Name:   name,
			Schema: schema,
			Strict: openaisdk.Bool(true),
		},
	}, nil
}

// decodeStructured parses a model's structured JSON reply (the Responses output
// text) into T, wrapping a decode failure as an oops error under code so a
// malformed reply surfaces in the openai domain rather than as a bare
// encoding/json error. The controller and judge roles share it.
//
// It decodes the first complete JSON value rather than json.Unmarshal-ing the
// whole string: resp.OutputText concatenates the output_text of every output
// item, and gpt-5.5 occasionally returns the structured answer across more than
// one message item, so the concatenation is two schema-conformant objects
// ("{...}{...}") that json.Unmarshal rejects as trailing data ("invalid
// character '{' after top-level value"). Under strict structured output every
// emitted object is the whole answer, so the first one is authoritative; the
// remainder of the stream is then drained and must be well-formed JSON, so a
// duplicate object is ignored while trailing junk like "{...}xyz" — which Decode
// would otherwise leave unread — still fails here, as does a truncated or
// non-JSON reply. Every such failure surfaces under code.
func decodeStructured[T any](raw, code string) (T, error) {
	var out T

	dec := json.NewDecoder(strings.NewReader(raw))
	if err := dec.Decode(&out); err != nil {
		return out, oops.
			In("openai").
			Code(code).
			Wrapf(err, "decode structured reply")
	}

	// Decode stops after the first value and leaves any trailing bytes unread, so
	// drain the rest: each remaining value must parse as JSON (the duplicate object
	// gpt-5.5 emits when the answer spans two message items), and the first
	// non-JSON byte surfaces under code rather than reaching the caller.
	for {
		var discard json.RawMessage
		if err := dec.Decode(&discard); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return out, oops.
				In("openai").
				Code(code).
				Wrapf(err, "decode structured reply")
		}
	}

	return out, nil
}

// failedControllerTurn pairs the zero ControllerResult with err so each error path
// in Controller returns a fully-initialized result without repeating the literal.
func failedControllerTurn(err error) (ControllerResult, error) {
	return ControllerResult{
		Response: ControllerResponse{Thinking: "", Code: "", Done: false},
	}, err
}
