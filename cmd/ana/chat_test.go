package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/terminal"
)

const (
	// chatQuery is the prompt the substitute shell runner submits to drive one
	// scripted investigation through the resolved controller.
	chatQuery = "why did the gpu oops on resume"
	// chatRunID is the run identifier the substitute shell runner stamps on the
	// submit, standing in for the live shell's per-run counter.
	chatRunID uint64 = 1
	// chatTurnLine is the reasoning-turn trace line the mock controller replays,
	// proving a real trace turn reaches the drain instead of an echoed input line.
	chatTurnLine = "Turn 1: inspecting the latest boot for failure signatures."
	// chatFinalAnswer is the FINAL answer the mock controller replays for the
	// submitted query.
	chatFinalAnswer = "Root cause: the i915 GPU driver oopsed during resume."
)

// mockChatController is a testify mock of the terminal.Controller seam. The live
// RLM adapter and this mock both satisfy the interface, so runChatWith drives
// either through the same submit path without knowing which it holds.
// Expectations script the trace channel Start replays, and AssertExpectations
// confirms the shell drove Start with the (query, runID) the test scripted.
//
// Controller is a single-method seam, so a testify mock is the clear fit over a
// hand-written stub: .On("Start", ...).Return(channel) scripts the run and the
// built-in call recorder asserts the submit lifecycle with no bespoke
// bookkeeping.
type mockChatController struct {
	mock.Mock
}

// Start records the (query, runID) it was driven with and replays the scripted
// channel configured via .On("Start", ...).Return(channel), so the test both
// asserts the submit lifecycle and feeds scripted trace events back to the drain.
func (m *mockChatController) Start(ctx context.Context, query string, runID uint64) <-chan terminal.TraceEvent {
	args := m.Called(ctx, query, runID)

	channel, ok := args.Get(0).(<-chan terminal.TraceEvent)
	if !ok {
		return nil
	}

	return channel
}

// compile-time assertion that mockChatController satisfies the Controller seam.
var _ terminal.Controller = (*mockChatController)(nil)

// chatTrace builds a fully-populated, token-free TraceEvent of kind carrying text
// at top level, keeping the scripted run readable while satisfying exhaustruct.
func chatTrace(kind terminal.TraceKind, text string) terminal.TraceEvent {
	return terminal.TraceEvent{
		Kind:       kind,
		Text:       text,
		TokensIn:   0,
		TokensOut:  0,
		CostMicros: 0,
		Depth:      0,
		RunID:      0,
	}
}

// scriptedRun returns a closed, buffered channel replaying events stamped with
// runID, standing in for a controller run's trace stream in mock setup.
func scriptedRun(runID uint64, events ...terminal.TraceEvent) <-chan terminal.TraceEvent {
	out := make(chan terminal.TraceEvent, len(events))
	for _, event := range events {
		event.RunID = runID
		out <- event
	}

	close(out)

	return out
}

// TestRunChatWithDrivesControllerToFinal asserts runChatWith hands the resolved
// controller a submit and drains its scripted run to the rendered FINAL: the mock
// replays a reasoning turn and a final answer, and the substitute shell runner —
// standing in for the live terminal so no real screen is opened — collects the
// trace lines and the final text. It proves a chat submit drives the controller
// so a real trace turn and the FINAL answer reach the drain instead of an echoed
// input line.
func TestRunChatWithDrivesControllerToFinal(t *testing.T) {
	t.Parallel()

	controller := new(mockChatController)
	channel := scriptedRun(
		chatRunID,
		chatTrace(terminal.TraceKindTurn, chatTurnLine),
		chatTrace(terminal.TraceKindFinal, chatFinalAnswer),
	)
	controller.On("Start", mock.Anything, chatQuery, chatRunID).Return(channel)

	var (
		lines    []string
		rendered string
	)

	runner := func(ctx context.Context, opts terminal.RunOptions) error {
		for event := range opts.Controller.Start(ctx, chatQuery, chatRunID) {
			lines = append(lines, event.Text)

			if event.Kind == terminal.TraceKindFinal {
				rendered = event.Text
			}
		}

		return nil
	}

	require.NoError(t, runChatWith(context.Background(), controller, runner))
	assert.Equal(t, chatFinalAnswer, rendered, "the FINAL answer reaches the drain")
	assert.Contains(t, lines, chatTurnLine, "a real trace turn reaches the drain, not an echoed line")
	controller.AssertCalled(t, "Start", mock.Anything, chatQuery, chatRunID)
	controller.AssertExpectations(t)
}

// TestResolveChatControllerResolvesFromDI asserts the DI wiring resolves a live
// terminal.Controller from the runtime container with default configuration and
// no network: NewChatController adapts rlm.Investigate behind the seam, so
// resolution returns a usable controller for `ana chat` to drive.
func TestResolveChatControllerResolvesFromDI(t *testing.T) {
	t.Parallel()

	controller, err := resolveChatController("")

	require.NoError(t, err)
	assert.NotNil(t, controller, "the DI container provides the chat controller")
}
