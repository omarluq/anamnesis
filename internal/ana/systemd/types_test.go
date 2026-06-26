package systemd_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/omarluq/anamnesis/internal/ana/systemd"
)

func TestUnitFields(t *testing.T) {
	t.Parallel()

	unit := systemd.Unit{
		Name:        "nginx.service",
		Description: "A high performance web server",
		LoadState:   "loaded",
		ActiveState: "active",
		SubState:    "running",
	}

	assert.Equal(t, "nginx.service", unit.Name)
	assert.Equal(t, "A high performance web server", unit.Description)
	assert.Equal(t, "loaded", unit.LoadState)
	assert.Equal(t, "active", unit.ActiveState)
	assert.Equal(t, "running", unit.SubState)
}

func TestUnitStatusFields(t *testing.T) {
	t.Parallel()

	status := systemd.UnitStatus{
		Name:        "sshd.service",
		Description: "OpenSSH server daemon",
		LoadState:   "loaded",
		ActiveState: "failed",
		SubState:    "dead",
		MainPID:     4242,
	}

	assert.Equal(t, "sshd.service", status.Name)
	assert.Equal(t, "OpenSSH server daemon", status.Description)
	assert.Equal(t, "loaded", status.LoadState)
	assert.Equal(t, "failed", status.ActiveState)
	assert.Equal(t, "dead", status.SubState)
	assert.Equal(t, 4242, status.MainPID)
}

func TestUnitStatusZeroMainPID(t *testing.T) {
	t.Parallel()

	status := systemd.UnitStatus{
		Name:        "cron.service",
		Description: "Regular background program processing daemon",
		LoadState:   "masked",
		ActiveState: "inactive",
		SubState:    "exited",
		MainPID:     0,
	}

	assert.Equal(t, "cron.service", status.Name)
	assert.Equal(t, "Regular background program processing daemon", status.Description)
	assert.Equal(t, "masked", status.LoadState)
	assert.Equal(t, "inactive", status.ActiveState)
	assert.Equal(t, "exited", status.SubState)
	assert.Zero(t, status.MainPID)
}
