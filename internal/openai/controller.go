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

// controllerSchemaName names the structured-output schema sent to the Responses
// API. It is restricted to the [A-Za-z0-9_-] characters the API allows.
const controllerSchemaName = "controller_response"

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
func (client *Client) Controller(
	ctx context.Context,
	instructions, input string,
	onReasoning func(string),
) (ControllerResult, error) {
	format, err := structuredFormat[ControllerResponse](controllerSchemaName, "controller_schema")
	if err != nil {
		return failedControllerTurn(err)
	}

	// Stream the turn rather than blocking on one shot: gpt-5.5's reasoning pass can
	// run long, and streaming both lets the UI render thinking as it arrives (via the
	// onReasoning seam) and lets streamResponses apply the shared truncation guard. It
	// surfaces a transport failure as controller_call_failed and a reply the model could
	// not finish as controller_incomplete, so a truncated turn never reaches the decoder
	// as a cryptic "unexpected EOF". No MaxOutputTokens is set — output is unbounded
	// (mirroring librecode's Responses path); the §6 turn and wall-clock budget bound the
	// loop instead, and the caller compacts the input history.
	output, reasoning, err := client.streamResponses(ctx, &responses.ResponseNewParams{
		Model:        Model,
		Instructions: openaisdk.String(instructions),
		Input:        responses.ResponseNewParamsInputUnion{OfString: openaisdk.String(input)},
		Text:         responses.ResponseTextConfigParam{Format: format},
		// Reason at the client's configured controller effort (default medium): this is
		// the investigation brain, but the effort is tunable per role so a turn need not
		// pay maximum-effort latency. Summary:auto surfaces a readable prose summary of
		// that reasoning, which streamResponses accumulates from the reasoning-summary
		// deltas and renders as the turn's live thinking.
		Reasoning: responses.ReasoningParam{
			Effort:  client.controllerEffort,
			Summary: responses.ReasoningSummaryAuto,
		},
	}, onReasoning, "controller")
	if err != nil {
		return failedControllerTurn(err)
	}

	parsed, err := decodeStructured[ControllerResponse](output, "controller_decode")
	if err != nil {
		return failedControllerTurn(err)
	}

	// The reasoning summary streams in its own reasoning-summary deltas, not in the
	// structured output_text the decoder read, so attach the accumulated summary to
	// the parsed reply for the loop to render as this turn's thinking. It is empty
	// when the model returned no summary, in which case the caller falls back to the
	// terse structured Thinking field.
	parsed.Reasoning = reasoning

	return ControllerResult{Response: parsed}, nil
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
