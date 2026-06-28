package di

import (
	"testing"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/repl"
	"github.com/omarluq/anamnesis/internal/ana/rlm"
	"github.com/omarluq/anamnesis/internal/terminal"
)

// TestContainerResolvesChatControllerAndRLMCollaborators builds the fully
// registered runtime container and resolves the chat controller end to end
// together with every collaborator the chat investigation assembles per submit —
// the three RLM model seams and the journal and systemd host read surfaces —
// asserting each resolves with no "could not find service" error and with no live
// OpenAI call, no journal or bus access, and no API key. It is the regression
// guard the stub-controller capstone test missed: resolving terminal.Controller
// alone left the collaborator graph unverified, so an unregistered ControllerLLM,
// SubLLM, Journal, or Systemd surfaced only at submit time as a failed run.
// Covering the complete investigationDeps set keeps the guard honest: it now
// breaks if any seam the per-submit graph resolves goes unregistered.
func TestContainerResolvesChatControllerAndRLMCollaborators(t *testing.T) {
	t.Parallel()

	container, err := NewContainer("")

	require.NoError(t, err)
	require.NotNil(t, container)

	controller, err := do.Invoke[terminal.Controller](container.injector)

	require.NoError(t, err, "the fully-registered container resolves the chat controller")
	require.NotNil(t, controller)

	controllerLLM, err := do.Invoke[rlm.ControllerLLM](container.injector)

	require.NoError(t, err, "the controller-model collaborator resolves with no API key")
	require.NotNil(t, controllerLLM)

	subLLM, err := do.Invoke[rlm.SubLLM](container.injector)

	require.NoError(t, err, "the sub-LLM collaborator resolves with no API key")
	require.NotNil(t, subLLM)

	journalSurface, err := do.Invoke[repl.Journal](container.injector)

	require.NoError(t, err, "the journal host surface resolves with no journal access")
	require.NotNil(t, journalSurface)

	systemdSurface, err := do.Invoke[repl.Systemd](container.injector)

	require.NoError(t, err, "the systemd host surface resolves with no bus access")
	require.NotNil(t, systemdSurface)
}
