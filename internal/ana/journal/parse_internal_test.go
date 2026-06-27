package journal

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// sampleStamp is a fixed UTC instant whose microsecond encoding feeds the
// __REALTIME_TIMESTAMP inputs, so decoded timestamps are deterministic.
var sampleStamp = time.Date(2026, time.June, 26, 9, 0, 0, 0, time.UTC)

// sampleMicros is sampleStamp rendered as journald's microsecond string.
var sampleMicros = strconv.FormatInt(sampleStamp.UnixMicro(), 10)

// messageBytes encodes text as journalctl's array-of-byte-numbers form, the
// shape a binary or non-UTF-8 MESSAGE arrives in.
func messageBytes(text string) []any {
	out := make([]any, 0, len(text))
	for _, code := range []byte(text) {
		out = append(out, float64(code))
	}

	return out
}

// parseEntryCases is a package-level table so the test function stays small and
// each Entry literal can be written exhaustruct-complete.
var parseEntryCases = []struct {
	name  string
	input map[string]any
	want  Entry
}{
	{
		name: "well_formed",
		input: map[string]any{
			FieldCursor:   "cur-oom-1",
			FieldBootID:   "boot-alpha",
			FieldUnit:     "oomd.service",
			FieldComm:     "systemd-oomd",
			FieldHostname: "host-a.example.net",
			FieldMessage:  "Killed process 4242 due to memory pressure",
			FieldPriority: "3",
			FieldPID:      "4242",
			FieldRealtime: sampleMicros,
		},
		want: Entry{
			Timestamp: sampleStamp,
			Cursor:    "cur-oom-1",
			BootID:    "boot-alpha",
			Unit:      "oomd.service",
			Comm:      "systemd-oomd",
			Hostname:  "host-a.example.net",
			Message:   "Killed process 4242 due to memory pressure",
			Priority:  3,
			PID:       4242,
		},
	},
	{
		name: "missing_optional_fields_are_zero",
		input: map[string]any{
			FieldCursor: "cur-min-2",
		},
		want: Entry{
			Timestamp: time.Time{},
			Cursor:    "cur-min-2",
			BootID:    "",
			Unit:      "",
			Comm:      "",
			Hostname:  "",
			Message:   "",
			Priority:  0,
			PID:       0,
		},
	},
	{
		name: "unparseable_priority_defaults_to_zero",
		input: map[string]any{
			FieldCursor:   "cur-pri-3",
			FieldMessage:  "priority value was garbled",
			FieldPriority: "not-a-number",
			FieldPID:      "",
			FieldRealtime: sampleMicros,
		},
		want: Entry{
			Timestamp: sampleStamp,
			Cursor:    "cur-pri-3",
			BootID:    "",
			Unit:      "",
			Comm:      "",
			Hostname:  "",
			Message:   "priority value was garbled",
			Priority:  0,
			PID:       0,
		},
	},
	{
		name: "array_form_message",
		input: map[string]any{
			FieldCursor:   "cur-arr-4",
			FieldMessage:  messageBytes("kernel BUG at mm/slub.c"),
			FieldPriority: "0",
			FieldRealtime: sampleMicros,
		},
		want: Entry{
			Timestamp: sampleStamp,
			Cursor:    "cur-arr-4",
			BootID:    "",
			Unit:      "",
			Comm:      "",
			Hostname:  "",
			Message:   "kernel BUG at mm/slub.c",
			Priority:  0,
			PID:       0,
		},
	},
}

func TestParseEntry(t *testing.T) {
	t.Parallel()

	for _, testCase := range parseEntryCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := parseEntry(testCase.input)

			assert.Equal(t, testCase.want, got)
		})
	}
}
