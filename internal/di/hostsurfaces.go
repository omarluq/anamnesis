package di

import (
	"context"
	"log/slog"

	"github.com/samber/do/v2"

	"github.com/omarluq/anamnesis/internal/ana/journal"
	"github.com/omarluq/anamnesis/internal/ana/repl"
	"github.com/omarluq/anamnesis/internal/ana/systemd"
)

// logSurfaceErr records a swallowed host-surface read failure to the operator log, so
// an interpreted journal.* or systemd.* call that returns an empty result because the
// underlying read FAILED can be told apart from one that genuinely found nothing — a
// distinction the context-free, error-free host API cannot carry to the controller.
func logSurfaceErr(surface, op string, err error) {
	slog.Warn("host surface read failed; returning empty result",
		slog.String("surface", surface),
		slog.String("op", op),
		slog.String("error", err.Error()),
	)
}

// newJournalSurface provides the journal read surface investigationDeps resolves
// per submit, backed by the sdjournal-reading journal.Client. The provider is lazy
// like newOpenAIClient: journal.NewClient opens no journal handle, so the surface
// resolves with no live journal present and the libsystemd read stays deferred to
// the first interpreted journal.Query — a submit issued where the journal is
// unreadable surfaces as an empty result on that call, not a resolution failure at
// container assembly.
func newJournalSurface(_ do.Injector) (repl.Journal, error) {
	return &journalSurface{client: journal.NewClient()}, nil
}

// journalSurface adapts the error-returning journal.Client to the error-free
// repl.Journal host surface interpreted code calls by name. Each read forwards to
// the client and, on a libsystemd failure, falls back to the empty result the
// interpreter reads as "no records" rather than propagating an error the
// context-free host API cannot carry.
type journalSurface struct {
	// client is the sdjournal-backed journal reader the surface forwards to.
	client *journal.Client
}

// compile-time assertion that journalSurface satisfies the journal host surface.
var _ repl.Journal = (*journalSurface)(nil)

// Boots forwards to the client, returning the empty boot list when the read fails.
func (surface *journalSurface) Boots() []journal.BootInfo {
	boots, err := surface.client.Boots()
	if err != nil {
		logSurfaceErr("journal", "Boots", err)

		return nil
	}

	return boots
}

// Query forwards to the client, returning the empty entry list when the read fails.
func (surface *journalSurface) Query(filter *journal.QueryFilter) []journal.Entry {
	entries, err := surface.client.Query(filter)
	if err != nil {
		logSurfaceErr("journal", "Query", err)

		return nil
	}

	return entries
}

// Counts forwards to the client, returning the empty histogram when the read fails.
func (surface *journalSurface) Counts(bootID, byField string) map[string]int {
	counts, err := surface.client.Counts(bootID, byField)
	if err != nil {
		logSurfaceErr("journal", "Counts", err)

		return nil
	}

	return counts
}

// Unique forwards to the client, returning the empty value set when the read fails.
func (surface *journalSurface) Unique(field string, filter *journal.QueryFilter) []string {
	values, err := surface.client.Unique(field, filter)
	if err != nil {
		logSurfaceErr("journal", "Unique", err)

		return nil
	}

	return values
}

// newSystemdSurface provides the systemd read surface investigationDeps resolves
// per submit, backed by the dbus-reading systemd.Client. The provider is lazy like
// newOpenAIClient: systemd.NewClient dials no bus, so the surface resolves with no
// live system bus present and the dbus connection stays deferred to the first
// interpreted systemd read — a submit issued where the bus is unreachable surfaces
// as an empty result on that call, not a resolution failure at container assembly.
func newSystemdSurface(_ do.Injector) (repl.Systemd, error) {
	return &systemdSurface{client: systemd.NewClient()}, nil
}

// systemdSurface adapts the context-taking, error-returning systemd.Client to the
// error-free repl.Systemd host surface interpreted code calls by name. Each read
// runs under context.Background — the host API is context-free by design — and, on
// a dbus failure, falls back to the empty result the interpreter reads as "no unit"
// rather than propagating an error the surface cannot carry.
type systemdSurface struct {
	// client is the dbus-backed systemd reader the surface forwards to.
	client *systemd.Client
}

// compile-time assertion that systemdSurface satisfies the systemd host surface.
var _ repl.Systemd = (*systemdSurface)(nil)

// UnitStatus forwards to the client, returning the zero status when the read fails.
func (surface *systemdSurface) UnitStatus(name string) systemd.UnitStatus {
	status, err := surface.client.UnitStatus(context.Background(), name)
	if err != nil {
		logSurfaceErr("systemd", "UnitStatus", err)

		var empty systemd.UnitStatus

		return empty
	}

	return status
}

// ListUnits forwards to the client, returning the empty listing when the read fails.
func (surface *systemdSurface) ListUnits(state string) []systemd.Unit {
	units, err := surface.client.ListUnits(context.Background(), state)
	if err != nil {
		logSurfaceErr("systemd", "ListUnits", err)

		return nil
	}

	return units
}
