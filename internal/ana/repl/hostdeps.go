package repl

import (
	"reflect"

	"github.com/omarluq/anamnesis/internal/ana/journal"
	"github.com/omarluq/anamnesis/internal/ana/systemd"
)

// Journal is the read-only journal surface the host injects into a REPL session.
// Its methods mirror the journal host package the controller writes Go against,
// so HostDeps.Register exposes them as journal.Boots, journal.Query,
// journal.Counts and journal.Unique. Implementations return the journal value
// types unchanged, so interpreted source ranges the results and reads their
// fields back natively. The query methods take *journal.QueryFilter so the heavy
// filter struct crosses by reference; interpreted source builds it with the
// address-of form, for example journal.Query(&journal.QueryFilter{Unit: u}).
type Journal interface {
	// Boots lists the recorded boots as journal.Boots does.
	Boots() []journal.BootInfo
	// Query returns the entries matching filter as journal.Query does.
	Query(filter *journal.QueryFilter) []journal.Entry
	// Counts returns the value-to-count histogram of byField across one boot.
	Counts(bootID, byField string) map[string]int
	// Unique returns the distinct values of field across the filter range.
	Unique(field string, filter *journal.QueryFilter) []string
}

// Systemd is the read-only systemd surface the host injects into a REPL session.
// HostDeps.Register exposes its methods as systemd.UnitStatus and
// systemd.ListUnits, returning the systemd value types so interpreted source
// reads their fields back natively.
type Systemd interface {
	// UnitStatus returns the detailed status of the named unit.
	UnitStatus(name string) systemd.UnitStatus
	// ListUnits lists units in the given state, or all units when state is "".
	ListUnits(state string) []systemd.Unit
}

// HostDeps bundles the live host surfaces a REPL session exposes to interpreted
// source. The controller depends only on these interfaces, so a session is wired
// with the real sdjournal- and dbus-backed clients in production and with test
// doubles under test. Register installs them as the journal and systemd packages.
type HostDeps struct {
	// Journal is the journal read surface exposed as the journal package.
	Journal Journal
	// Systemd is the systemd read surface exposed as the systemd package.
	Systemd Systemd
}

// Register exposes the journal and systemd surfaces to interpreter so controller
// source can call journal.Query, systemd.UnitStatus and the rest of the read API
// by name. Both packages are installed with their methods and, for journal, the
// value types needed to build a journal.QueryFilter, then auto-imported. It
// returns an oops error tagged with the repl domain when either surface is unset
// or is missing a method declared on its interface.
func (deps HostDeps) Register(interpreter *Interpreter) error {
	if err := deps.registerJournal(interpreter); err != nil {
		return err
	}

	return deps.registerSystemd(interpreter)
}

// registerJournal installs the journal surface together with the constructible
// journal value types as the interpreted journal package, so source can both call
// the read methods and write journal.QueryFilter literals to drive Query.
func (deps HostDeps) registerJournal(interpreter *Interpreter) error {
	symbols, err := surfaceFuncs("journal", reflect.TypeFor[Journal](), reflect.ValueOf(deps.Journal))
	if err != nil {
		return err
	}

	symbols["BootInfo"] = typeBinding[journal.BootInfo]()
	symbols["Entry"] = typeBinding[journal.Entry]()
	symbols["QueryFilter"] = typeBinding[journal.QueryFilter]()

	importSurface(interpreter, "journal", symbols)

	return nil
}

// registerSystemd installs the systemd surface as the interpreted systemd package
// together with the constructible systemd.Unit value type, so source can both call
// the read methods and build []systemd.Unit slices — for example to merge several
// ListUnits results into one slice before sorting it. Without the bound type a
// `[]systemd.Unit{}` literal or `var x []systemd.Unit` declaration has an element
// type the interpreter cannot resolve, so a later field access fails with
// "undefined: Name". The UnitStatus TYPE is deliberately not bound: the surface
// already exports a UnitStatus function, and a type of the same name would collide in
// the package symbol table, so source reads a returned UnitStatus's fields by
// reflection without ever naming the type. Unit has no such collision.
func (deps HostDeps) registerSystemd(interpreter *Interpreter) error {
	symbols, err := surfaceFuncs("systemd", reflect.TypeFor[Systemd](), reflect.ValueOf(deps.Systemd))
	if err != nil {
		return err
	}

	symbols["Unit"] = typeBinding[systemd.Unit]()

	importSurface(interpreter, "systemd", symbols)

	return nil
}
