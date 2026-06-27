package journal

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/samber/mo"
)

// parseEntry decodes a raw journald field map into an Entry. PRIORITY and _PID
// parse as integers (0 when missing or unparseable), __REALTIME_TIMESTAMP
// parses from microseconds into a UTC time, and every absent optional field
// becomes its zero value.
func parseEntry(fields map[string]any) Entry {
	return Entry{
		Timestamp: parseRealtime(fields[FieldRealtime]),
		Cursor:    fieldString(fields[FieldCursor]),
		BootID:    fieldString(fields[FieldBootID]),
		Unit:      fieldString(fields[FieldUnit]),
		Comm:      fieldString(fields[FieldComm]),
		Hostname:  fieldString(fields[FieldHostname]),
		Message:   fieldString(fields[FieldMessage]),
		Priority:  fieldInt(fields[FieldPriority]),
		PID:       fieldInt(fields[FieldPID]),
	}
}

// fieldString renders a journald field value as text. journalctl emits most
// fields as plain strings but encodes binary or non-UTF-8 values as an array
// of byte-valued numbers and repeated assignments as an array of strings; both
// array forms are reconstructed here. Unknown shapes yield the empty string.
func fieldString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	case []any:
		return arrayToString(typed)
	default:
		return ""
	}
}

// arrayToString reconstructs a journalctl array-form field. An array of
// byte-valued numbers decodes to the raw byte string; an array of strings
// (one element per repeated assignment) joins with newlines.
func arrayToString(items []any) string {
	parts := make([]string, 0, len(items))
	raw := make([]byte, 0, len(items))

	for _, item := range items {
		switch elem := item.(type) {
		case string:
			parts = append(parts, elem)
		case float64:
			if elem >= 0 && elem <= math.MaxUint8 {
				raw = append(raw, byte(elem))
			}
		}
	}

	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}

	return string(raw)
}

// fieldInt parses an integer-valued journald field. Missing or unparseable
// values yield 0, matching journald's habit of omitting absent fields.
func fieldInt(value any) int {
	return mo.TupleToResult(strconv.Atoi(strings.TrimSpace(fieldString(value)))).OrElse(0)
}

// parseRealtime converts a __REALTIME_TIMESTAMP microsecond field into a UTC
// time. Missing or unparseable values yield the zero time.
func parseRealtime(value any) time.Time {
	micros, err := strconv.ParseInt(strings.TrimSpace(fieldString(value)), 10, 64)
	if err != nil {
		return time.Time{}
	}

	return time.UnixMicro(micros).UTC()
}
