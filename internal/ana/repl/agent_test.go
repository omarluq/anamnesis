package repl_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/journal"
	"github.com/omarluq/anamnesis/internal/ana/repl"
)

// mockCitationSink is a testify mock of the repl.CitationSink seam. agent.Cite
// forwards the entries it is handed straight to this sink, so the test scripts
// Cite with .On("Cite", ...).Return() and the built-in recorder proves the
// interpreted call reached the host with the slice intact — making a testify
// mock the right double rather than a bespoke fake.
type mockCitationSink struct {
	mock.Mock
}

// Cite records the entries it was forwarded so the test can assert the call.
func (m *mockCitationSink) Cite(entries []journal.Entry) {
	m.Called(entries)
}

// compile-time assertion that mockCitationSink satisfies the CitationSink seam.
var _ repl.CitationSink = (*mockCitationSink)(nil)

// citedEntry builds a fully-populated journal entry the Cite test hands to the
// agent. Only cursor and message vary per call; the constant fields keep their
// literals to a single occurrence so the round-trip stays unambiguous.
func citedEntry(cursor, message string) journal.Entry {
	return journal.Entry{
		Timestamp: time.Date(2021, time.March, 1, 9, 0, 0, 0, time.UTC),
		Cursor:    cursor,
		BootID:    "boot-cite",
		Unit:      unitSSH,
		Comm:      "sshd-cite",
		Hostname:  "host-cite",
		Message:   message,
		Priority:  3,
		PID:       4242,
	}
}

// TestAgentFinalRecordsLiteralAnswer registers the agent surface, evaluates
// agent.FINAL with a literal answer, and proves Interpreter.Final crosses that
// literal back to the host as the terminal answer with its ok flag set.
func TestAgentFinalRecordsLiteralAnswer(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()
	sink := new(mockCitationSink)
	repl.RegisterAgent(interpreter, sink)

	pendingAnswer, pending := interpreter.Final()
	require.False(t, pending, "a fresh agent has signaled no terminal answer")
	assert.Empty(t, pendingAnswer)

	_, err := interpreter.Eval("turn_0", `agent.FINAL("done")`)
	require.NoError(t, err)

	answer, ok := interpreter.Final()
	require.True(t, ok, "FINAL signals a terminal answer is ready")
	assert.Equal(t, "done", answer)

	sink.AssertNotCalled(t, "Cite")
}

// TestAgentFinalVarResolvesVariableBuiltAcrossTurns binds a variable in one turn
// and signals agent.FINAL_VAR against it in the next, proving Interpreter.Final
// reads the variable's current value back from the persistent session state, so
// an answer assembled across turns resolves to that variable's final string.
func TestAgentFinalVarResolvesVariableBuiltAcrossTurns(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()
	sink := new(mockCitationSink)
	repl.RegisterAgent(interpreter, sink)

	_, err := interpreter.Eval("turn_0", `x := "built across turns"`)
	require.NoError(t, err)

	_, err = interpreter.Eval("turn_1", `agent.FINAL_VAR("x")`)
	require.NoError(t, err)

	answer, ok := interpreter.Final()
	require.True(t, ok, "FINAL_VAR signals a terminal answer is ready")
	assert.Equal(t, "built across turns", answer)
}

// TestAgentCiteForwardsEntriesToSink registers the real journal surface and the
// agent surface, evaluates controller-style source that queries the journal and
// hands the resulting []journal.Entry to agent.Cite, and proves the slice
// crosses the interpreter boundary into the citation sink intact.
func TestAgentCiteForwardsEntriesToSink(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	entries := []journal.Entry{
		citedEntry("s=cursor-cite-1", "Out of memory: Killed process"),
		citedEntry("s=cursor-cite-2", "checkout-api main process exited"),
	}

	journalSurface := new(mockJournalSurface)
	journalSurface.On("Query", mock.Anything).Return(entries)

	deps := repl.HostDeps{Journal: journalSurface, Systemd: new(mockSystemdSurface)}
	require.NoError(t, deps.Register(interpreter))

	sink := new(mockCitationSink)
	sink.On("Cite", entries).Return()
	repl.RegisterAgent(interpreter, sink)

	const src = `cited := journal.Query(&journal.QueryFilter{Unit: "ssh.service"})
agent.Cite(cited)
len(cited)`

	result, err := interpreter.Eval("turn_0", src)
	require.NoError(t, err)
	require.True(t, result.Retval.IsValid())
	assert.Equal(t, int64(2), result.Retval.Int())

	sink.AssertExpectations(t)
	sink.AssertCalled(t, "Cite", entries)
	journalSurface.AssertCalled(t, "Query", mock.Anything)
}

// TestAgentFinalVarDegradesWhenVariableUnresolvable proves a FINAL_VAR whose name
// cannot be evaluated degrades to "no answer" rather than panicking. The mvm engine
// resolves a bare undefined identifier shell-style to its own name, so the
// degradation guard fires on an expression that genuinely faults — here a selector
// on an unbound identifier — and Interpreter.Final reports the empty string and false.
func TestAgentFinalVarDegradesWhenVariableUnresolvable(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()
	sink := new(mockCitationSink)
	repl.RegisterAgent(interpreter, sink)

	_, err := interpreter.Eval("turn_0", `agent.FINAL_VAR("missingResult.field")`)
	require.NoError(t, err)

	answer, ok := interpreter.Final()
	assert.False(t, ok, "a FINAL_VAR that cannot be resolved signals no answer")
	assert.Empty(t, answer)

	sink.AssertNotCalled(t, "Cite")
}

// TestAgentFinalVarRendersNonStringVariable proves FINAL_VAR resolves a variable
// holding a non-string value by rendering it with fmt.Sprint: an int variable bound
// to 42 in one turn crosses back as the string "42".
func TestAgentFinalVarRendersNonStringVariable(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()
	sink := new(mockCitationSink)
	repl.RegisterAgent(interpreter, sink)

	_, err := interpreter.Eval("turn_0", `n := 42`)
	require.NoError(t, err)

	_, err = interpreter.Eval("turn_1", `agent.FINAL_VAR("n")`)
	require.NoError(t, err)

	answer, ok := interpreter.Final()
	require.True(t, ok, "FINAL_VAR resolves a non-string variable")
	assert.Equal(t, "42", answer)
}

// TestFinalWithoutAgentReportsNoAnswer proves Interpreter.Final returns no answer
// when no agent was ever registered on the interpreter — the agentBindings miss
// path — rather than faulting on the absent binding.
func TestFinalWithoutAgentReportsNoAnswer(t *testing.T) {
	t.Parallel()

	answer, ok := repl.NewInterpreter().Final()
	assert.False(t, ok, "an interpreter with no agent registered signals no answer")
	assert.Empty(t, answer)
}
