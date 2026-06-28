package repl_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/repl"
	"github.com/omarluq/anamnesis/internal/ana/repl/repltest"
	"github.com/omarluq/anamnesis/internal/ana/systemd"
)

// TestSystemdUnitTypeIsConstructible proves the interpreter binds systemd.Unit as a
// constructible value type, so controller source can declare and build []systemd.Unit
// slices — for example merging several ListUnits results into one slice — then sort
// and read fields off the elements. Before systemd.Unit was bound, a
// `var x []systemd.Unit` declaration or a `[]systemd.Unit{}` literal carried an
// element type the interpreter could not resolve, so a later .Name access failed with
// "undefined: Name" and crashed the "list all units" investigation.
func TestSystemdUnitTypeIsConstructible(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	systemdMock := new(repltest.MockSystemd)
	systemdMock.On("ListUnits", "").Return([]systemd.Unit{
		{Name: "b.service", Description: "", LoadState: "", ActiveState: "", SubState: ""},
		{Name: "a.service", Description: "", LoadState: "", ActiveState: "", SubState: ""},
	})

	deps := repl.HostDeps{Journal: new(repltest.MockJournal), Systemd: systemdMock}
	require.NoError(t, deps.Register(interpreter))

	// var []systemd.Unit + append + sort + field access — the exact shape the model
	// used. The element states are irrelevant here; the regression was the explicit
	// []systemd.Unit element type, not the field values.
	const src = `var all []systemd.Unit
all = append(all, systemd.ListUnits("")...)
sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
all[0].Name`

	result, err := interpreter.Eval("merge_units", src)
	require.NoError(t, err, "building and sorting a []systemd.Unit must resolve the element type")
	require.True(t, result.Retval.IsValid())
	assert.Equal(t, "a.service", result.Retval.String())
}
