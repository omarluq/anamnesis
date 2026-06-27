package repl_test

import (
	"testing"
	"time"

	"github.com/samber/oops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/journal"
	"github.com/omarluq/anamnesis/internal/ana/repl"
	"github.com/omarluq/anamnesis/internal/ana/systemd"
)

// unitSSH is the unit name the test queries and statuses; hoisted to a constant
// so the surrounding mock setup and assertions share one literal.
const unitSSH = "ssh.service"

// mockJournalSurface is a testify mock of the repl.Journal host surface. Every
// method is a recorded seam scripted with .On(...).Return(...); the REPL bridge
// reflects the interface's methods into journal.Query and friends, so a testify
// mock is the right double rather than a bespoke fake.
type mockJournalSurface struct {
	mock.Mock
}

// Boots replays the []journal.BootInfo scripted via .On("Boots").Return(boots).
func (m *mockJournalSurface) Boots() []journal.BootInfo {
	args := m.Called()

	boots, ok := args.Get(0).([]journal.BootInfo)
	if !ok {
		return nil
	}

	return boots
}

// Query records filter and replays the []journal.Entry scripted via
// .On("Query", filter).Return(entries).
func (m *mockJournalSurface) Query(filter *journal.QueryFilter) []journal.Entry {
	args := m.Called(filter)

	entries, ok := args.Get(0).([]journal.Entry)
	if !ok {
		return nil
	}

	return entries
}

// Counts records its arguments and replays the histogram scripted via
// .On("Counts", bootID, byField).Return(counts).
func (m *mockJournalSurface) Counts(bootID, byField string) map[string]int {
	args := m.Called(bootID, byField)

	counts, ok := args.Get(0).(map[string]int)
	if !ok {
		return nil
	}

	return counts
}

// Unique records its arguments and replays the values scripted via
// .On("Unique", field, filter).Return(values).
func (m *mockJournalSurface) Unique(field string, filter *journal.QueryFilter) []string {
	args := m.Called(field, filter)

	values, ok := args.Get(0).([]string)
	if !ok {
		return nil
	}

	return values
}

// mockSystemdSurface is a testify mock of the repl.Systemd host surface. The
// bridge reflects its methods into systemd.UnitStatus and systemd.ListUnits, so
// the test scripts them with .On(...).Return(...).
type mockSystemdSurface struct {
	mock.Mock
}

// UnitStatus records name and replays the systemd.UnitStatus scripted via
// .On("UnitStatus", name).Return(status).
func (m *mockSystemdSurface) UnitStatus(name string) systemd.UnitStatus {
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
func (m *mockSystemdSurface) ListUnits(state string) []systemd.Unit {
	args := m.Called(state)

	units, ok := args.Get(0).([]systemd.Unit)
	if !ok {
		return nil
	}

	return units
}

// Compile-time assertions that the mocks satisfy the host surfaces they double.
var (
	_ repl.Journal = (*mockJournalSurface)(nil)
	_ repl.Systemd = (*mockSystemdSurface)(nil)
)

// TestHostDepsRegistersJournalAndSystemd builds a HostDeps from mock journal and
// systemd surfaces, registers them on a fresh interpreter, then evaluates
// controller-style source that queries the journal, ranges the returned
// []journal.Entry reading Message, Priority and Timestamp.Year(), and reads an
// ActiveState off systemd.UnitStatus. It proves the injected real surfaces cross
// the interpreter boundary intact: both rows and the unit state reach stdout and
// len(entries) crosses back as the turn's retval of 2.
func TestHostDepsRegistersJournalAndSystemd(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	journalSurface := new(mockJournalSurface)
	journalSurface.On("Query", mock.Anything).Return([]journal.Entry{
		{
			Timestamp: time.Date(2021, time.March, 1, 9, 0, 0, 0, time.UTC),
			Cursor:    "s=cursor-accepted",
			BootID:    "boot-current",
			Unit:      unitSSH,
			Comm:      "sshd",
			Hostname:  "host-a",
			Message:   "Accepted password for root",
			Priority:  6,
			PID:       1000,
		},
		{
			Timestamp: time.Date(2021, time.March, 1, 9, 0, 1, 0, time.UTC),
			Cursor:    "s=cursor-failed",
			BootID:    "boot-current",
			Unit:      unitSSH,
			Comm:      "sshd",
			Hostname:  "host-a",
			Message:   "Failed password for root",
			Priority:  3,
			PID:       1001,
		},
	})

	systemdSurface := new(mockSystemdSurface)
	systemdSurface.On("UnitStatus", unitSSH).Return(systemd.UnitStatus{
		Name:        unitSSH,
		Description: "OpenSSH server daemon",
		LoadState:   "loaded",
		ActiveState: "active",
		SubState:    "running",
		MainPID:     1000,
	})

	deps := repl.HostDeps{Journal: journalSurface, Systemd: systemdSurface}

	err := deps.Register(interpreter)
	require.NoError(t, err)

	const src = `entries := journal.Query(&journal.QueryFilter{Unit: "ssh.service"})
for _, e := range entries {
	fmt.Printf("%s pri=%d year=%d\n", e.Message, e.Priority, e.Timestamp.Year())
}
status := systemd.UnitStatus("ssh.service")
fmt.Println("state=" + status.ActiveState)
len(entries)`

	result, err := interpreter.Eval("turn_0", src)
	require.NoError(t, err)

	assert.Contains(t, result.Stdout, "Accepted password for root pri=6 year=2021")
	assert.Contains(t, result.Stdout, "Failed password for root pri=3 year=2021")
	assert.Contains(t, result.Stdout, "state=active")

	require.True(t, result.Retval.IsValid(), "len(entries) crosses back as the turn retval")
	assert.Equal(t, int64(2), result.Retval.Int())

	journalSurface.AssertExpectations(t)
	systemdSurface.AssertExpectations(t)
	journalSurface.AssertCalled(t, "Query", mock.Anything)
	systemdSurface.AssertCalled(t, "UnitStatus", unitSSH)
}

// TestHostDepsRegisterReportsNilSurface proves HostDeps.Register propagates the
// surface-registration error rather than swallowing it: a HostDeps whose journal
// surface is nil makes Register return an oops error in the repl domain, exercising
// the error branch that short-circuits before systemd registration.
func TestHostDepsRegisterReportsNilSurface(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	deps := repl.HostDeps{Journal: nil, Systemd: new(mockSystemdSurface)}

	err := deps.Register(interpreter)
	require.Error(t, err)

	var oopsErr oops.OopsError

	require.ErrorAs(t, err, &oopsErr)
	assert.Equal(t, "repl", oopsErr.Domain())
	assert.Equal(t, "host_surface_nil", oopsErr.Code())
}

// TestHostDepsExposesFullMethodSet proves the reflection-based registration exposes
// every method of each surface, not only the ones other tests touch: it drives
// journal.Boots and systemd.ListUnits from interpreted code and asserts both results
// reach stdout, so a registration that dropped one of those methods would be caught.
func TestHostDepsExposesFullMethodSet(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	journalSurface := new(mockJournalSurface)
	journalSurface.On("Boots").Return([]journal.BootInfo{
		{
			FirstSeen: time.Date(2021, time.March, 1, 9, 0, 0, 0, time.UTC),
			LastSeen:  time.Date(2021, time.March, 1, 10, 0, 0, 0, time.UTC),
			ID:        "boot-listed",
			Index:     0,
		},
	})

	systemdSurface := new(mockSystemdSurface)
	systemdSurface.On("ListUnits", "").Return([]systemd.Unit{
		{
			Name:        "cron.service",
			Description: "Regular background program processing daemon",
			LoadState:   "masked",
			ActiveState: "inactive",
			SubState:    "dead",
		},
	})

	deps := repl.HostDeps{Journal: journalSurface, Systemd: systemdSurface}
	require.NoError(t, deps.Register(interpreter))

	const src = `boots := journal.Boots()
fmt.Printf("boot=%s\n", boots[0].ID)
units := systemd.ListUnits("")
fmt.Printf("unit=%s state=%s\n", units[0].Name, units[0].ActiveState)
len(boots) + len(units)`

	result, err := interpreter.Eval("turn_0", src)
	require.NoError(t, err)

	assert.Contains(t, result.Stdout, "boot=boot-listed")
	assert.Contains(t, result.Stdout, "unit=cron.service state=inactive")

	require.True(t, result.Retval.IsValid(), "len(boots)+len(units) crosses back as the turn retval")
	assert.Equal(t, int64(2), result.Retval.Int())

	journalSurface.AssertExpectations(t)
	systemdSurface.AssertExpectations(t)
}
