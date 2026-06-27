package openai_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/samber/oops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/openai"
)

// judgeResponseBody renders a Responses API success envelope whose output text is
// verdict marshaled to JSON (the structured reply the judge model emits) and whose
// usage block reports the given token counts, so a test can drive Judge end to end
// through the mock transport. The verdict is double-encoded — once to the JSON
// object the model returns, then again so it embeds as the string-valued output
// text field — mirroring how the API ships structured output.
func judgeResponseBody(t *testing.T, verdict openai.JudgeVerdict, tokensIn, tokensOut int) string {
	t.Helper()

	inner, err := json.Marshal(verdict)
	require.NoError(t, err)

	text, err := json.Marshal(string(inner))
	require.NoError(t, err)

	return fmt.Sprintf(
		`{"id":"resp_judge","object":"response","created_at":1,"status":"completed",`+
			`"model":%q,"output":[{"id":"msg_judge","type":"message","role":"assistant",`+
			`"status":"completed","content":[{"type":"output_text","text":%s,"annotations":[]}]}],`+
			`"usage":{"input_tokens":%d,"output_tokens":%d,"total_tokens":%d,`+
			`"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}`,
		openai.Model, string(text), tokensIn, tokensOut, tokensIn+tokensOut,
	)
}

// captureJudgeRequest drives one Judge call through a mock transport that records
// the outgoing request body during RoundTrip, returning that raw body so a test
// can assert on what actually went over the wire (model id, the framed input).
func captureJudgeRequest(t *testing.T, question, answer string, citations []string) []byte {
	t.Helper()

	var body []byte

	transport := new(mockTransport)
	transport.
		On("RoundTrip", mock.Anything).
		Run(func(args mock.Arguments) {
			req, ok := args.Get(0).(*http.Request)
			require.True(t, ok, "RoundTrip receives an *http.Request")
			require.NotNil(t, req.Body, "the judge call carries a request body")

			raw, err := io.ReadAll(req.Body)
			require.NoError(t, err)

			body = raw
		}).
		Return(http.StatusOK, judgeResponseBody(t, openai.JudgeVerdict{Approve: true, Critique: ""}, 10, 10), nil).
		Once()

	client := newControllerClient(t, transport)

	_, err := client.Judge(context.Background(), question, answer, citations)
	require.NoError(t, err)

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)

	return body
}

func TestJudgeReturnsCritiqueVerdictAndUsage(t *testing.T) {
	t.Parallel()

	verdict := openai.JudgeVerdict{
		Approve:  false,
		Critique: "The claim that the kernel ran out of memory is unsupported by any cited entry.",
	}

	transport := new(mockTransport)
	transport.
		On("RoundTrip", mock.Anything).
		Return(http.StatusOK, judgeResponseBody(t, verdict, 845, 37), nil).
		Once()

	client := newControllerClient(t, transport)

	result, err := client.Judge(
		context.Background(),
		"Why did sshd fail at 09:01?",
		"sshd crashed because the kernel ran out of memory.",
		[]string{"sshd.service: address already in use"},
	)
	require.NoError(t, err)

	assert.Equal(t, verdict, result.Verdict, "the structured approve/critique verdict round-trips")
	assert.False(t, result.Verdict.Approve, "the judge rejected the answer")
	assert.NotEmpty(t, result.Verdict.Critique, "a rejection carries a critique")
	assert.Equal(t, openai.Usage{TokensIn: 845, TokensOut: 37}, result.Usage,
		"the API usage block maps onto Usage")

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)
}

func TestJudgeApprovesWithEmptyCritique(t *testing.T) {
	t.Parallel()

	verdict := openai.JudgeVerdict{Approve: true, Critique: ""}

	transport := new(mockTransport)
	transport.
		On("RoundTrip", mock.Anything).
		Return(http.StatusOK, judgeResponseBody(t, verdict, 612, 5), nil).
		Once()

	client := newControllerClient(t, transport)

	result, err := client.Judge(
		context.Background(),
		"Why did sshd fail at 09:01?",
		"sshd failed because port 22 was already bound; entry 1 shows the bind error.",
		[]string{"sshd.service: address already in use"},
	)
	require.NoError(t, err)

	assert.True(t, result.Verdict.Approve, "the judge approved the grounded answer")
	assert.Empty(t, result.Verdict.Critique, "an approval carries no critique")
	assert.Equal(t, openai.Usage{TokensIn: 612, TokensOut: 5}, result.Usage,
		"the API usage block maps onto Usage")

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)
}

func TestJudgeRequestFramesQuestionAnswerAndCitations(t *testing.T) {
	t.Parallel()

	const (
		question = "Why did the box stall around 09:00?"
		answer   = "checkout-api was OOM-killed under memory pressure."
		citation = "checkout-api.service: Out of memory: Killed process 4242"
	)

	body := captureJudgeRequest(t, question, answer, []string{citation})

	var payload struct {
		Model string `json:"model"`
	}

	require.NoError(t, json.Unmarshal(body, &payload))
	assert.Equal(t, openai.Model, payload.Model,
		"the judge runs on the same flagship model as the controller")

	raw := string(body)
	assert.Contains(t, raw, question, "the judge input carries the user question")
	assert.Contains(t, raw, answer, "the judge input carries the final answer")
	assert.Contains(t, raw, citation, "the judge input lists the cited entries")
	assert.Contains(t, raw, "1. "+citation, "the cited entries render as a numbered list")
}

func TestJudgeRendersNoCitationsMarker(t *testing.T) {
	t.Parallel()

	body := captureJudgeRequest(t, "What failed at boot?", "Something went wrong with the network.", nil)

	assert.Contains(t, string(body), "(none",
		"a zero-citation answer renders the explicit no-grounding marker on the wire")
}

func TestJudgeHandlesAnswerWithNoCitations(t *testing.T) {
	t.Parallel()

	verdict := openai.JudgeVerdict{
		Approve:  false,
		Critique: "The answer cites no journal entries, so every claim is ungrounded.",
	}

	transport := new(mockTransport)
	transport.
		On("RoundTrip", mock.Anything).
		Return(http.StatusOK, judgeResponseBody(t, verdict, 300, 21), nil).
		Once()

	client := newControllerClient(t, transport)

	result, err := client.Judge(
		context.Background(),
		"What failed at boot?",
		"Something went wrong with the network.",
		nil,
	)
	require.NoError(t, err)

	assert.False(t, result.Verdict.Approve, "an uncited answer is rejected")
	assert.NotEmpty(t, result.Verdict.Critique, "the rejection explains the missing grounding")

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)
}

func TestJudgeSurfacesCallFailure(t *testing.T) {
	t.Parallel()

	const errorBody = `{"error":{"message":"The model gpt-5.5 does not exist or you do not have access to it.",` +
		`"type":"invalid_request_error","param":null,"code":"model_not_found"}}`

	transport := new(mockTransport)
	transport.
		On("RoundTrip", mock.Anything).
		Return(http.StatusNotFound, errorBody, nil).
		Once()

	client := newControllerClient(t, transport)

	result, err := client.Judge(
		context.Background(),
		"Why did nginx fail to start?",
		"nginx died because its configuration was invalid.",
		[]string{"nginx.service: invalid configuration directive"},
	)
	require.Error(t, err)

	assert.Equal(t, openai.JudgeResult{
		Verdict: openai.JudgeVerdict{Approve: false, Critique: ""},
		Usage:   openai.Usage{TokensIn: 0, TokensOut: 0},
	}, result, "a failed judge call yields the zero result")

	oopsErr, ok := oops.AsOops(err)
	require.True(t, ok, "the error is oops-wrapped")
	assert.Equal(t, "openai", oopsErr.Domain())
	assert.Equal(t, "judge_call_failed", oopsErr.Code())

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)
}

func TestJudgeSurfacesDecodeFailure(t *testing.T) {
	t.Parallel()

	malformedBody := fmt.Sprintf(
		`{"id":"resp_judge","object":"response","created_at":1,"status":"completed",`+
			`"model":%q,"output":[{"id":"msg_judge","type":"message","role":"assistant",`+
			`"status":"completed","content":[{"type":"output_text","text":%q,"annotations":[]}]}],`+
			`"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12,`+
			`"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}`,
		openai.Model, "this is prose, not a JudgeVerdict JSON object",
	)

	transport := new(mockTransport)
	transport.
		On("RoundTrip", mock.Anything).
		Return(http.StatusOK, malformedBody, nil).
		Once()

	client := newControllerClient(t, transport)

	result, err := client.Judge(
		context.Background(),
		"What killed the database process?",
		"postgres was OOM-killed under memory pressure.",
		[]string{"postgres.service: out of memory, killed process 4242"},
	)
	require.Error(t, err)

	assert.Equal(t, openai.JudgeResult{
		Verdict: openai.JudgeVerdict{Approve: false, Critique: ""},
		Usage:   openai.Usage{TokensIn: 0, TokensOut: 0},
	}, result, "a verdict that fails to decode yields the zero result")

	oopsErr, ok := oops.AsOops(err)
	require.True(t, ok, "the error is oops-wrapped")
	assert.Equal(t, "openai", oopsErr.Domain())
	assert.Equal(t, "judge_decode", oopsErr.Code())

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)
}
