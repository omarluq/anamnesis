package systemd_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/omarluq/anamnesis/internal/ana/systemd"
)

// States shared by the healthy-unit fixtures, hoisted to constants so the
// repeated literals stay in sync.
const (
	loadStateLoaded   = "loaded"
	activeStateActive = "active"
	subStateRunning   = "running"
)

func TestUnitFields(t *testing.T) {
	t.Parallel()

	unit := systemd.Unit{
		Name:        "nginx.service",
		Description: "A high performance web server",
		LoadState:   loadStateLoaded,
		ActiveState: activeStateActive,
		SubState:    subStateRunning,
	}

	assert.Equal(t, "nginx.service", unit.Name)
	assert.Equal(t, "A high performance web server", unit.Description)
	assert.Equal(t, loadStateLoaded, unit.LoadState)
	assert.Equal(t, activeStateActive, unit.ActiveState)
	assert.Equal(t, subStateRunning, unit.SubState)
}

func TestUnitStatusFields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		unitName       string
		description    string
		loadState      string
		activeState    string
		subState       string
		mainPID        int
		hasMainProcess bool
	}{
		{
			name:           "running main process",
			unitName:       "sshd.service",
			description:    "OpenSSH server daemon",
			loadState:      "loaded",
			activeState:    "failed",
			subState:       "dead",
			mainPID:        4242,
			hasMainProcess: true,
		},
		{
			name:           "no running main process",
			unitName:       "cron.service",
			description:    "Regular background program processing daemon",
			loadState:      "masked",
			activeState:    "inactive",
			subState:       "exited",
			mainPID:        0,
			hasMainProcess: false,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			status := systemd.UnitStatus{
				Name:        testCase.unitName,
				Description: testCase.description,
				LoadState:   testCase.loadState,
				ActiveState: testCase.activeState,
				SubState:    testCase.subState,
				MainPID:     testCase.mainPID,
			}

			assert.Equal(t, testCase.unitName, status.Name)
			assert.Equal(t, testCase.description, status.Description)
			assert.Equal(t, testCase.loadState, status.LoadState)
			assert.Equal(t, testCase.activeState, status.ActiveState)
			assert.Equal(t, testCase.subState, status.SubState)
			assert.Equal(t, testCase.mainPID, status.MainPID)

			if testCase.hasMainProcess {
				assert.Positive(t, status.MainPID, "a running main process has a positive MainPID")
			} else {
				assert.Zero(t, status.MainPID, "no running main process means a zero MainPID")
			}
		})
	}
}

// TestUnitStatusExtendsUnitListingFields locks the documented contract that a
// UnitStatus carries the same listing fields as the Unit it details and adds the
// main process identifier on top.
func TestUnitStatusExtendsUnitListingFields(t *testing.T) {
	t.Parallel()

	unit := systemd.Unit{
		Name:        "redis.service",
		Description: "Advanced key-value store",
		LoadState:   loadStateLoaded,
		ActiveState: activeStateActive,
		SubState:    subStateRunning,
	}
	status := systemd.UnitStatus{
		Name:        unit.Name,
		Description: unit.Description,
		LoadState:   unit.LoadState,
		ActiveState: unit.ActiveState,
		SubState:    unit.SubState,
		MainPID:     991,
	}

	assert.Equal(t, unit.Name, status.Name)
	assert.Equal(t, unit.Description, status.Description)
	assert.Equal(t, unit.LoadState, status.LoadState)
	assert.Equal(t, unit.ActiveState, status.ActiveState)
	assert.Equal(t, unit.SubState, status.SubState)
	assert.Positive(t, status.MainPID, "MainPID extends the listing fields with the main process id")
}

func TestUnitStatusZeroValueHasNoMainPID(t *testing.T) {
	t.Parallel()

	var status systemd.UnitStatus

	assert.Zero(t, status.MainPID, "zero-value MainPID must be 0 to mean no running main process")
}
