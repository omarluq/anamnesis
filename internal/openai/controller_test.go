package openai_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/samber/oops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/openai"
)

// TestMain seeds OPENAI_API_KEY for the whole external test package so NewClient
// succeeds without each test mutating process-wide environment (which t.Setenv
// would make incompatible with t.Parallel). The injected mock transport serves
// every request, so the key value is never sent anywhere real.
func TestMain(m *testing.M) {
	if err := os.Setenv(openai.EnvOpenAIKey, "sk-controller-test"); err != nil {
		panic(err)
	}

	os.Exit(m.Run())
}

// mockTransport is a testify mock of the http.RoundTripper seam the openai client
// issues its Responses calls through. Expectations script the canned HTTP response
// each request receives via .On("RoundTrip", ...).Return(resp, nil), and the
// built-in call recorder proves the no-fallback contract: a model_not_found turn
// must issue exactly one request and never retry to a weaker model.
//
// RoundTrip is a single-method seam, so a testify mock is a clear win over a
// hand-written stub: the embedded mock.Mock is mutex-guarded, keeping the -race
// detector quiet even if the SDK were to drive RoundTrip from another goroutine.
type mockTransport struct {
	mock.Mock
}

// RoundTrip records the request and replays the response scripted via
// .On("RoundTrip", ...).Return(status, body, err): it builds an HTTP response from
// the (status, body) pair the test supplied, mirroring the openai SDK transport
// seam. The response literal is assembled here rather than passed in so the body
// is created and owned at the point it is returned.
func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	args := m.Called(req)

	// Honor the http.RoundTripper contract: drain and close the request body so a
	// reader is never leaked across repeated calls. Tests read req.Body inside the
	// synchronous .Run callback above, so the body is already at EOF by here.
	if req.Body != nil {
		if _, err := io.Copy(io.Discard, req.Body); err != nil {
			return nil, err
		}

		if err := req.Body.Close(); err != nil {
			return nil, err
		}
	}

	if err := args.Error(2); err != nil {
		return nil, err
	}

	status := args.Int(0)
	body := args.String(1)

	header := make(http.Header)
	header.Set("Content-Type", "application/json")

	return &http.Response{
		StatusCode:    status,
		Status:        http.StatusText(status),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}, nil
}

// compile-time assertion that mockTransport satisfies http.RoundTripper.
var _ http.RoundTripper = (*mockTransport)(nil)

// controllerResponseBody renders a Responses API success envelope whose output
// text is the model's structured reply (reply marshaled to JSON) and whose usage
// block reports the given token counts, so a test can drive Controller end to end
// through the mock transport.
func controllerResponseBody(t *testing.T, reply openai.ControllerResponse, tokensIn, tokensOut int) string {
	t.Helper()

	inner, err := json.Marshal(reply)
	require.NoError(t, err)

	envelope := map[string]any{
		"id":         "resp_test",
		"object":     "response",
		"created_at": 1,
		"status":     "completed",
		"model":      openai.Model,
		"output": []map[string]any{
			{
				"id":     "msg_test",
				"type":   "message",
				"role":   "assistant",
				"status": "completed",
				"content": []map[string]any{
					{"type": "output_text", "text": string(inner), "annotations": []any{}},
				},
			},
		},
		"usage": map[string]any{
			"input_tokens":          tokensIn,
			"output_tokens":         tokensOut,
			"total_tokens":          tokensIn + tokensOut,
			"input_tokens_details":  map[string]any{"cached_tokens": 0},
			"output_tokens_details": map[string]any{"reasoning_tokens": 0},
		},
	}

	out, err := json.Marshal(envelope)
	require.NoError(t, err)

	return string(out)
}

// newControllerClient builds an openai client whose Responses calls are served by
// transport, pointed at a stub base URL so no request leaves the process. The API
// key comes from the ambient environment TestMain seeded.
func newControllerClient(t *testing.T, transport http.RoundTripper) *openai.Client {
	t.Helper()

	client, err := openai.NewClient(
		openai.WithBaseURL("https://stub.anamnesis.test/v1/"),
		openai.WithTransport(transport),
	)
	require.NoError(t, err)

	return client
}

func TestControllerParsesStructuredResponseAndUsage(t *testing.T) {
	t.Parallel()

	reply := openai.ControllerResponse{
		Thinking: "List the boots, then inspect the failed unit.",
		Code:     "boots := journal.Boots()\nfmt.Println(len(boots))",
		Done:     false,
	}

	transport := new(mockTransport)
	transport.
		On("RoundTrip", mock.Anything).
		Return(http.StatusOK, controllerResponseBody(t, reply, 1875, 219), nil).
		Once()

	client := newControllerClient(t, transport)

	result, err := client.Controller(context.Background(), "system prompt", "USER: why did sshd fail?")
	require.NoError(t, err)

	assert.Equal(t, reply, result.Response, "the structured ControllerResponse fields round-trip")
	assert.Equal(t, openai.Usage{TokensIn: 1875, TokensOut: 219}, result.Usage,
		"the API usage block maps onto Usage")

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)
}

func TestControllerSurfacesModelNotFoundWithNoFallback(t *testing.T) {
	t.Parallel()

	const errorBody = `{"error":{"message":"The model gpt-5.5 does not exist or you do not have access to it.",` +
		`"type":"invalid_request_error","param":null,"code":"model_not_found"}}`

	transport := new(mockTransport)
	transport.
		On("RoundTrip", mock.Anything).
		Return(http.StatusNotFound, errorBody, nil).
		Once()

	client := newControllerClient(t, transport)

	result, err := client.Controller(context.Background(), "system prompt", "USER: why did sshd fail?")
	require.Error(t, err)

	assert.Equal(t, openai.ControllerResult{
		Response: openai.ControllerResponse{Thinking: "", Code: "", Done: false},
		Usage:    openai.Usage{TokensIn: 0, TokensOut: 0},
	}, result, "a failed turn yields the zero result")

	oopsErr, ok := oops.AsOops(err)
	require.True(t, ok, "the error is oops-wrapped")
	assert.Equal(t, "openai", oopsErr.Domain())
	assert.Equal(t, "controller_call_failed", oopsErr.Code())

	var apiErr *openaisdk.Error

	require.ErrorAs(t, err, &apiErr, "the gpt-5.5 rejection surfaces as an openai API error")
	assert.Equal(t, "model_not_found", apiErr.Code)
	assert.Equal(t, http.StatusNotFound, apiErr.StatusCode)

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)
}

// captureControllerRequest drives one Controller call through a mock transport that
// records the outgoing request body during RoundTrip, returning that raw body so a
// test can assert on what actually went over the wire (model id, the §6 token cap,
// the strict structured-output schema, and the instructions and input).
func captureControllerRequest(t *testing.T, instructions, input string) []byte {
	t.Helper()

	var body []byte

	transport := new(mockTransport)
	transport.
		On("RoundTrip", mock.Anything).
		Run(func(args mock.Arguments) {
			req, ok := args.Get(0).(*http.Request)
			require.True(t, ok, "RoundTrip receives an *http.Request")
			require.NotNil(t, req.Body, "the controller call carries a request body")

			raw, err := io.ReadAll(req.Body)
			require.NoError(t, err)

			body = raw
		}).
		Return(http.StatusOK, controllerResponseBody(t, openai.ControllerResponse{
			Thinking: "done",
			Code:     "",
			Done:     true,
		}, 10, 10), nil).
		Once()

	client := newControllerClient(t, transport)

	_, err := client.Controller(context.Background(), instructions, input)
	require.NoError(t, err)

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)

	return body
}

func TestControllerRequestSendsModelTokenCapAndStrictSchema(t *testing.T) {
	t.Parallel()

	const (
		instructions = "the RLM controller system prompt"
		input        = "USER: why did sshd fail at 09:01?"
	)

	body := captureControllerRequest(t, instructions, input)

	var payload struct {
		Model        string `json:"model"`
		Instructions string `json:"instructions"`
		Input        string `json:"input"`
		Text         struct {
			Format struct {
				Type   string `json:"type"`
				Name   string `json:"name"`
				Strict bool   `json:"strict"`
			} `json:"format"`
		} `json:"text"`
		MaxOutputTokens int `json:"max_output_tokens"`
	}

	require.NoError(t, json.Unmarshal(body, &payload))

	assert.Equal(t, openai.Model, payload.Model, "the controller turn runs on the flagship model")
	assert.Equal(t, 1500, payload.MaxOutputTokens, "the turn caps output at the §6 1500-token budget")
	assert.Equal(t, instructions, payload.Instructions, "the §14 system prompt is sent as instructions")
	assert.Equal(t, input, payload.Input, "the §6 rendered history is sent as input")
	assert.Equal(t, "json_schema", payload.Text.Format.Type, "the reply is constrained by a json_schema format")
	assert.Equal(t, "controller_response", payload.Text.Format.Name, "the structured-output schema is named")
	assert.True(t, payload.Text.Format.Strict, "strict structured-output adherence is enabled")
}

func TestControllerDecodesAnswerSplitAcrossTwoMessages(t *testing.T) {
	t.Parallel()

	// gpt-5.5 occasionally returns the structured answer as more than one output
	// message item. resp.OutputText concatenates them, so the decoder sees
	// "{...}{...}" — which a plain json.Unmarshal rejects as trailing data with
	// "invalid character '{' after top-level value". The controller must still
	// decode the first (authoritative) object and resolve the turn.
	reply := openai.ControllerResponse{
		Thinking: "inspect the failed unit before concluding",
		Code:     "boots := journal.Boots()\nfmt.Println(len(boots))",
		Done:     false,
	}

	inner, err := json.Marshal(reply)
	require.NoError(t, err)

	object, err := json.Marshal(string(inner))
	require.NoError(t, err)

	message := func(id string) string {
		return `{"id":"msg_` + id + `","type":"message","role":"assistant","status":"completed",` +
			`"content":[{"type":"output_text","text":` + string(object) + `,"annotations":[]}]}`
	}
	doubled := `{"id":"resp_test","object":"response","created_at":1,"status":"completed",` +
		`"model":"` + openai.Model + `","output":[` + message("a") + `,` + message("b") + `],` +
		`"usage":{"input_tokens":12,"output_tokens":8,"total_tokens":20,` +
		`"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}`

	transport := new(mockTransport)
	transport.
		On("RoundTrip", mock.Anything).
		Return(http.StatusOK, doubled, nil).
		Once()

	client := newControllerClient(t, transport)

	result, err := client.Controller(context.Background(), "system prompt", "USER: why did sshd fail?")
	require.NoError(t, err, "two concatenated objects must not fail the turn")

	assert.Equal(t, reply, result.Response, "the first of the two concatenated objects is the decoded answer")
	assert.Equal(t, openai.Usage{TokensIn: 12, TokensOut: 8}, result.Usage,
		"usage is still read from the envelope")

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)
}

func TestControllerSurfacesMalformedReplyAsDecodeError(t *testing.T) {
	t.Parallel()

	const malformedBody = `{"id":"resp_test","object":"response","created_at":1,"status":"completed",` +
		`"model":"gpt-5.5","output":[{"id":"msg_test","type":"message","role":"assistant",` +
		`"status":"completed","content":[{"type":"output_text","text":"not json at all","annotations":[]}]}],` +
		`"usage":{"input_tokens":5,"output_tokens":5,"total_tokens":10,` +
		`"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}`

	transport := new(mockTransport)
	transport.
		On("RoundTrip", mock.Anything).
		Return(http.StatusOK, malformedBody, nil).
		Once()

	client := newControllerClient(t, transport)

	result, err := client.Controller(context.Background(), "system prompt", "USER: why did sshd fail?")
	require.Error(t, err)

	assert.Equal(t, openai.ControllerResult{
		Response: openai.ControllerResponse{Thinking: "", Code: "", Done: false},
		Usage:    openai.Usage{TokensIn: 0, TokensOut: 0},
	}, result, "a malformed model reply yields the zero result")

	oopsErr, ok := oops.AsOops(err)
	require.True(t, ok, "the error is oops-wrapped")
	assert.Equal(t, "openai", oopsErr.Domain())
	assert.Equal(t, "controller_decode", oopsErr.Code(),
		"a non-JSON output_text surfaces as an openai-domain decode error")

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)
}

func TestControllerSurfacesTrailingGarbageAsDecodeError(t *testing.T) {
	t.Parallel()

	// A well-formed structured object followed by junk ("{...}garbage") must not
	// slip through: json.Decoder.Decode reads only the first value and would leave
	// the trailing bytes unread, so the decoder has to drain the rest and reject
	// the non-JSON suffix as a controller_decode failure rather than letting a
	// half-valid reply reach the loop.
	reply := openai.ControllerResponse{
		Thinking: "inspect the failed unit before concluding",
		Code:     "boots := journal.Boots()",
		Done:     false,
	}

	inner, err := json.Marshal(reply)
	require.NoError(t, err)

	object, err := json.Marshal(string(inner) + " not json at all")
	require.NoError(t, err)

	trailingBody := `{"id":"resp_test","object":"response","created_at":1,"status":"completed",` +
		`"model":"` + openai.Model + `","output":[{"id":"msg_test","type":"message","role":"assistant",` +
		`"status":"completed","content":[{"type":"output_text","text":` + string(object) + `,"annotations":[]}]}],` +
		`"usage":{"input_tokens":5,"output_tokens":5,"total_tokens":10,` +
		`"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}`

	transport := new(mockTransport)
	transport.
		On("RoundTrip", mock.Anything).
		Return(http.StatusOK, trailingBody, nil).
		Once()

	client := newControllerClient(t, transport)

	result, err := client.Controller(context.Background(), "system prompt", "USER: why did sshd fail?")
	require.Error(t, err, "trailing garbage after the first object must fail the turn")

	assert.Equal(t, openai.ControllerResult{
		Response: openai.ControllerResponse{Thinking: "", Code: "", Done: false},
		Usage:    openai.Usage{TokensIn: 0, TokensOut: 0},
	}, result, "a reply with trailing garbage yields the zero result")

	oopsErr, ok := oops.AsOops(err)
	require.True(t, ok, "the error is oops-wrapped")
	assert.Equal(t, "openai", oopsErr.Domain())
	assert.Equal(t, "controller_decode", oopsErr.Code(),
		"trailing non-JSON content surfaces as an openai-domain decode error")

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)
}
