package rlm

import (
	"context"

	"github.com/samber/oops"

	"github.com/omarluq/anamnesis/internal/ana/citations"
	"github.com/omarluq/anamnesis/internal/ana/journal"
	"github.com/omarluq/anamnesis/internal/ana/repl"
	"github.com/omarluq/anamnesis/internal/ana/scenarios"
	"github.com/omarluq/anamnesis/internal/terminal"
)

// Deps carries the live collaborators one investigation is assembled from: the
// three model seams the controller loop drives, the two host read surfaces the
// interpreted code calls, and the trace channel the run publishes its progress
// on. Investigate wires them into a controller spine; the zero value is not
// usable because every collaborator must be supplied before a run begins.
type Deps struct {
	// Controller produces the next ControllerResponse on every turn.
	Controller ControllerLLM
	// Sub answers the recursive sub-calls agent.Query fans out.
	Sub SubLLM
	// Judge audits the final answer once before it renders.
	Judge Judger
	// Journal is the journal read surface exposed to interpreted code as the
	// journal host package.
	Journal repl.Journal
	// Systemd is the systemd read surface exposed to interpreted code as the
	// systemd host package.
	Systemd repl.Systemd
	// Events receives this run's trace events for the shell's panes.
	Events chan<- terminal.TraceEvent
	// RunID stamps every emitted trace event so the UI loop can drop stale work.
	RunID uint64
}

// validate rejects a Deps that is nil or missing any collaborator, returning the
// rlm-tagged assembly error Investigate surfaces before it dereferences a field, so
// an incomplete wiring fails loudly at the boundary instead of panicking deep inside
// an adapter on its first use. A nil receiver is reported the same way as a missing
// field, since both mean a run cannot be assembled.
func (deps *Deps) validate() error {
	if deps == nil {
		return oops.
			In("rlm").
			Code("investigate_deps_missing").
			Errorf("investigate requires non-nil dependencies")
	}

	missing := deps.Controller == nil ||
		deps.Sub == nil ||
		deps.Judge == nil ||
		deps.Journal == nil ||
		deps.Systemd == nil ||
		deps.Events == nil
	if missing {
		return oops.
			In("rlm").
			Code("investigate_deps_missing").
			Errorf("investigate requires every dependency to be supplied")
	}

	return nil
}

// Investigate assembles and runs one RLM investigation of question end to end: it
// builds the citation store and trace emitter, wires an mvm REPL session over the
// host surfaces with a visibility-recording journal and a sub-call seam that emits
// a trace event per fan-out, frames the controller with the SPEC §14 system
// prompt, and drives the audited turn loop under the §6 hard budget — a 120-second
// wall-clock deadline layered onto ctx alongside the twelve-turn cap. It returns
// the judge-approved final answer, which it also publishes as the terminal trace
// event, or an oops error tagged with the rlm domain when the session cannot be
// assembled or the loop fails.
func Investigate(ctx context.Context, question string, deps *Deps) (string, error) {
	if err := deps.validate(); err != nil {
		return "", err
	}

	budget := NewBudget()

	ctx, cancel := context.WithTimeout(ctx, budget.WallTimeout)
	defer cancel()

	store := citations.NewStore()
	emitter := NewEmitter(deps.Events, deps.RunID)

	interpreter, err := buildInterpreter(ctx, deps, store, emitter)
	if err != nil {
		return "", err
	}

	session := Session{
		Controller:   deps.Controller,
		Sub:          deps.Sub,
		Judge:        deps.Judge,
		Budget:       budget,
		Store:        store,
		Emitter:      emitter,
		Question:     question,
		SystemPrompt: scenarios.ControllerSystemPrompt,
		History:      nil,
	}

	answer, err := NewController(&session, interpreter).RunAudited(ctx)
	if err != nil {
		return "", err
	}

	emitter.Final(answer)

	return answer, nil
}

// buildInterpreter wires the mvm REPL session the controller drives each turn: the
// journal surface is decorated so every query records its returned entries as
// citable evidence, the citation store receives agent.Cite, and the sub-call seam
// emits a trace event per fan-out before forwarding to deps.Sub under ctx. It
// returns an oops error tagged with the rlm domain when repl.New rejects the
// assembled config.
func buildInterpreter(
	ctx context.Context,
	deps *Deps,
	store *citations.Store,
	emitter *Emitter,
) (*repl.Interpreter, error) {
	cfg := repl.Config{
		Host: repl.HostDeps{
			Journal: &recordingJournal{delegate: deps.Journal, store: store},
			Systemd: deps.Systemd,
		},
		Sink: store,
		Sub:  newEmittingSub(ctx, deps.Sub, emitter),
		Budget: repl.QueryBudget{
			MaxDepth:    repl.DefaultMaxDepth,
			MaxSubCalls: repl.DefaultMaxSubCalls,
		},
	}

	interpreter, err := repl.New(&cfg)
	if err != nil {
		return nil, oops.
			In("rlm").
			Code("investigate_session_unbuilt").
			Wrapf(err, "assemble repl session")
	}

	return interpreter, nil
}

// recordingJournal decorates a journal host surface so every Query records the
// entries it returned as session-visible in the citation store, grounding any
// later agent.Cite against evidence a real query actually surfaced. It is a real
// adapter the run assembles, not a test double.
type recordingJournal struct {
	// delegate is the underlying journal surface the decorator forwards to.
	delegate repl.Journal
	// store records the cursors each query made visible for citation grounding.
	store *citations.Store
}

// Compile-time assertion that the decorator satisfies the journal host surface.
var _ repl.Journal = (*recordingJournal)(nil)

// Boots forwards to the underlying surface; a boot record carries no citable
// cursor, so there is nothing to record.
func (surface *recordingJournal) Boots() []journal.BootInfo {
	return surface.delegate.Boots()
}

// Query forwards to the underlying surface and records every returned entry as
// session-visible, so a cursor the controller later cites is grounded in a real
// query result rather than fabricated.
func (surface *recordingJournal) Query(filter *journal.QueryFilter) []journal.Entry {
	entries := surface.delegate.Query(filter)
	surface.store.RecordVisible(entries)

	return entries
}

// Counts forwards to the underlying surface; a histogram carries no citable
// cursor, so there is nothing to record.
func (surface *recordingJournal) Counts(bootID, byField string) map[string]int {
	return surface.delegate.Counts(bootID, byField)
}

// Unique forwards to the underlying surface; a distinct field value carries no
// citable cursor, so there is nothing to record.
func (surface *recordingJournal) Unique(field string, filter *journal.QueryFilter) []string {
	return surface.delegate.Unique(field, filter)
}

// emittingSub adapts the rlm sub-LLM seam to the repl sub-call surface the
// interpreter drives: it emits a sub-call trace event for every fan-out, then
// forwards the call to the underlying SubLLM bound to the run context. It is a
// real adapter the run assembles, not a test double.
type emittingSub struct {
	// answer forwards one sub-call to the underlying SubLLM under the run context.
	answer func(prompt, evidence string) (string, error)
	// emitter publishes the sub-call trace event each fan-out produces.
	emitter *Emitter
}

// newEmittingSub binds sub and ctx into a repl sub-call surface that emits onto
// emitter. The run context is captured in the forwarding closure rather than
// stored on the struct, matching the repl seam's context-free contract.
func newEmittingSub(ctx context.Context, sub SubLLM, emitter *Emitter) *emittingSub {
	return &emittingSub{
		answer: func(prompt, evidence string) (string, error) {
			return sub.Answer(ctx, prompt, evidence)
		},
		emitter: emitter,
	}
}

// Compile-time assertion that the adapter satisfies the repl sub-call seam.
var _ repl.SubLLM = (*emittingSub)(nil)

// Sub emits a sub-call trace event carrying prompt, then forwards the call to the
// underlying SubLLM, returning its reply and any error unchanged so the
// interpreter surfaces a sub-call failure as a turn error.
func (relay *emittingSub) Sub(prompt, evidence string) (string, error) {
	relay.emitter.SubCall(prompt)

	return relay.answer(prompt, evidence)
}
