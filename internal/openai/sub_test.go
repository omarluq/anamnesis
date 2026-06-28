package openai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/openai"
)

// subRequestHeadroom bounds the §15 system prompt, the PROMPT/CONTEXT framing, and
// the JSON envelope that ride alongside the capped evidence in a sub-call request,
// so a test can assert the truncated body fits within MaxSubEvidenceBytes plus this
// fixed overhead rather than the unbounded evidence the caller supplied.
const subRequestHeadroom = 2048

// subResponseBody renders a Responses SSE stream whose output_text delta carries
// reply (a plain sub-LLM answer, not structured JSON), terminated by a
// response.completed event, so a test can drive Sub end to end through the streaming
// transport. It reuses the shared completedStream builder so every role's success
// body shares one SSE shape.
func subResponseBody(t *testing.T, reply string) string {
	t.Helper()

	return completedStream(t, reply)
}

// captureSubRequest drives one Sub call through captureRequest with a fixed canned
// reply, returning the raw outgoing request body so a test can assert on what
// actually went over the wire (size after truncation, model id). The canned reply
// is fixed because callers assert on the request, not the reply.
func captureSubRequest(t *testing.T, prompt, evidence string) []byte {
	t.Helper()

	return captureRequest(t, subResponseBody(t, "ok"), func(client *openai.Client) error {
		_, err := client.Sub(context.Background(), prompt, evidence)

		return err
	})
}

func TestSubReturnsTextReply(t *testing.T) {
	t.Parallel()

	const reply = "sshd failed at 09:01: port 22 already in use; the prior instance never released it."

	client, transport := newClientServing(t, http.StatusOK, subResponseBody(t, reply))

	result, err := client.Sub(
		context.Background(),
		"Summarize why sshd failed.",
		"[]journal.Entry{{Unit: \"sshd.service\", Message: \"address already in use\"}}",
	)
	require.NoError(t, err)

	assert.Equal(t, reply, result.Text, "the sub-LLM text reply round-trips")

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)
}

// assertSubTurnFails drives one Sub call whose streamed response is streamBody and
// asserts the sub-call fails with the zero result under wantCode — the mirror of
// assertControllerTurnFails for the sub role.
func assertSubTurnFails(t *testing.T, streamBody, wantCode string) {
	t.Helper()

	client, transport := newClientServing(t, http.StatusOK, streamBody)

	result, err := client.Sub(context.Background(), "Summarize the failures.", "[]journal.Entry{}")
	require.Error(t, err)

	assert.Equal(t, openai.SubResult{Text: ""}, result, "a failed sub-call yields the zero result")

	assertOpenAICode(t, err, wantCode)

	transport.AssertExpectations(t)
	transport.AssertNumberOfCalls(t, "RoundTrip", 1)
}

func TestSubTruncatesOversizedEvidenceWithinBudget(t *testing.T) {
	t.Parallel()

	const oversizedBytes = 64 * 1024

	oversized := strings.Repeat("E", oversizedBytes)

	body := captureSubRequest(t, "Summarize the failures.", oversized)

	assert.LessOrEqual(t, len(body), openai.MaxSubEvidenceBytes+subRequestHeadroom,
		"the truncated request body stays within the sub-call input budget")
	assert.Less(t, len(body), oversizedBytes,
		"oversized evidence is truncated rather than shipped whole")
	assert.Contains(t, string(body), "evidence truncated",
		"the truncation marker reaches the wire so the model knows its context was cut")
}

func TestSubShipsWithinBudgetEvidenceUntouched(t *testing.T) {
	t.Parallel()

	const evidence = "sshd.service reported address already in use at 09:01"

	body := captureSubRequest(t, "Summarize the failures.", evidence)

	assert.Contains(t, string(body), evidence,
		"within-budget evidence ships whole")
	assert.NotContains(t, string(body), "evidence truncated",
		"within-budget evidence carries no truncation marker")
}

func TestSubTruncationKeepsValidUTF8AtMultibyteBoundary(t *testing.T) {
	t.Parallel()

	// Three ASCII-prefix offsets guarantee that, whatever the marker length, at
	// least two of the cuts land mid-rune in the 3-byte 世 run — exercising the
	// strings.ToValidUTF8 boundary trim rather than only single-byte truncation.
	for _, prefix := range []int{0, 1, 2} {
		evidence := strings.Repeat("a", prefix) + strings.Repeat("世", 16*1024)

		body := captureSubRequest(t, "Summarize the failures.", evidence)

		var payload struct {
			Input string `json:"input"`
		}

		require.NoError(t, json.Unmarshal(body, &payload))
		assert.True(t, utf8.ValidString(payload.Input),
			"the truncated sub-call input is valid UTF-8")
		assert.NotContains(t, payload.Input, "�",
			"the truncation cut never carries a split rune onto the wire")
	}
}

func TestSubSurfacesModelNotFoundWithNoFallback(t *testing.T) {
	t.Parallel()

	assertModelNotFoundSurface(t, "sub_call_failed", func(client *openai.Client) error {
		result, err := client.Sub(context.Background(), "Summarize the failures.", "[]journal.Entry{}")
		assert.Equal(t, openai.SubResult{Text: ""}, result, "a failed sub-call yields the zero result")

		return err
	})
}

func TestSubSurfacesIncompleteTruncationError(t *testing.T) {
	t.Parallel()

	// gpt-5.5's hidden reasoning draws from the sub-call's output budget, so a reply
	// can truncate before the answer is complete. Sub now streams, so that terminal
	// response.incomplete surfaces as sub_incomplete rather than a silently shortened
	// answer — the guard the blocking one-shot call lacked.
	assertSubTurnFails(t, incompleteStream(t, "sshd failed because"), "sub_incomplete")
}

func TestSubRequestUsesFlagshipModel(t *testing.T) {
	t.Parallel()

	body := captureSubRequest(t, "Summarize.", "[]journal.Entry{{Unit: \"sshd.service\"}}")

	var payload struct {
		Model string `json:"model"`
	}

	require.NoError(t, json.Unmarshal(body, &payload))
	assert.Equal(t, openai.Model, payload.Model,
		"the sub-call runs on the same flagship model as the controller")
}
