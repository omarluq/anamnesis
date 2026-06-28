package rlm

import (
	"context"
	"log/slog"
	"sync/atomic"

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
	// MaxDepth bounds how deep agent.Query may recurse: 0 keeps every sub-call a
	// flat base-case call, the SPEC §6 default of 3 lets the tree recurse two
	// levels below the root before the leaf falls back to a flat call.
	MaxDepth int
	// MaxSubCalls bounds the total agent.Query and agent.QueryBatched sub-calls the
	// whole recursion tree may spend, shared across every level (SPEC §6: 60).
	MaxSubCalls int
}

// validate rejects a Deps that is nil, missing any collaborator, or carrying
// invalid recursion limits, returning the rlm-tagged assembly error Investigate
// surfaces before it dereferences a field, so an incomplete wiring fails loudly at
// the boundary instead of panicking deep inside an adapter on its first use. A nil
// receiver is reported the same way as a missing field, since both mean a run
// cannot be assembled. The limit guard keeps a zero or negative MaxSubCalls from
// seeding an already-exhausted budget that degrades every agent.Query to text, and
// a negative MaxDepth from silently disabling recursion.
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

	if deps.MaxDepth < 0 || deps.MaxSubCalls <= 0 {
		return oops.
			In("rlm").
			Code("investigate_limits_invalid").
			Errorf("investigate requires MaxDepth >= 0 and MaxSubCalls > 0")
	}

	return nil
}

// Investigate assembles and runs one RLM investigation of question end to end: it
// builds the citation store, trace emitter, and shared tree-wide §6 budget, then
// wires the root mvm REPL session over the host surfaces with a visibility-
// recording journal and the recursive sub-call adapter, frames the controller with
// the SPEC §14 system prompt, and drives the audited turn loop. The run carries no
// wall-clock or turn budget: it runs until the controller signals agent.FINAL, the
// caller cancels ctx, or a turn's eval idles past its per-eval progress watchdog.
// Investigate derives a cancelable child of ctx and cancels it on return, so a
// force-finished or completed run tears down any still-running child controller loops
// and sub-LLM calls rather than letting an abandoned subtree keep making paid model
// calls. agent.Query is genuinely recursive: a sub-call above the leaf level spawns a
// full child controller loop one level deeper, while the leaf falls back to a flat
// base-case call, all sharing one budget. It returns the judge-approved final answer,
// which it also publishes as the terminal trace event, or an oops error tagged with
// the rlm domain when the session cannot be assembled or the loop fails.
func Investigate(ctx context.Context, question string, deps *Deps) (string, error) {
	if err := deps.validate(); err != nil {
		return "", err
	}

	// Derive a cancelable child so returning — on a clean finish or a force-finish —
	// tears down any still-running child controller loops and sub-LLM calls. Without
	// this an abandoned eval's subtree would keep honoring the original ctx and keep
	// making paid model calls after the run has already settled.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	budget := newTreeBudget(deps)

	slog.InfoContext(
		ctx,
		"rlm investigation start",
		slog.Uint64("run_id", deps.RunID),
		slog.Int("question_len", len(question)),
		slog.Int("max_depth", deps.MaxDepth),
		slog.Int("max_sub_calls", deps.MaxSubCalls),
	)

	store := citations.NewStore()
	emitter := NewEmitter(ctx, deps.Events, deps.RunID)
	// progress is the tree-wide work counter every interpreter's idle watchdog reads:
	// one counter shared across the root and every child loop, so a slow child's work
	// keeps the parent's watchdog from mistaking a busy fan-out for a wedge.
	progress := new(atomic.Int64)
	factory := newRecursor(deps, budget, emitter, emitter, progress)
	root := RecursionContext{CurrentDepth: 0, MaxDepth: deps.MaxDepth}

	interpreter, err := factory.interpreter(ctx, root, store)
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

	controller := NewController(&session, interpreter)

	answer, err := controller.RunAudited(ctx)
	if err != nil {
		return "", err
	}

	slog.InfoContext(
		ctx,
		"rlm investigation end",
		slog.Uint64("run_id", deps.RunID),
		slog.Int("turns", len(session.History)),
		slog.Int64("sub_calls", budget.subCalls.Load()),
		slog.Uint64("queries", factory.queryIDs.Load()),
		slog.Int64("max_depth_reached", budget.maxDepthSeen.Load()),
		slog.String("finished_by", controller.FinishReason()),
	)

	emitter.Final(answer)

	return answer, nil
}

// newTreeBudget builds the shared, tree-wide §6 budget for one investigation. The
// sub-call ceiling is seeded from deps so the §6 cap on total sub-calls holds
// across the whole tree rather than per controller loop. The §6 per-path recursion
// depth is NOT enforced here: that is the job of RecursionContext.CanRecurse, which
// stays correct under the concurrent QueryBatched fan-out where a shared atomic
// gauge would conflate breadth with depth. The budget's depth gauge is instead a
// backstop on concurrently-active recursion frames; since each live frame holds a
// reserved sub-call, it is sized to the sub-call budget so it never refuses a
// frame the sub-call budget would still admit. The per-eval idle timeout keeps its
// NewBudget default; each child controller loop builds its own fresh Budget.
func newTreeBudget(deps *Deps) *Budget {
	budget := NewBudget()
	budget.MaxSubCalls = deps.MaxSubCalls
	budget.MaxDepth = deps.MaxSubCalls

	return budget
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
