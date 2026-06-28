package rlm_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/journal"
	"github.com/omarluq/anamnesis/internal/ana/repl"
	"github.com/omarluq/anamnesis/internal/ana/repl/repltest"
	"github.com/omarluq/anamnesis/internal/ana/rlm"
	"github.com/omarluq/anamnesis/internal/ana/scenarios"
	"github.com/omarluq/anamnesis/internal/openai"
	"github.com/omarluq/anamnesis/internal/terminal"
)

// citableEntry is a fully-populated journal entry whose cursor a test can record
// visible before the controller cites it, grounding the §7/§10 citation check.
func citableEntry(cursor string) journal.Entry {
	return journal.Entry{
		Timestamp: time.Time{},
		Cursor:    cursor,
		BootID:    "boot-1",
		Unit:      "checkout-api.service",
		Comm:      "checkout-api",
		Hostname:  "host-1",
		Message:   "Out of memory: Killed process",
		Priority:  2,
		PID:       4242,
	}
}

// investigateTraceBuffer sizes the test trace channel so a whole run's events —
// each turn's thinking plus its code-start/code-end pair, and every sub-call's
// query-start/query-end — queue without the synchronous emitter ever blocking on a
// full buffer.
const investigateTraceBuffer = 32

// TestInvestigateRunsTwoTurnInvestigation is the RLM-12 acceptance test: it drives
// rlm.Investigate through a two-turn investigation assembled entirely from mock
// model seams and mock host surfaces over a real interpreter, citation store, and
// emitter. With MaxDepth 0 every agent.Query is the flat base-case sub-call, so
// this pins the depth-0 leaf path: turn 0 queries the journal and prints the match
// count; turn 1 summarizes the matched entries through a base-case sub-call, cites
// them, and signals agent.FINAL; turn 2 reports done. It proves the run resolves
// the sub-LLM answer as the final answer, grounds the citation against the recorded
// query, and emits the turn, sub-call, and final trace events in order on the
// channel.
func TestInvestigateRunsTwoTurnInvestigation(t *testing.T) {
	t.Parallel()

	const (
		question  = "why did checkout-api crash?"
		subPrompt = "summarize the checkout-api failures"
		answer    = "memory pressure killed checkout-api after a leak"
	)

	entries := []journal.Entry{citableEntry("s=cur-oom-1"), citableEntry("s=cur-oom-2")}

	controllerLLM := new(mockControllerLLM)
	sub := new(mockSubLLM)
	judge := new(mockJudger)
	journalHost := new(repltest.MockJournal)
	systemdHost := new(repltest.MockSystemd)
	events := make(chan terminal.TraceEvent, investigateTraceBuffer)

	queryCode := "entries := journal.Query(&journal.QueryFilter{Unit: \"checkout-api.service\"})\n" +
		"fmt.Println(len(entries))"
	finalCode := "summary := agent.Query(\"" + subPrompt + "\", entries)\n" +
		"agent.Cite(entries)\n" +
		"agent.FINAL(summary)"

	turn0 := openai.ControllerResponse{Thinking: "inspect the matching entries", Code: queryCode, Done: false}
	turn1 := openai.ControllerResponse{Thinking: "summarize then conclude", Code: finalCode, Done: false}
	turn2 := openai.ControllerResponse{Thinking: "wrap up the investigation", Code: "", Done: true}

	scriptControllerTurns(controllerLLM, question, turn0, turn1, turn2)
	journalHost.On("Query", mock.Anything).Return(entries).Once()
	sub.On("Answer", mock.Anything, subPrompt, mock.Anything).Return(answer, nil).Once()
	judge.On("Judge", mock.Anything, question, answer, mock.Anything).Return("", nil).Once()

	deps := rlm.Deps{
		Controller:  controllerLLM,
		Sub:         sub,
		Judge:       judge,
		Journal:     journalHost,
		Systemd:     systemdHost,
		Events:      events,
		RunID:       fixtureRunID,
		MaxDepth:    0,
		MaxSubCalls: repl.DefaultMaxSubCalls,
	}

	got, err := rlm.Investigate(context.Background(), question, &deps)
	require.NoError(t, err)
	assert.Equal(t, answer, got)

	assertTraceSequence(t, events, turn0.Thinking, subPrompt, answer, turn1.Thinking, answer)

	controllerLLM.AssertNumberOfCalls(t, "Respond", 3)
	controllerLLM.AssertExpectations(t)
	sub.AssertExpectations(t)
	judge.AssertExpectations(t)
	journalHost.AssertExpectations(t)
}

// TestInvestigateSurfacesControllerFailure proves a failed controller call aborts
// the run cleanly: Investigate returns the wrapped error, yields no answer, and
// emits no final trace event, so a half-finished run never publishes a terminal
// answer to the shell.
func TestInvestigateSurfacesControllerFailure(t *testing.T) {
	t.Parallel()

	const question = "why did checkout-api crash?"

	controllerLLM := new(mockControllerLLM)
	events := make(chan terminal.TraceEvent, investigateTraceBuffer)
	stalled := openai.ControllerResponse{Thinking: "", Code: "", Done: false}

	controllerLLM.
		On("Respond", mock.Anything, scenarios.ControllerSystemPrompt, question, mock.Anything).
		Return(stalled, errors.New("controller offline")).
		Once()

	deps := rlm.Deps{
		Controller:  controllerLLM,
		Sub:         new(mockSubLLM),
		Judge:       new(mockJudger),
		Journal:     new(repltest.MockJournal),
		Systemd:     new(repltest.MockSystemd),
		Events:      events,
		RunID:       fixtureRunID,
		MaxDepth:    0,
		MaxSubCalls: repl.DefaultMaxSubCalls,
	}

	got, err := rlm.Investigate(context.Background(), question, &deps)
	require.Error(t, err)
	assert.Empty(t, got)
	require.ErrorContains(t, err, "controller turn request")

	assert.Empty(t, events, "a failed run emits no final trace event")
	controllerLLM.AssertExpectations(t)
}

// scriptControllerTurns programs the controller mock to return the given responses
// in order across successive turns, matching the SPEC §14 system prompt and the
// question while leaving the rendered history free so the real interpreter's
// per-turn output need not be predicted.
func scriptControllerTurns(
	controllerLLM *mockControllerLLM,
	question string,
	turns ...openai.ControllerResponse,
) {
	for index := range turns {
		controllerLLM.
			On("Respond", mock.Anything, scenarios.ControllerSystemPrompt, question, mock.Anything).
			Return(turns[index], nil).
			Once()
	}
}

// assertTraceSequence drains a run's trace events and asserts the key lifecycle
// events arrive in execution order amid the per-turn code blocks: the first turn's
// thinking, the second turn's thinking, then the start and end of the sub-call the
// second turn's code fanned out, and finally the answer — each stamped with the run
// ID. recordTurn streams a turn's thinking before its code runs, so a sub-call's
// query events follow their own turn's thinking rather than preceding it. The root
// controller's sub-call renders at depth 1 — one level under its depth-0 turn —
// since a sub-call indents below the code block that spawned it; recursive child
// sub-calls would carry deeper levels.
func assertTraceSequence(
	t *testing.T,
	events <-chan terminal.TraceEvent,
	thinking0, queryStart, queryEnd, thinking1, final string,
) {
	t.Helper()

	want := []struct {
		kind terminal.TraceKind
		text string
	}{
		{terminal.TraceKindThinking, thinking0},
		{terminal.TraceKindThinking, thinking1},
		{terminal.TraceKindQueryStart, queryStart},
		{terminal.TraceKindQueryEnd, queryEnd},
		{terminal.TraceKindFinal, final},
	}

	cursor, codeStarts, codeEnds := 0, 0, 0

	for _, event := range drainTraceEvents(t, events) {
		assert.Equal(t, fixtureRunID, event.RunID, "every event carries the run ID")

		switch event.Kind {
		case terminal.TraceKindCodeStart:
			codeStarts++
		case terminal.TraceKindCodeEnd:
			codeEnds++
		case terminal.TraceKindQueryStart, terminal.TraceKindQueryEnd:
			assert.Equal(t, 1, event.Depth, "the root sub-call renders one level under its depth-0 turn")
		case terminal.TraceKindThinkingDelta, terminal.TraceKindThinking,
			terminal.TraceKindJudgeStart, terminal.TraceKindJudgeEnd, terminal.TraceKindFinal:
		}

		if cursor < len(want) && event.Kind == want[cursor].kind && event.Text == want[cursor].text {
			cursor++
		}
	}

	assert.Equal(t, len(want), cursor, "the key trace events arrive in execution order amid the code blocks")
	assert.Equal(t, 2, codeStarts, "each turn with code opens a code block")
	assert.Equal(t, 2, codeEnds, "each code block settles with its output")
}

// drainTraceEvents collects every event currently buffered on events, stopping once
// the channel is momentarily empty — safe because the run under test has already
// returned, so no further events are coming. It fails fast when no event is ready,
// catching a regression that drops the whole stream.
func drainTraceEvents(t *testing.T, events <-chan terminal.TraceEvent) []terminal.TraceEvent {
	t.Helper()

	drained := make([]terminal.TraceEvent, 0)

	for {
		select {
		case event := <-events:
			drained = append(drained, event)
		default:
			require.NotEmpty(t, drained, "the run emitted no trace events")

			return drained
		}
	}
}
