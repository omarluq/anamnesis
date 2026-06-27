package di

import (
	"context"

	"github.com/samber/do/v2"
	"github.com/samber/oops"

	"github.com/omarluq/anamnesis/internal/ana/repl"
	"github.com/omarluq/anamnesis/internal/ana/rlm"
	"github.com/omarluq/anamnesis/internal/terminal"
)

// RegisterServices registers all application services in the injector. Every host
// collaborator investigationDeps resolves per submit is wired here, so the full
// set assembles rather than failing at the first unregistered seam: the OpenAI
// client backs the three RLM model adapters — rlm.ControllerLLM, rlm.SubLLM, and
// rlm.Judger — and the journal and systemd read surfaces back repl.Journal and
// repl.Systemd. Every registration is lazy, so the container assembles and all
// five collaborators resolve with no live OpenAI call, no journal or bus access,
// and no API key present.
func RegisterServices(injector do.Injector) {
	do.Provide(injector, NewConfigService)
	do.Provide(injector, NewLoggerService)
	do.Provide(injector, NewChatController)
	do.Provide(injector, newOpenAIClient)
	do.Provide(injector, newControllerAdapter)
	do.Provide(injector, newSubAdapter)
	do.Provide(injector, newJudgeAdapter)
	do.Provide(injector, newJournalSurface)
	do.Provide(injector, newSystemdSurface)
}

// NewChatController provides the terminal Controller the chat shell drives. It
// adapts rlm.Investigate behind the terminal.Controller seam so a composer submit
// spawns a live investigation whose trace events stream into the shell's panes
// instead of an echoed line. The host collaborators each run needs are resolved
// lazily, per submit, from injector, so registering the controller never fails
// and the live OpenAI path stays gated behind those collaborators and the API
// key — a submit issued before they are wired surfaces as a failed run, not a
// crash.
func NewChatController(injector do.Injector) (terminal.Controller, error) {
	return terminal.NewRLMController(investigateWith(injector)), nil
}

// investigateWith binds injector into a terminal.Investigator that assembles the
// run's host collaborators on each submit and drives rlm.Investigate, streaming
// its trace events onto events stamped with runID. It returns an oops error
// tagged with the di domain when a collaborator is not yet registered, which the
// adapter renders to the shell as a failed run.
func investigateWith(injector do.Injector) terminal.Investigator {
	return func(ctx context.Context, query string, events chan<- terminal.TraceEvent, runID uint64) (string, error) {
		deps, err := investigationDeps(injector, events, runID)
		if err != nil {
			return "", err
		}

		return rlm.Investigate(ctx, query, deps)
	}
}

// investigationDeps resolves the host collaborators one investigation is wired
// from — the controller, sub-LLM and judge model seams plus the journal and
// systemd read surfaces — and bundles them with the run's trace channel and ID
// into the rlm.Deps the controller spine consumes. It returns an oops error
// tagged with the di domain identifying the first collaborator that is not yet
// registered.
func investigationDeps(
	injector do.Injector,
	events chan<- terminal.TraceEvent,
	runID uint64,
) (*rlm.Deps, error) {
	controller, err := do.Invoke[rlm.ControllerLLM](injector)
	if err != nil {
		return nil, collaboratorError(err, "controller")
	}

	sub, err := do.Invoke[rlm.SubLLM](injector)
	if err != nil {
		return nil, collaboratorError(err, "sub-LLM")
	}

	judge, err := do.Invoke[rlm.Judger](injector)
	if err != nil {
		return nil, collaboratorError(err, "judge")
	}

	journalSurface, err := do.Invoke[repl.Journal](injector)
	if err != nil {
		return nil, collaboratorError(err, "journal")
	}

	systemdSurface, err := do.Invoke[repl.Systemd](injector)
	if err != nil {
		return nil, collaboratorError(err, "systemd")
	}

	return &rlm.Deps{
		Controller: controller,
		Sub:        sub,
		Judge:      judge,
		Journal:    journalSurface,
		Systemd:    systemdSurface,
		Events:     events,
		RunID:      runID,
	}, nil
}

// collaboratorError wraps a missing-collaborator resolution failure in an oops
// error tagged with the di domain, naming the collaborator the chat investigation
// could not assemble.
func collaboratorError(err error, collaborator string) error {
	return oops.
		In("di").
		Code("chat_collaborator_unwired").
		Wrapf(err, "resolve %s collaborator for chat investigation", collaborator)
}
