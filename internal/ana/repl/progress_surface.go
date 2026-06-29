package repl

import (
	"github.com/omarluq/anamnesis/internal/ana/journal"
	"github.com/omarluq/anamnesis/internal/ana/systemd"
)

// progressJournal decorates a Journal surface so every read advances the interpreter's
// tree-wide progress counter before forwarding. The idle-progress watchdog samples
// stdout plus that counter, so without this a turn busy entirely in host reads — a
// windowed journal.Query can scan a whole boot through cgo without printing a byte —
// registers no progress and is force-finished as a wedge though it is doing real work.
// The counter is reached through the interpreter at call time rather than snapshotted,
// because SetProgress reassigns it to the shared tree counter after Register has bound
// this surface.
type progressJournal struct {
	// interpreter owns the progress counter recordProgress advances at call time.
	interpreter *Interpreter
	// delegate is the underlying journal surface each read forwards to.
	delegate Journal
}

// compile-time assertion that the decorator satisfies the journal host surface.
var _ Journal = (*progressJournal)(nil)

// Boots records progress, then forwards to the wrapped surface.
func (surface progressJournal) Boots() []journal.BootInfo {
	surface.interpreter.recordProgress()

	return surface.delegate.Boots()
}

// Query records progress, then forwards to the wrapped surface.
func (surface progressJournal) Query(filter *journal.QueryFilter) []journal.Entry {
	surface.interpreter.recordProgress()

	return surface.delegate.Query(filter)
}

// Counts records progress, then forwards to the wrapped surface.
func (surface progressJournal) Counts(bootID, byField string) map[string]int {
	surface.interpreter.recordProgress()

	return surface.delegate.Counts(bootID, byField)
}

// Unique records progress, then forwards to the wrapped surface.
func (surface progressJournal) Unique(field string, filter *journal.QueryFilter) []string {
	surface.interpreter.recordProgress()

	return surface.delegate.Unique(field, filter)
}

// progressSystemd decorates a Systemd surface so every read advances the interpreter's
// tree-wide progress counter before forwarding, for the same reason progressJournal
// does: a systemd.ListUnits over hundreds of units does real work while printing
// nothing, and must not read as a wedge to the idle watchdog.
type progressSystemd struct {
	// interpreter owns the progress counter recordProgress advances at call time.
	interpreter *Interpreter
	// delegate is the underlying systemd surface each read forwards to.
	delegate Systemd
}

// compile-time assertion that the decorator satisfies the systemd host surface.
var _ Systemd = (*progressSystemd)(nil)

// UnitStatus records progress, then forwards to the wrapped surface.
func (surface progressSystemd) UnitStatus(name string) systemd.UnitStatus {
	surface.interpreter.recordProgress()

	return surface.delegate.UnitStatus(name)
}

// ListUnits records progress, then forwards to the wrapped surface.
func (surface progressSystemd) ListUnits(state string) []systemd.Unit {
	surface.interpreter.recordProgress()

	return surface.delegate.ListUnits(state)
}
