package openai

import (
	"context"
	"encoding/json"
	"strings"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/samber/oops"
)

// maxControllerOutputTokens caps a controller turn's output at the §6 budget
// (1500 output tokens) so a single turn cannot overrun the per-call ceiling. The
// matching 8000-input cap is the caller's job: it compacts history before the
// call rather than relying on the API to truncate.
const maxControllerOutputTokens = 1500

// controllerSchemaName names the structured-output schema sent to the Responses
// API. It is restricted to the [A-Za-z0-9_-] characters the API allows.
const controllerSchemaName = "controller_response"

// ControllerResult is one controller turn's outcome: the parsed structured reply
// the model produced plus the token usage the call consumed, so the loop can both
// act on the next step and bill the turn against the session budget.
type ControllerResult struct {
	// Response is the decoded structured reply for this turn.
	Response ControllerResponse
	// Usage is the input/output token count the API reported for this call.
	Usage Usage
}

// Controller runs one controller turn against gpt-5.5: it sends instructions (the
// §14 system prompt) and input (the §6 rendered history) under the
// ControllerResponse structured-output schema, then decodes the model's JSON reply
// into a ControllerResponse and pairs it with the call's token usage. There is no
// fallback model — if gpt-5.5 rejects the request (for example because the key
// lacks access) the wrapped error surfaces immediately so the caller fails loudly
// rather than silently downgrading to a weaker model.
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
	})
	if err != nil {
		return failedControllerTurn(oops.
			In("openai").
			Code("controller_call_failed").
			Wrapf(err, "controller responses call on model %s", Model))
	}

	parsed, err := decodeStructured[ControllerResponse](resp.OutputText(), "controller_decode")
	if err != nil {
		return failedControllerTurn(err)
	}

	return ControllerResult{
		Response: parsed,
		Usage:    usageFrom(&resp.Usage),
	}, nil
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
// emitted object is the whole answer, so the first one is authoritative and any
// duplicate that follows is ignored. A truncated or non-JSON reply still fails
// here and surfaces under code.
func decodeStructured[T any](raw, code string) (T, error) {
	var out T

	if err := json.NewDecoder(strings.NewReader(raw)).Decode(&out); err != nil {
		return out, oops.
			In("openai").
			Code(code).
			Wrapf(err, "decode structured reply")
	}

	return out, nil
}

// usageFrom maps the API's reported token usage onto the package Usage type,
// narrowing the SDK's int64 counts to int at the single place the controller,
// sub-LLM, and judge roles all share. It takes a pointer because the SDK's usage
// struct is large and is only read here.
func usageFrom(reported *responses.ResponseUsage) Usage {
	return Usage{
		TokensIn:  int(reported.InputTokens),
		TokensOut: int(reported.OutputTokens),
	}
}

// failedControllerTurn pairs the zero ControllerResult with err so each error path
// in Controller returns a fully-initialized result without repeating the literal.
func failedControllerTurn(err error) (ControllerResult, error) {
	return ControllerResult{
		Response: ControllerResponse{Thinking: "", Code: "", Done: false},
		Usage:    Usage{TokensIn: 0, TokensOut: 0},
	}, err
}
