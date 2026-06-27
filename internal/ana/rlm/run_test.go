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
	"github.com/omarluq/anamnesis/internal/ana/rlm"
	"github.com/omarluq/anamnesis/internal/ana/scenarios"
	"github.com/omarluq/anamnesis/internal/ana/systemd"
	"github.com/omarluq/anamnesis/internal/openai"
	"github.com/omarluq/anamnesis/internal/terminal"
)

// investigateTraceBuffer sizes the test trace channel so a whole run's events
// queue without the synchronous emitter ever blocking on a full buffer.
const investigateTraceBuffer = 16

// mockJournalHost is a testify mock of the repl.Journal host surface the assembled
// investigation exposes to interpreted code. Investigate decorates it with the
// visibility recorder, so this mock scripts only the raw query results.
type mockJournalHost struct {
	mock.Mock
}

// Boots replays the []journal.BootInfo scripted via .On("Boots").Return(boots).
func (m *mockJournalHost) Boots() []journal.BootInfo {
	args := m.Called()

	boots, ok := args.Get(0).([]journal.BootInfo)
	if !ok {
		return nil
	}

	return boots
}

// Query records filter and replays the []journal.Entry scripted via
// .On("Query", filter).Return(entries).
func (m *mockJournalHost) Query(filter *journal.QueryFilter) []journal.Entry {
	args := m.Called(filter)

	entries, ok := args.Get(0).([]journal.Entry)
	if !ok {
		return nil
	}

	return entries
}

// Counts records its arguments and replays the histogram scripted via
// .On("Counts", bootID, byField).Return(counts).
func (m *mockJournalHost) Counts(bootID, byField string) map[string]int {
	args := m.Called(bootID, byField)

	counts, ok := args.Get(0).(map[string]int)
	if !ok {
		return nil
	}

	return counts
}

// Unique records its arguments and replays the values scripted via
// .On("Unique", field, filter).Return(values).
func (m *mockJournalHost) Unique(field string, filter *journal.QueryFilter) []string {
	args := m.Called(field, filter)

	values, ok := args.Get(0).([]string)
	if !ok {
		return nil
	}

	return values
}

// mockSystemdHost is a testify mock of the repl.Systemd host surface. The assembled
// investigation requires a non-nil surface even when a run never reads systemd, so
// it stands in unprogrammed for the journal-only scenarios.
type mockSystemdHost struct {
	mock.Mock
}

// UnitStatus records name and replays the systemd.UnitStatus scripted via
// .On("UnitStatus", name).Return(status).
func (m *mockSystemdHost) UnitStatus(name string) systemd.UnitStatus {
	args := m.Called(name)

	status, ok := args.Get(0).(systemd.UnitStatus)
	if !ok {
		var zero systemd.UnitStatus

		return zero
	}

	return status
}

// ListUnits records state and replays the []systemd.Unit scripted via
// .On("ListUnits", state).Return(units).
func (m *mockSystemdHost) ListUnits(state string) []systemd.Unit {
	args := m.Called(state)

	units, ok := args.Get(0).([]systemd.Unit)
	if !ok {
		return nil
	}

	return units
}

// Compile-time assertions that the mocks satisfy the host surfaces they double.
var (
	_ repl.Journal = (*mockJournalHost)(nil)
	_ repl.Systemd = (*mockSystemdHost)(nil)
)

// TestInvestigateRunsTwoTurnInvestigation is the RLM-12 acceptance test: it drives
// rlm.Investigate through a two-turn investigation assembled entirely from mock
// model seams and mock host surfaces over a real interpreter, citation store, and
// emitter. Turn 0 queries the journal and prints the match count; turn 1
// summarizes the matched entries through a recursive sub-call, cites them, and
// signals agent.FINAL; turn 2 reports done. It proves the run resolves the sub-LLM
// answer as the final answer, grounds the citation against the recorded query, and
// emits the turn, sub-call, and final trace events in order on the channel.
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
	journalHost := new(mockJournalHost)
	systemdHost := new(mockSystemdHost)
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
		Controller: controllerLLM,
		Sub:        sub,
		Judge:      judge,
		Journal:    journalHost,
		Systemd:    systemdHost,
		Events:     events,
		RunID:      fixtureRunID,
	}

	got, err := rlm.Investigate(context.Background(), question, &deps)
	require.NoError(t, err)
	assert.Equal(t, answer, got)

	assertTraceSequence(t, events, turn0.Thinking, subPrompt, turn1.Thinking, answer)

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
		Controller: controllerLLM,
		Sub:        new(mockSubLLM),
		Judge:      new(mockJudger),
		Journal:    new(mockJournalHost),
		Systemd:    new(mockSystemdHost),
		Events:     events,
		RunID:      fixtureRunID,
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

// assertTraceSequence drains the run's four trace events and asserts they arrive in
// the expected order — the first turn, the sub-call fanned out from the second
// turn, the second turn, and the final answer — each stamped with the run ID.
func assertTraceSequence(t *testing.T, events <-chan terminal.TraceEvent, turn0, subCall, turn1, final string) {
	t.Helper()

	assertTraceEvent(t, receiveTraceEvent(t, events), terminal.TraceKindTurn, turn0)
	assertTraceEvent(t, receiveTraceEvent(t, events), terminal.TraceKindSubCall, subCall)
	assertTraceEvent(t, receiveTraceEvent(t, events), terminal.TraceKindTurn, turn1)
	assertTraceEvent(t, receiveTraceEvent(t, events), terminal.TraceKindFinal, final)
}

// receiveTraceEvent reads the next trace event off events, failing the test fast
// instead of blocking until the package deadline when a regression drops an event.
func receiveTraceEvent(t *testing.T, events <-chan terminal.TraceEvent) terminal.TraceEvent {
	t.Helper()

	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for trace event")
	}

	var zero terminal.TraceEvent

	return zero
}

// assertTraceEvent asserts one drained event carries the expected kind and text and
// is stamped with the fixture run ID.
func assertTraceEvent(t *testing.T, event terminal.TraceEvent, kind terminal.TraceKind, text string) {
	t.Helper()

	assert.Equal(t, kind, event.Kind)
	assert.Equal(t, text, event.Text)
	assert.Equal(t, fixtureRunID, event.RunID)
}
