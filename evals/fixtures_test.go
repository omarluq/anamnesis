package main

import (
	"strings"
	"testing"
	"time"

	"github.com/samber/oops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/journal"
)

// journalLineOOMKill is the first export record: a checkout-api OOM kill at
// 09:00:00.123456 UTC with PRIORITY "3" and _PID "1234". Its microsecond
// timestamp and string PRIORITY are the values the acceptance pins. Fragments
// are concatenated so each source line stays within the column limit while the
// on-disk record is one line.
const journalLineOOMKill = `{"__CURSOR":"s=abc;i=1",` +
	`"__REALTIME_TIMESTAMP":"1719392400123456",` +
	`"_BOOT_ID":"boot-aaaa","_SYSTEMD_UNIT":"checkout-api.service",` +
	`"_COMM":"checkout-api","_HOSTNAME":"prod-1",` +
	`"PRIORITY":"3","_PID":"1234",` +
	`"MESSAGE":"Out of memory: Killed process 1234 (checkout-api)"}`

// journalLineSSHAccept is the second record: an ssh.service login with _PID
// "789", confirming a present _PID coerces to its int.
const journalLineSSHAccept = `{"__CURSOR":"s=abc;i=2",` +
	`"__REALTIME_TIMESTAMP":"1719392460000000",` +
	`"_BOOT_ID":"boot-aaaa","_SYSTEMD_UNIT":"ssh.service",` +
	`"_COMM":"sshd","_HOSTNAME":"prod-1",` +
	`"PRIORITY":"6","_PID":"789",` +
	`"MESSAGE":"Accepted publickey for root"}`

// journalLineKernelNoPID is the third record: a kernel oom-killer line with no
// _PID and no _SYSTEMD_UNIT, the entry whose PID must coerce to 0 without error.
const journalLineKernelNoPID = `{"__CURSOR":"s=abc;i=3",` +
	`"__REALTIME_TIMESTAMP":"1719392400000000",` +
	`"_BOOT_ID":"boot-aaaa","_COMM":"kernel","_HOSTNAME":"prod-1",` +
	`"PRIORITY":"4",` +
	`"MESSAGE":"oom-killer: gfp_mask=0x100cca order=0"}`

// journalLineSessionStart is the fourth record: a systemd session-start line
// with _PID "1".
const journalLineSessionStart = `{"__CURSOR":"s=abc;i=4",` +
	`"__REALTIME_TIMESTAMP":"1719392461000000",` +
	`"_BOOT_ID":"boot-aaaa","_SYSTEMD_UNIT":"init.scope",` +
	`"_COMM":"systemd","_HOSTNAME":"prod-1",` +
	`"PRIORITY":"6","_PID":"1",` +
	`"MESSAGE":"Started Session 1 of user root"}`

// journalLineCheckoutRestart is the fifth record: a checkout-api restart after
// the OOM, rounding the export out to five entries.
const journalLineCheckoutRestart = `{"__CURSOR":"s=abc;i=5",` +
	`"__REALTIME_TIMESTAMP":"1719392462000000",` +
	`"_BOOT_ID":"boot-aaaa","_SYSTEMD_UNIT":"checkout-api.service",` +
	`"_COMM":"checkout-api","_HOSTNAME":"prod-1",` +
	`"PRIORITY":"4","_PID":"1235",` +
	`"MESSAGE":"restarting after OOM"}`

// fiveLineExport joins the five records into one journalctl-json export body.
const fiveLineExport = journalLineOOMKill + "\n" +
	journalLineSSHAccept + "\n" +
	journalLineKernelNoPID + "\n" +
	journalLineSessionStart + "\n" +
	journalLineCheckoutRestart + "\n"

func TestParseJournalExportDecodesFiveEntries(t *testing.T) {
	t.Parallel()

	entries, err := ParseJournalExport(strings.NewReader(fiveLineExport))
	require.NoError(t, err)
	require.Len(t, entries, 5)

	wantTime := time.Date(2024, time.June, 26, 9, 0, 0, 123456000, time.UTC)

	wantFirst := journal.Entry{
		Timestamp: wantTime,
		Cursor:    "s=abc;i=1",
		BootID:    "boot-aaaa",
		Unit:      "checkout-api.service",
		Comm:      "checkout-api",
		Hostname:  "prod-1",
		Message:   "Out of memory: Killed process 1234 (checkout-api)",
		Priority:  3,
		PID:       1234,
	}
	assert.Equal(t, wantFirst, entries[0])

	// __REALTIME_TIMESTAMP is microseconds, so the fractional second must survive
	// as 123456 µs and the clock must land in UTC, not the host's local zone.
	firstTimestamp := entries[0].Timestamp
	assert.True(t, wantTime.Equal(firstTimestamp), "realtime micros should decode to %s", wantTime)
	assert.Equal(t, wantTime.UnixMicro(), firstTimestamp.UnixMicro())
	assert.Equal(t, time.UTC, firstTimestamp.Location())

	// PRIORITY arrives as the string "3" and must coerce to the int 3; a present
	// _PID coerces the same way.
	assert.Equal(t, 3, entries[0].Priority)
	assert.Equal(t, 1234, entries[0].PID)
	assert.Equal(t, 789, entries[1].PID)
}

func TestParseJournalExportMissingPIDIsZero(t *testing.T) {
	t.Parallel()

	entries, err := ParseJournalExport(strings.NewReader(fiveLineExport))
	require.NoError(t, err)
	require.Len(t, entries, 5)

	// The kernel record carries no _PID and no _SYSTEMD_UNIT; both absent fields
	// must fall back to their zero values without raising an error.
	kernel := entries[2]
	assert.Equal(t, 0, kernel.PID)
	assert.Empty(t, kernel.Unit)
	assert.Equal(t, "kernel", kernel.Comm)
	assert.Equal(t, 4, kernel.Priority)
	assert.Contains(t, kernel.Message, "oom-killer")

	// The remaining units confirm all five distinct records decoded in order.
	assert.Equal(t, "ssh.service", entries[1].Unit)
	assert.Equal(t, "init.scope", entries[3].Unit)
	assert.Equal(t, "checkout-api.service", entries[4].Unit)
}

func TestParseJournalExportSkipsBlankLines(t *testing.T) {
	t.Parallel()

	body := "\n" + journalLineOOMKill + "\n\n   \n" + journalLineSSHAccept + "\n"

	entries, err := ParseJournalExport(strings.NewReader(body))
	require.NoError(t, err)
	require.Len(t, entries, 2)

	assert.Equal(t, "checkout-api.service", entries[0].Unit)
	assert.Equal(t, "ssh.service", entries[1].Unit)
}

func TestParseJournalExportEmptyInput(t *testing.T) {
	t.Parallel()

	entries, err := ParseJournalExport(strings.NewReader(""))
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestParseJournalExportDecodesArrayFields(t *testing.T) {
	t.Parallel()

	// journalctl encodes a non-UTF-8 MESSAGE as an array of byte-valued numbers
	// and a repeated field as an array of strings; both array forms must decode.
	binaryLine := `{"__CURSOR":"s=bin;i=1","__REALTIME_TIMESTAMP":"1719392400000000",` +
		`"_SYSTEMD_UNIT":"weird.service","PRIORITY":"5","_PID":"42",` +
		`"MESSAGE":[72,105,33]}`
	repeatedLine := `{"__CURSOR":"s=bin;i=2","__REALTIME_TIMESTAMP":"1719392400000001",` +
		`"_SYSTEMD_UNIT":"weird.service","PRIORITY":"5","_PID":"43",` +
		`"MESSAGE":["first","second"]}`

	entries, err := ParseJournalExport(strings.NewReader(binaryLine + "\n" + repeatedLine + "\n"))
	require.NoError(t, err)
	require.Len(t, entries, 2)

	assert.Equal(t, "Hi!", entries[0].Message)
	assert.Equal(t, 42, entries[0].PID)
	assert.Equal(t, "first\nsecond", entries[1].Message)
	assert.Equal(t, 43, entries[1].PID)
}

func TestParseJournalExportMalformedLineWrapsOops(t *testing.T) {
	t.Parallel()

	body := journalLineOOMKill + "\n" + `{"__CURSOR":"x", not valid json` + "\n"

	entries, err := ParseJournalExport(strings.NewReader(body))
	require.Error(t, err)
	assert.Nil(t, entries)

	var oopsErr oops.OopsError
	require.ErrorAs(t, err, &oopsErr)
	assert.Equal(t, "evals", oopsErr.Domain())
	assert.Equal(t, "malformed_journal_line", oopsErr.Code())
	assert.ErrorContains(t, err, "line 2")
}
