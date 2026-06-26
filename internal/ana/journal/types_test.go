package journal_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/journal"
)

func TestEntryFields(t *testing.T) {
	t.Parallel()

	stamp := time.Date(2026, time.June, 26, 9, 0, 0, 0, time.UTC)
	entry := journal.Entry{
		Timestamp: stamp,
		Cursor:    "s=abc;i=1;b=boot-1",
		BootID:    "boot-1",
		Unit:      "checkout-api.service",
		Comm:      "checkout",
		Hostname:  "node-7.prod.internal",
		Message:   "out of memory: killed process 4242",
		Priority:  3,
		PID:       4242,
	}

	assert.Equal(t, stamp, entry.Timestamp)
	assert.Equal(t, "s=abc;i=1;b=boot-1", entry.Cursor)
	assert.Equal(t, "boot-1", entry.BootID)
	assert.Equal(t, "checkout-api.service", entry.Unit)
	assert.Equal(t, "checkout", entry.Comm)
	assert.Equal(t, "node-7.prod.internal", entry.Hostname)
	assert.Equal(t, "out of memory: killed process 4242", entry.Message)
	assert.Equal(t, 3, entry.Priority)
	assert.Equal(t, 4242, entry.PID)
}

func TestBootInfoFields(t *testing.T) {
	t.Parallel()

	first := time.Date(2026, time.June, 26, 8, 0, 0, 0, time.UTC)
	last := time.Date(2026, time.June, 26, 12, 0, 0, 0, time.UTC)
	boot := journal.BootInfo{
		FirstSeen: first,
		LastSeen:  last,
		ID:        "boot-1",
		Index:     0,
	}

	assert.Equal(t, first, boot.FirstSeen)
	assert.Equal(t, last, boot.LastSeen)
	assert.Equal(t, "boot-1", boot.ID)
	assert.Equal(t, 0, boot.Index)
	assert.False(t, boot.FirstSeen.After(boot.LastSeen), "FirstSeen must not be after LastSeen")
}

func TestQueryFilterFields(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, time.June, 26, 8, 0, 0, 0, time.UTC)
	until := time.Date(2026, time.June, 26, 10, 0, 0, 0, time.UTC)
	maxPriority := 4
	filter := journal.QueryFilter{
		Since:       since,
		Until:       until,
		Unit:        "ssh.service",
		BootID:      "boot-2",
		Grep:        "Failed password",
		MaxPriority: &maxPriority,
		Limit:       1000,
	}

	assert.Equal(t, since, filter.Since)
	assert.Equal(t, until, filter.Until)
	assert.Equal(t, "ssh.service", filter.Unit)
	assert.Equal(t, "boot-2", filter.BootID)
	assert.Equal(t, "Failed password", filter.Grep)
	require.NotNil(t, filter.MaxPriority)
	assert.Equal(t, 4, *filter.MaxPriority)
	assert.Equal(t, 1000, filter.Limit)
}

func TestQueryFilterZeroValueHasNoPriorityConstraint(t *testing.T) {
	t.Parallel()

	var filter journal.QueryFilter

	assert.Nil(t, filter.MaxPriority, "zero-value MaxPriority must be nil to mean no priority constraint")
}

func TestFieldConstants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		got  string
		want string
	}{
		{name: "cursor", got: journal.FieldCursor, want: "__CURSOR"},
		{name: "boot_id", got: journal.FieldBootID, want: "_BOOT_ID"},
		{name: "unit", got: journal.FieldUnit, want: "_SYSTEMD_UNIT"},
		{name: "comm", got: journal.FieldComm, want: "_COMM"},
		{name: "hostname", got: journal.FieldHostname, want: "_HOSTNAME"},
		{name: "message", got: journal.FieldMessage, want: "MESSAGE"},
		{name: "priority", got: journal.FieldPriority, want: "PRIORITY"},
		{name: "pid", got: journal.FieldPID, want: "_PID"},
		{name: "realtime", got: journal.FieldRealtime, want: "__REALTIME_TIMESTAMP"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.got)
		})
	}
}
