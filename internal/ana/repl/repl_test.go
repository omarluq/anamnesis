package repl_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/journal"
	"github.com/omarluq/anamnesis/internal/ana/repl"
	"github.com/omarluq/anamnesis/internal/ana/repl/repltest"
	"github.com/omarluq/anamnesis/internal/ana/systemd"
)

// authEntry builds a fully-populated journal entry the session integration test
// queries and cites. Only cursor, message and priority vary per call; the constant
// fields keep their literals to a single occurrence so the round-trip stays clear.
func authEntry(cursor, message string, priority int) journal.Entry {
	return journal.Entry{
		Timestamp: time.Date(2021, time.March, 1, 9, 0, 0, 0, time.UTC),
		Cursor:    cursor,
		BootID:    "boot-session",
		Unit:      unitSSH,
		Comm:      "sshd-session",
		Hostname:  "host-session",
		Message:   message,
		Priority:  priority,
		PID:       2200,
	}
}

// TestNewAssemblesSessionForMultiStepInvestigation is the REPL-07 acceptance test:
// it builds a full session through repl.New from testify-mock host surfaces, a mock
// citation sink and a mock sub-LLM seam, then evaluates a multi-step controller
// program — journal.Query, a fmt.Print summary over the entries, a systemd status
// read, agent.Cite of the queried entries, and agent.FINAL — proving the assembled
// session wires every surface: both rows and the unit state reach Result.Stdout,
// the cited slice crosses into the sink intact, len(entries) crosses back as the
// turn retval, and Interpreter.Final resolves the literal terminal answer.
func TestNewAssemblesSessionForMultiStepInvestigation(t *testing.T) {
	t.Parallel()

	entries := []journal.Entry{
		authEntry("s=cursor-session-1", "Accepted password for root", 6),
		authEntry("s=cursor-session-2", "Failed password for root", 3),
	}

	journalSurface := new(repltest.MockJournal)
	journalSurface.On("Query", mock.MatchedBy(func(filter *journal.QueryFilter) bool {
		return filter != nil && filter.Unit == unitSSH
	})).Return(entries)

	systemdSurface := new(repltest.MockSystemd)
	systemdSurface.On("UnitStatus", unitSSH).Return(systemd.UnitStatus{
		Name:        unitSSH,
		Description: "OpenSSH server daemon",
		LoadState:   "loaded",
		ActiveState: "active",
		SubState:    "running",
		MainPID:     2200,
	})

	sink := new(mockCitationSink)
	sink.On("Cite", entries).Return()

	cfg := repl.Config{
		Host:   repl.HostDeps{Journal: journalSurface, Systemd: systemdSurface},
		Sink:   sink,
		Sub:    new(mockSubLLM),
		Budget: fullBudget(),
	}

	session, err := repl.New(&cfg)
	require.NoError(t, err)
	require.NotNil(t, session)

	const src = `entries := journal.Query(&journal.QueryFilter{Unit: "ssh.service"})
for _, e := range entries {
	fmt.Printf("%s pri=%d\n", e.Message, e.Priority)
}
status := systemd.UnitStatus("ssh.service")
fmt.Println("state=" + status.ActiveState)
agent.Cite(entries)
agent.FINAL("ssh: 2 auth events, unit " + status.ActiveState)
len(entries)`

	result, err := session.Eval("turn_0", src)
	require.NoError(t, err)

	assert.Contains(t, result.Stdout, "Accepted password for root pri=6")
	assert.Contains(t, result.Stdout, "Failed password for root pri=3")
	assert.Contains(t, result.Stdout, "state=active")

	require.True(t, result.Retval.IsValid(), "len(entries) crosses back as the turn retval")
	assert.Equal(t, int64(2), result.Retval.Int())

	answer, ok := session.Final()
	require.True(t, ok, "agent.FINAL signals a terminal answer is ready")
	assert.Equal(t, "ssh: 2 auth events, unit active", answer)

	journalSurface.AssertExpectations(t)
	systemdSurface.AssertExpectations(t)
	sink.AssertExpectations(t)
	sink.AssertCalled(t, "Cite", entries)
}

// TestNewWiresSubLLMSeamAndFinalVar proves repl.New also threads the sub-LLM seam
// and the FINAL_VAR terminal: a program drives agent.Query through the assembled
// session, assigns the reply to a REPL variable, prints it, and signals
// agent.FINAL_VAR against that variable. The mock sub-LLM sees the prompt and the
// ctx rendered to evidence, and Interpreter.Final reads the variable's value back.
func TestNewWiresSubLLMSeamAndFinalVar(t *testing.T) {
	t.Parallel()

	sub := new(mockSubLLM)
	sub.On("Sub", "summarize ssh", "[evidence]").Return("ssh is degraded", nil)

	cfg := repl.Config{
		Host:   repl.HostDeps{Journal: new(repltest.MockJournal), Systemd: new(repltest.MockSystemd)},
		Sink:   new(mockCitationSink),
		Sub:    sub,
		Budget: fullBudget(),
	}

	session, err := repl.New(&cfg)
	require.NoError(t, err)

	const src = `summary := agent.Query("summarize ssh", []string{"evidence"})
fmt.Println(summary)
agent.FINAL_VAR("summary")`

	result, err := session.Eval("turn_0", src)
	require.NoError(t, err)
	assert.Contains(t, result.Stdout, "ssh is degraded")

	answer, ok := session.Final()
	require.True(t, ok, "FINAL_VAR signals the assembled answer is ready")
	assert.Equal(t, "ssh is degraded", answer)

	sub.AssertExpectations(t)
	sub.AssertCalled(t, "Sub", "summarize ssh", "[evidence]")
}

// validSessionConfig builds a fully-wired Config from fresh mock collaborators, the
// baseline a rejection-table row mutates to null out exactly one collaborator so
// repl.New is forced down a single validate branch.
func validSessionConfig() repl.Config {
	return repl.Config{
		Host:   repl.HostDeps{Journal: new(repltest.MockJournal), Systemd: new(repltest.MockSystemd)},
		Sink:   new(mockCitationSink),
		Sub:    new(mockSubLLM),
		Budget: fullBudget(),
	}
}

// TestNewRejectsNilConfig proves repl.New rejects a nil Config with the
// session_config_nil oops error rather than dereferencing it, so a caller that
// forgets to build a Config gets a clear construction error.
func TestNewRejectsNilConfig(t *testing.T) {
	t.Parallel()

	session, err := repl.New(nil)
	require.Error(t, err)
	require.Nil(t, session)

	repltest.RequireOopsCode(t, err, "repl", "session_config_nil")
}

// TestNewRejectsUnsetCollaborator proves repl.New fails loudly when any collaborator
// is missing rather than handing the controller a half-wired session that panics
// deep inside an interpreted agent.Cite or agent.Query call. Each row nulls out one
// collaborator — including a zero Budget ceiling, which would otherwise leave reserve
// refusing the first sub-call — and asserts New returns a nil session and an oops
// error whose repl-domain code names the unset collaborator.
func TestNewRejectsUnsetCollaborator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(cfg *repl.Config)
		code   string
	}{
		{
			name:   "unset journal surface",
			mutate: func(cfg *repl.Config) { cfg.Host.Journal = nil },
			code:   "session_journal_unset",
		},
		{
			name:   "unset systemd surface",
			mutate: func(cfg *repl.Config) { cfg.Host.Systemd = nil },
			code:   "session_systemd_unset",
		},
		{
			name:   "unset citation sink",
			mutate: func(cfg *repl.Config) { cfg.Sink = nil },
			code:   "session_sink_unset",
		},
		{
			name:   "unset sub-LLM seam",
			mutate: func(cfg *repl.Config) { cfg.Sub = nil },
			code:   "session_sub_unset",
		},
		{
			name:   "zero budget depth",
			mutate: func(cfg *repl.Config) { cfg.Budget.MaxDepth = 0 },
			code:   "session_budget_depth_unset",
		},
		{
			name:   "zero budget sub-calls",
			mutate: func(cfg *repl.Config) { cfg.Budget.MaxSubCalls = 0 },
			code:   "session_budget_calls_unset",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg := validSessionConfig()
			testCase.mutate(&cfg)

			session, err := repl.New(&cfg)
			require.Error(t, err)
			require.Nil(t, session)

			repltest.RequireOopsCode(t, err, "repl", testCase.code)
		})
	}
}
