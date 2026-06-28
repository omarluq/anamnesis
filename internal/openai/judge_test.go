package openai_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/samber/oops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/openai"
)

// judgeResponseBody renders a Responses SSE stream whose output_text delta carries
// verdict marshaled to JSON (the structured reply the judge model emits), terminated
// by a response.completed event, so a test can drive Judge end to end through the
// streaming transport. It reuses the shared completedStream builder so every role's
// success body shares one SSE shape.
func judgeResponseBody(t *testing.T, verdict openai.JudgeVerdict) string {
	t.Helper()

	inner, err := json.Marshal(verdict)
	require.NoError(t, err)

	return completedStream(t, string(inner))
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
		Return(http.StatusOK, judgeResponseBody(t, openai.JudgeVerdict{Approve: true, Critique: ""}), nil).
		Once()

	client := newControllerClient(t, transport)

	_, err := client.Judge(context.Background(), question, answer, citations)
	require.NoError(t, err)

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)

	return body
}

func TestJudgeReturnsCritiqueVerdict(t *testing.T) {
	t.Parallel()

	verdict := openai.JudgeVerdict{
		Approve:  false,
		Critique: "The claim that the kernel ran out of memory is unsupported by any cited entry.",
	}

	transport := new(mockTransport)
	transport.
		On("RoundTrip", mock.Anything).
		Return(http.StatusOK, judgeResponseBody(t, verdict), nil).
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

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)
}

func TestJudgeApprovesWithEmptyCritique(t *testing.T) {
	t.Parallel()

	verdict := openai.JudgeVerdict{Approve: true, Critique: ""}

	transport := new(mockTransport)
	transport.
		On("RoundTrip", mock.Anything).
		Return(http.StatusOK, judgeResponseBody(t, verdict), nil).
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

	assert.Contains(t, string(body), "(none — the answer cited no journal entries)",
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
		Return(http.StatusOK, judgeResponseBody(t, verdict), nil).
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
	}, result, "a failed judge call yields the zero result")

	oopsErr, ok := oops.AsOops(err)
	require.True(t, ok, "the error is oops-wrapped")
	assert.Equal(t, "openai", oopsErr.Domain())
	assert.Equal(t, "judge_call_failed", oopsErr.Code())

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)
}

func TestJudgeSurfacesIncompleteTruncationError(t *testing.T) {
	t.Parallel()

	// The EOF bug: gpt-5.5's hidden reasoning draws from the judge's output budget, so
	// a verdict could truncate before its JSON closed and decode to a cryptic
	// "unexpected EOF". The judge had no incomplete guard. Streaming adds the shared
	// guard, so a terminal response.incomplete now surfaces as judge_incomplete.
	transport := new(mockTransport)
	transport.
		On("RoundTrip", mock.Anything).
		Return(http.StatusOK, incompleteStream(t, `{"approve":fal`), nil).
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
	}, result, "a truncated verdict yields the zero result")

	oopsErr, ok := oops.AsOops(err)
	require.True(t, ok, "the error is oops-wrapped")
	assert.Equal(t, "openai", oopsErr.Domain())
	assert.Equal(t, "judge_incomplete", oopsErr.Code(),
		"a truncated verdict surfaces as judge_incomplete, not a cryptic decode EOF")

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)
}

func TestJudgeSurfacesDecodeFailure(t *testing.T) {
	t.Parallel()

	transport := new(mockTransport)
	transport.
		On("RoundTrip", mock.Anything).
		Return(http.StatusOK, completedStream(t, "this is prose, not a JudgeVerdict JSON object"), nil).
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
	}, result, "a verdict that fails to decode yields the zero result")

	oopsErr, ok := oops.AsOops(err)
	require.True(t, ok, "the error is oops-wrapped")
	assert.Equal(t, "openai", oopsErr.Domain())
	assert.Equal(t, "judge_decode", oopsErr.Code())

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)
}
