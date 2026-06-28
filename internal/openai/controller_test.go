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

	// Every Responses call is now a streaming call, so a success body is a
	// Server-Sent-Events stream. text/event-stream is the faithful content type; it
	// is not load-bearing, though — the SDK registers no per-content-type stream
	// decoder, so its default event-stream decoder parses the body whatever this
	// says, and a non-2xx error body is read regardless of content type.
	header := make(http.Header)
	header.Set("Content-Type", "text/event-stream")

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

// sseKeyType is the JSON field every Responses streaming event carries its event
// type in; the SDK's decoder switches on it, so the frame builders below stamp it
// onto each event object rather than repeating the bare "type" key.
const sseKeyType = "type"

// sseEvent renders one Responses streaming event as a Server-Sent-Events frame: a
// single data: line carrying the event JSON, terminated by the blank line the SDK's
// event-stream decoder needs to dispatch the event. The SDK reads each event's type
// off the data payload's own "type" field rather than the SSE "event:" line, so the
// event: line is omitted as redundant.
func sseEvent(t *testing.T, event map[string]any) string {
	t.Helper()

	payload, err := json.Marshal(event)
	require.NoError(t, err)

	return "data: " + string(payload) + "\n\n"
}

// outputTextDeltaFrame renders one response.output_text.delta SSE frame carrying
// text — the event whose deltas streamResponses accumulates into the reply. It is
// the one place the output-text event type is spelled, so the stream builders below
// compose it rather than repeating the wire string.
func outputTextDeltaFrame(t *testing.T, text string) string {
	t.Helper()

	return sseEvent(t, map[string]any{sseKeyType: "response.output_text.delta", "delta": text})
}

// completedFrame renders the terminal response.completed SSE frame whose response
// object reports status "completed", the event that ends a reply that fit the cap.
func completedFrame(t *testing.T) string {
	t.Helper()

	return sseEvent(t, map[string]any{
		sseKeyType: "response.completed",
		"response": map[string]any{"id": "resp_test", "status": "completed"},
	})
}

// completedStream renders a full Responses SSE stream that emits each delta as an
// output_text frame, then the terminal completed frame. The deltas concatenate into
// the output text streamResponses hands back, so a caller can drive any role end to
// end through the streaming transport; passing several deltas exercises the helper's
// across-delta accumulation.
func completedStream(t *testing.T, deltas ...string) string {
	t.Helper()

	var stream strings.Builder

	for _, delta := range deltas {
		stream.WriteString(outputTextDeltaFrame(t, delta))
	}

	stream.WriteString(completedFrame(t))

	return stream.String()
}

// incompleteStream renders a Responses SSE stream that emits a partial output_text
// frame then a terminal response.incomplete event whose response object reports
// status "incomplete" and incomplete_details.reason "max_output_tokens" — the
// truncation a reasoning model hits when its hidden reasoning plus the reply overrun
// max_output_tokens. It drives a role's shared *_incomplete guard.
func incompleteStream(t *testing.T, partial string) string {
	t.Helper()

	return outputTextDeltaFrame(t, partial) + sseEvent(t, map[string]any{
		sseKeyType: "response.incomplete",
		"response": map[string]any{
			"id":                 "resp_test",
			"status":             "incomplete",
			"incomplete_details": map[string]any{"reason": "max_output_tokens"},
		},
	})
}

// controllerResponseBody renders a Responses SSE stream whose output_text deltas
// carry the model's structured reply (reply marshaled to JSON), terminated by a
// response.completed event, so a test can drive Controller end to end through the
// streaming transport.
func controllerResponseBody(t *testing.T, reply openai.ControllerResponse) string {
	t.Helper()

	inner, err := json.Marshal(reply)
	require.NoError(t, err)

	return completedStream(t, string(inner))
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

func TestControllerParsesStructuredResponse(t *testing.T) {
	t.Parallel()

	reply := openai.ControllerResponse{
		Thinking: "List the boots, then inspect the failed unit.",
		Code:     "boots := journal.Boots()\nfmt.Println(len(boots))",
		Done:     false,
	}

	transport := new(mockTransport)
	transport.
		On("RoundTrip", mock.Anything).
		Return(http.StatusOK, controllerResponseBody(t, reply), nil).
		Once()

	client := newControllerClient(t, transport)

	result, err := client.Controller(context.Background(), "system prompt", "USER: why did sshd fail?", nil)
	require.NoError(t, err)

	assert.Equal(t, reply, result.Response, "the structured ControllerResponse fields round-trip")

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)
}

// reasoningControllerResponseBody renders a Responses SSE stream that emits the
// given summaries as response.reasoning_summary_text.delta events ahead of the
// reply's output_text delta and the terminal response.completed event, so a test
// can drive Controller against a turn that streamed a reasoning summary. The
// summaries are joined by a blank line — the way the loop renders a multi-part
// summary as paragraphs — and that joined prose is what streamResponses accumulates
// onto the turn's Reasoning.
func reasoningControllerResponseBody(t *testing.T, reply openai.ControllerResponse, summaries ...string) string {
	t.Helper()

	inner, err := json.Marshal(reply)
	require.NoError(t, err)

	var stream strings.Builder

	stream.WriteString(sseEvent(t, map[string]any{
		sseKeyType: "response.reasoning_summary_text.delta",
		"delta":    strings.Join(summaries, "\n\n"),
	}))
	stream.WriteString(outputTextDeltaFrame(t, string(inner)))
	stream.WriteString(completedFrame(t))

	return stream.String()
}

func TestControllerExtractsReasoningSummaryOntoResult(t *testing.T) {
	t.Parallel()

	reply := openai.ControllerResponse{
		Thinking: "inspect the failed unit",
		Code:     "boots := journal.Boots()",
		Done:     false,
	}

	transport := new(mockTransport)
	transport.
		On("RoundTrip", mock.Anything).
		Return(http.StatusOK, reasoningControllerResponseBody(t, reply,
			"First I listed the recent boots to find the failing window.",
			"Then I narrowed in on sshd, which OOM-killed at 09:01."), nil).
		Once()

	client := newControllerClient(t, transport)

	var streamed strings.Builder

	result, err := client.Controller(context.Background(), "system prompt", "USER: why did sshd fail?",
		func(delta string) { streamed.WriteString(delta) })
	require.NoError(t, err)

	assert.Equal(t,
		"First I listed the recent boots to find the failing window.\n\n"+
			"Then I narrowed in on sshd, which OOM-killed at 09:01.",
		result.Response.Reasoning,
		"the reasoning summary parts are joined as paragraphs onto the result")
	assert.Equal(t, result.Response.Reasoning, streamed.String(),
		"every reasoning-summary delta is forwarded live to the onReasoning callback")
	assert.Equal(t, reply.Code, result.Response.Code, "the structured code still decodes alongside the summary")
	assert.Equal(t, reply.Thinking, result.Response.Thinking, "the structured thinking still decodes")

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)
}

func TestControllerLeavesReasoningEmptyWhenNoSummaryReturned(t *testing.T) {
	t.Parallel()

	reply := openai.ControllerResponse{
		Thinking: "list the boots",
		Code:     "fmt.Println(len(journal.Boots()))",
		Done:     false,
	}

	transport := new(mockTransport)
	transport.
		On("RoundTrip", mock.Anything).
		Return(http.StatusOK, controllerResponseBody(t, reply), nil).
		Once()

	client := newControllerClient(t, transport)

	result, err := client.Controller(context.Background(), "system prompt", "USER: why did sshd fail?", nil)
	require.NoError(t, err)

	assert.Empty(t, result.Response.Reasoning,
		"a turn that streamed no reasoning-summary delta leaves the summary empty for the thinking fallback")

	transport.AssertExpectations(t)
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

	result, err := client.Controller(context.Background(), "system prompt", "USER: why did sshd fail?", nil)
	require.Error(t, err)

	assert.Equal(t, openai.ControllerResult{
		Response: openai.ControllerResponse{Thinking: "", Code: "", Done: false},
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
		}), nil).
		Once()

	client := newControllerClient(t, transport)

	_, err := client.Controller(context.Background(), instructions, input, nil)
	require.NoError(t, err)

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)

	return body
}

func TestControllerRequestSendsModelEffortAndStrictSchema(t *testing.T) {
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
		MaxOutputTokens int  `json:"max_output_tokens"`
		Stream          bool `json:"stream"`
	}

	require.NoError(t, json.Unmarshal(body, &payload))

	assert.Equal(t, openai.Model, payload.Model, "the controller turn runs on the flagship model")
	assert.True(t, payload.Stream, "the controller turn streams its reply")
	assert.Zero(t, payload.MaxOutputTokens, "output is unbounded — no max_output_tokens cap is sent")
	assert.Equal(t, instructions, payload.Instructions, "the §14 system prompt is sent as instructions")
	assert.Equal(t, input, payload.Input, "the §6 rendered history is sent as input")
	assert.Equal(t, "json_schema", payload.Text.Format.Type, "the reply is constrained by a json_schema format")
	assert.Equal(t, "controller_response", payload.Text.Format.Name, "the structured-output schema is named")
	assert.True(t, payload.Text.Format.Strict, "strict structured-output adherence is enabled")

	// The turn reasons at maximum effort (xhigh) — it is the investigation brain — while
	// still asking for an auto reasoning summary so the loop can render that prose as the
	// turn's live thinking. Decoded separately to keep the request-shape struct above
	// unchanged.
	var reasoning struct {
		Reasoning struct {
			Effort  string `json:"effort"`
			Summary string `json:"summary"`
		} `json:"reasoning"`
	}

	require.NoError(t, json.Unmarshal(body, &reasoning))
	assert.Equal(t, "xhigh", reasoning.Reasoning.Effort,
		"the controller turn reasons at maximum effort")
	assert.Equal(t, "auto", reasoning.Reasoning.Summary,
		"the turn asks gpt-5.5 for an auto reasoning summary to render as the turn's thinking")
}

func TestControllerDecodesAnswerSplitAcrossTwoDeltas(t *testing.T) {
	t.Parallel()

	// gpt-5.5 occasionally streams the structured answer as more than one
	// output_text run. streamResponses accumulates the deltas, so the decoder sees
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

	transport := new(mockTransport)
	transport.
		On("RoundTrip", mock.Anything).
		Return(http.StatusOK, completedStream(t, string(inner), string(inner)), nil).
		Once()

	client := newControllerClient(t, transport)

	result, err := client.Controller(context.Background(), "system prompt", "USER: why did sshd fail?", nil)
	require.NoError(t, err, "two concatenated objects must not fail the turn")

	assert.Equal(t, reply, result.Response, "the first of the two concatenated objects is the decoded answer")

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)
}

// assertControllerTurnFails drives one Controller call whose streamed response is
// streamBody and asserts the turn fails with the zero result under wantCode. The
// controller's truncation (controller_incomplete) and decode (controller_decode)
// failure paths share this exact shape, so it lives in one helper and each path's
// test states only the stream body and code that make it distinct.
func assertControllerTurnFails(t *testing.T, streamBody, wantCode string) {
	t.Helper()

	transport := new(mockTransport)
	transport.
		On("RoundTrip", mock.Anything).
		Return(http.StatusOK, streamBody, nil).
		Once()

	client := newControllerClient(t, transport)

	result, err := client.Controller(context.Background(), "system prompt", "USER: why did sshd fail?", nil)
	require.Error(t, err)

	assert.Equal(t, openai.ControllerResult{
		Response: openai.ControllerResponse{Thinking: "", Code: "", Done: false},
	}, result, "a failed turn yields the zero result")

	oopsErr, ok := oops.AsOops(err)
	require.True(t, ok, "the error is oops-wrapped")
	assert.Equal(t, "openai", oopsErr.Domain())
	assert.Equal(t, wantCode, oopsErr.Code())

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)
}

func TestControllerSurfacesIncompleteTruncationError(t *testing.T) {
	t.Parallel()

	// A turn whose reply does not fit in max_output_tokens ends with a terminal
	// response.incomplete event carrying a partial output_text; the controller must
	// surface controller_incomplete rather than let the partial JSON decode to a
	// cryptic "unexpected EOF".
	assertControllerTurnFails(t, incompleteStream(t, `{"thinking":"inspect the failed`), "controller_incomplete")
}

func TestControllerSurfacesMalformedReplyAsDecodeError(t *testing.T) {
	t.Parallel()

	// A completed reply whose accumulated output_text is not JSON at all surfaces as
	// an openai-domain controller_decode failure.
	assertControllerTurnFails(t, completedStream(t, "not json at all"), "controller_decode")
}

func TestControllerSurfacesTrailingGarbageAsDecodeError(t *testing.T) {
	t.Parallel()

	// A well-formed structured object followed by junk ("{...}garbage") must not slip
	// through: the decoder reads the first value, then drains the rest and rejects the
	// non-JSON suffix as controller_decode rather than letting a half-valid reply reach
	// the loop.
	reply := openai.ControllerResponse{
		Thinking: "inspect the failed unit before concluding",
		Code:     "boots := journal.Boots()",
		Done:     false,
	}

	inner, err := json.Marshal(reply)
	require.NoError(t, err)

	assertControllerTurnFails(t, completedStream(t, string(inner)+" not json at all"), "controller_decode")
}
