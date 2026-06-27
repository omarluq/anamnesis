package journal

// DecodeFields decodes a raw journald field map — as produced by unmarshalling a
// single "journalctl --output=json" record — into an Entry. It applies the same
// coercions as the live reader: __REALTIME_TIMESTAMP is read as microseconds into
// a UTC time, PRIORITY and _PID are coerced from their string forms to ints,
// array-form fields are reconstructed, and every absent field becomes its zero
// value. It is the public seam external packages use to decode export records
// without reimplementing the field handling.
func DecodeFields(fields map[string]any) Entry {
	return parseEntry(fields)
}
