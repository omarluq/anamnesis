package openai

import (
	"context"
	"strings"

	"github.com/openai/openai-go/v3/responses"
	"github.com/samber/oops"
)

// Server-Sent-Events type discriminators the Responses streaming API stamps on the
// events streamResponses cares about. The API emits many more event types (item
// lifecycle, tool calls, audio); these four are the ones that carry the assembled
// reply, the reasoning summary prose, and the terminal status this layer needs.
const (
	// streamEventOutputTextDelta carries one chunk of the structured reply text.
	streamEventOutputTextDelta = "response.output_text.delta"
	// streamEventReasoningSummaryDelta carries one chunk of the reasoning summary prose.
	streamEventReasoningSummaryDelta = "response.reasoning_summary_text.delta"
	// streamEventCompleted is the terminal event for a reply that fit the budget.
	streamEventCompleted = "response.completed"
	// streamEventIncomplete is the terminal event for a reply the model could not finish.
	streamEventIncomplete = "response.incomplete"
)

// streamResponses runs one Responses request in streaming mode and assembles the
// reply from its Server-Sent-Events: it accumulates output_text deltas into output
// and reasoning_summary deltas into reasoning, invoking onReasoning per reasoning
// delta when it is non-nil so a UI can render thinking as it arrives. role names
// the calling layer (controller, sub, judge) so the two failure modes surface under
// role-scoped oops codes: a transport or protocol failure becomes role+"_call_failed"
// and a reply the model could not finish — gpt-5.5 is a reasoning model whose hidden
// reasoning is part of the response — becomes role+"_incomplete", the truncation guard
// every role shares. The request sets no max_output_tokens (output is unbounded), so
// this fires only when a reply overruns the model's own response limit or a server-
// side stop; the reported reason names which. The accumulated output is returned even
// though the caller re-decodes it, so the streaming seam stays decode-agnostic.
func (client *Client) streamResponses(
	ctx context.Context,
	params *responses.ResponseNewParams,
	onReasoning func(string),
	role string,
) (output, reasoning string, err error) {
	stream := client.api.Responses.NewStreaming(ctx, *params)

	// Closing the SSE body releases the underlying connection. Surface a close
	// failure only when the stream itself succeeded, so a real streaming error is
	// never masked by a secondary close error on the way out.
	defer func() {
		if closeErr := stream.Close(); closeErr != nil && err == nil {
			err = oops.
				In("openai").
				Code(role+"_close_failed").
				Wrapf(closeErr, "close %s response stream", role)
		}
	}()

	var (
		outputText    strings.Builder
		reasoningText strings.Builder
		final         responses.Response
		truncated     bool
	)

	for stream.Next() {
		event := stream.Current()

		switch event.Type {
		case streamEventOutputTextDelta:
			outputText.WriteString(event.Delta)
		case streamEventReasoningSummaryDelta:
			reasoningText.WriteString(event.Delta)

			if onReasoning != nil {
				onReasoning(event.Delta)
			}
		case streamEventCompleted, streamEventIncomplete:
			final = event.Response
			truncated = event.Response.Status == responses.ResponseStatusIncomplete
		}
	}

	if streamErr := stream.Err(); streamErr != nil {
		return "", "", oops.
			In("openai").
			Code(role+"_call_failed").
			Wrapf(streamErr, "%s responses stream on model %s", role, Model)
	}

	if truncated {
		return "", "", oops.
			In("openai").
			Code(role+"_incomplete").
			Errorf("%s reply truncated (reason %q): the model could not finish the response",
				role, final.IncompleteDetails.Reason)
	}

	return outputText.String(), reasoningText.String(), nil
}
