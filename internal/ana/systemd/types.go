// Package systemd exposes read-only systemd unit value types for the host API
// the in-process interpreter writes Go against.
package systemd

// Unit is one entry from a unit listing, as returned by ListUnits. It carries
// the load and activation state a controller needs to triage a unit without a
// detailed status fetch.
type Unit struct {
	// Name is the unit name, for example "nginx.service".
	Name string
	// Description is the unit's human-readable description.
	Description string
	// LoadState reports whether the unit definition loaded: "loaded",
	// "masked", or "not-found".
	LoadState string
	// ActiveState reports the high-level activation: "active", "inactive", or
	// "failed".
	ActiveState string
	// SubState reports the unit-type-specific state, for example "running",
	// "exited", "failed", or "dead".
	SubState string
}

// UnitStatus is the detailed status of a single unit, as returned by UnitStatus.
// It extends the listing fields with the main process identifier.
type UnitStatus struct {
	// Name is the unit name, for example "nginx.service".
	Name string
	// Description is the unit's human-readable description.
	Description string
	// LoadState reports whether the unit definition loaded: "loaded",
	// "masked", or "not-found".
	LoadState string
	// ActiveState reports the high-level activation: "active", "inactive", or
	// "failed".
	ActiveState string
	// SubState reports the unit-type-specific state, for example "running",
	// "exited", "failed", or "dead".
	SubState string
	// MainPID is the process identifier of the unit's main process, or zero
	// when the unit has no running main process or its PID is unavailable —
	// for example a non-service unit, or a MainPID property that cannot be read.
	MainPID int
}
