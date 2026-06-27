// Package journal defines the pure value types for systemd journald records.
// It carries no cgo and no libsystemd dependency, so any package that only
// needs the journal data shapes can import it without a build toolchain.
package journal

import "time"

// Journald field name constants are the native field identifiers used when
// matching raw records and decoding them into an Entry.
const (
	// FieldCursor is the journald __CURSOR field, the stable per-entry handle.
	FieldCursor = "__CURSOR"
	// FieldBootID is the journald _BOOT_ID field identifying a boot.
	FieldBootID = "_BOOT_ID"
	// FieldUnit is the journald _SYSTEMD_UNIT field naming the owning unit.
	FieldUnit = "_SYSTEMD_UNIT"
	// FieldComm is the journald _COMM field holding the process command name.
	FieldComm = "_COMM"
	// FieldHostname is the journald _HOSTNAME field naming the originating host.
	FieldHostname = "_HOSTNAME"
	// FieldMessage is the journald MESSAGE field holding the log text.
	FieldMessage = "MESSAGE"
	// FieldPriority is the journald PRIORITY field (0=emerg .. 7=debug).
	FieldPriority = "PRIORITY"
	// FieldPID is the journald _PID field holding the process identifier.
	FieldPID = "_PID"
	// FieldRealtime is the journald __REALTIME_TIMESTAMP field, the entry clock.
	FieldRealtime = "__REALTIME_TIMESTAMP"
)

// Entry is one journald record decoded into a curated subset of fields. Every
// Entry carries its Cursor so callers can cite the exact source record.
type Entry struct {
	// Timestamp is the entry realtime clock from __REALTIME_TIMESTAMP.
	Timestamp time.Time
	// Cursor is the stable __CURSOR handle used for citation.
	Cursor string
	// BootID is the _BOOT_ID of the boot that produced the entry.
	BootID string
	// Unit is the _SYSTEMD_UNIT that owns the entry.
	Unit string
	// Comm is the _COMM process command name that logged the entry.
	Comm string
	// Hostname is the _HOSTNAME of the originating host.
	Hostname string
	// Message is the MESSAGE log text of the entry.
	Message string
	// Priority is the syslog PRIORITY of the entry (0=emerg .. 7=debug).
	Priority int
	// PID is the _PID of the process that logged the entry.
	PID int
}

// BootInfo describes one boot as reported by journalctl --list-boots. Index 0
// is the running boot and older boots carry decreasing negative indexes.
type BootInfo struct {
	// FirstSeen is the realtime timestamp of the boot's earliest entry.
	FirstSeen time.Time
	// LastSeen is the realtime timestamp of the boot's latest entry.
	LastSeen time.Time
	// ID is the _BOOT_ID that uniquely identifies the boot.
	ID string
	// Index is 0 for the running boot and decreasing negatives for older boots.
	Index int
}

// QueryFilter narrows a journal query. Zero-value fields impose no constraint,
// so an empty QueryFilter matches every entry up to the default Limit.
type QueryFilter struct {
	// Since is the inclusive realtime lower bound for matched entries.
	Since time.Time
	// Until is the inclusive realtime upper bound for matched entries.
	Until time.Time
	// MaxPriority keeps only entries with PRIORITY <= this value (0..7). A nil
	// pointer means no priority constraint, which keeps 0 (emerg) distinct from
	// "unspecified" and preserves the zero-value contract. Build it with the
	// new(expr) builtin, e.g. new(4) for a *int pointing at 4.
	MaxPriority *int
	// Unit restricts matches to a single _SYSTEMD_UNIT.
	Unit string
	// BootID restricts matches to a single _BOOT_ID.
	BootID string
	// Grep keeps only entries whose MESSAGE contains this substring.
	Grep string
	// Limit is the hard cap on returned entries (default 1000, max 10000).
	Limit int
}
