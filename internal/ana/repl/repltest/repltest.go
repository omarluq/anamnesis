// Package repltest provides shared test doubles for the repl host surfaces. The
// interpreter bridge reflects the repl.Journal and repl.Systemd interfaces into the
// journal and systemd host packages interpreted code calls, so a testify mock is the
// right double for both the repl package's own tests and the rlm package's
// investigation tests. Hoisting the two mocks here keeps the byte-identical surface
// definitions in one place rather than duplicated across packages.
package repltest

import (
	"testing"

	"github.com/samber/oops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/journal"
	"github.com/omarluq/anamnesis/internal/ana/repl"
	"github.com/omarluq/anamnesis/internal/ana/systemd"
)

// MockJournal is a testify mock of the repl.Journal host surface. Every method is a
// recorded seam scripted with .On(...).Return(...); the REPL bridge reflects the
// interface's methods into journal.Query and friends, so a testify mock is the right
// double rather than a bespoke fake.
type MockJournal struct {
	mock.Mock
}

// Boots replays the []journal.BootInfo scripted via .On("Boots").Return(boots).
func (m *MockJournal) Boots() []journal.BootInfo {
	args := m.Called()

	boots, ok := args.Get(0).([]journal.BootInfo)
	if !ok {
		return nil
	}

	return boots
}

// Query records filter and replays the []journal.Entry scripted via
// .On("Query", filter).Return(entries).
func (m *MockJournal) Query(filter *journal.QueryFilter) []journal.Entry {
	args := m.Called(filter)

	entries, ok := args.Get(0).([]journal.Entry)
	if !ok {
		return nil
	}

	return entries
}

// Counts records its arguments and replays the histogram scripted via
// .On("Counts", bootID, byField).Return(counts).
func (m *MockJournal) Counts(bootID, byField string) map[string]int {
	args := m.Called(bootID, byField)

	counts, ok := args.Get(0).(map[string]int)
	if !ok {
		return nil
	}

	return counts
}

// Unique records its arguments and replays the values scripted via
// .On("Unique", field, filter).Return(values).
func (m *MockJournal) Unique(field string, filter *journal.QueryFilter) []string {
	args := m.Called(field, filter)

	values, ok := args.Get(0).([]string)
	if !ok {
		return nil
	}

	return values
}

// MockSystemd is a testify mock of the repl.Systemd host surface. The bridge reflects
// its methods into systemd.UnitStatus and systemd.ListUnits, so the test scripts them
// with .On(...).Return(...).
type MockSystemd struct {
	mock.Mock
}

// UnitStatus records name and replays the systemd.UnitStatus scripted via
// .On("UnitStatus", name).Return(status).
func (m *MockSystemd) UnitStatus(name string) systemd.UnitStatus {
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
func (m *MockSystemd) ListUnits(state string) []systemd.Unit {
	args := m.Called(state)

	units, ok := args.Get(0).([]systemd.Unit)
	if !ok {
		return nil
	}

	return units
}

// Compile-time assertions that the mocks satisfy the host surfaces they double.
var (
	_ repl.Journal = (*MockJournal)(nil)
	_ repl.Systemd = (*MockSystemd)(nil)
)

// RequireOopsCode asserts that err carries an oops error tagged with the given domain
// and code, the repeated ErrorAs-then-Domain-then-Code triad the repl and rlm tests
// check on every wrapped fault. It fails the test if err is not an oops error, and
// reports a domain or code mismatch.
func RequireOopsCode(t *testing.T, err error, domain, code string) {
	t.Helper()

	var oopsErr oops.OopsError

	require.ErrorAs(t, err, &oopsErr)
	assert.Equal(t, domain, oopsErr.Domain(), "error domain")
	assert.Equal(t, code, oopsErr.Code(), "error code")
}
