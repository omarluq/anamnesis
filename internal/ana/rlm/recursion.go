package rlm

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/samber/oops"

	"github.com/omarluq/anamnesis/internal/ana/citations"
	"github.com/omarluq/anamnesis/internal/ana/repl"
	"github.com/omarluq/anamnesis/internal/ana/scenarios"
)

// Degradation prefixes label the text a recursive sub-call returns to its parent
// when a sub-investigation cannot run or fails: SPEC §6 makes a budget breach or a
// failed child loop a graceful textual result the parent reasons over, never a Go
// error that would unwind the parent's turn.
const (
	subInvestigationSkipped = "sub-investigation skipped"
	subInvestigationFailed  = "sub-investigation failed"
)

// Sub-call routing paths recorded in the run's observability log: the branch resolve
// took for one agent.Query sub-call — degraded on a spent budget, a flat leaf call, or
// a recursive descent into a child controller loop.
const (
	subCallPathSkipped = "skipped"
	subCallPathLeaf    = "leaf"
	subCallPathRecurse = "recurse"
)

// RecursionContext is the immutable, server-side truth of how deep a sub-call
// sits in the investigation tree. A controller running at CurrentDepth may spawn a
// child controller loop one level deeper while CanRecurse holds; the leaf level
// (CurrentDepth == MaxDepth) falls back to a flat base-case sub-LLM call. Depth is
// never taken from a model-supplied value: the parent threads a Child of its own
// context, so the §6 "max recursion depth" bound cannot be talked past.
type RecursionContext struct {
	// CurrentDepth is the level this node runs at; the root is 0.
	CurrentDepth int
	// MaxDepth is the deepest level any node in the tree may run at.
	MaxDepth int
}

// Child returns the RecursionContext one level deeper, leaving MaxDepth fixed. The
// receiver is unchanged, so a parent can hand each child its own immutable depth.
func (rc RecursionContext) Child() RecursionContext {
	return RecursionContext{CurrentDepth: rc.CurrentDepth + 1, MaxDepth: rc.MaxDepth}
}

// CanRecurse reports whether this node may spawn a child controller loop, which it
// may while it sits above the leaf level. At CurrentDepth == MaxDepth it returns
// false and the caller takes the flat base-case path instead.
func (rc RecursionContext) CanRecurse() bool {
	return rc.CurrentDepth < rc.MaxDepth
}

// QueryTracer observes the lifecycle of one recursive agent.Query sub-call so the
// terminal layer can render it as a depth-indented query block. Every sub-call is
// minted a unique id the start and end share, so the renderer pairs an end with its
// own start even when parallel fan-out completes out of order. The rlm package owns
// this narrow seam and defaults to a no-op, so the recursion core never depends on
// the terminal or emitter package; the *Emitter satisfies it structurally through
// its QueryStart and QueryEnd methods, wired in at run.go.
type QueryTracer interface {
	// QueryStart marks the start of sub-call queryID carrying prompt at nesting depth.
	QueryStart(queryID uint64, prompt string, depth int)
	// QueryEnd marks the completion of sub-call queryID carrying result at depth.
	QueryEnd(queryID uint64, result string, depth int)
}

// noopQueryTracer is the default QueryTracer the recursion core runs under until a
// real tracer is wired: it drops every lifecycle event, so a run with no terminal
// attached neither blocks nor allocates on tracing.
type noopQueryTracer struct{}

// QueryStart drops the start event.
func (noopQueryTracer) QueryStart(_ uint64, _ string, _ int) {}

// QueryEnd drops the end event.
func (noopQueryTracer) QueryEnd(_ uint64, _ string, _ int) {}

// recursiveSub is the recursive sub-call adapter bound to one node of the
// investigation tree; it satisfies the repl.SubLLM seam agent.Query and
// agent.QueryBatched drive. A leaf node (CanRecurse false) makes a flat base-case
// sub-LLM call through leaf; a non-leaf node descends into a full child controller
// loop one level deeper and splices the child's FINAL answer back. Every call
// charges the shared, tree-wide sub-call budget, and a budget breach or a failed
// child loop degrades to text returned to the parent rather than a Go error, so a
// failed sub-investigation never unwinds the parent's turn.
type recursiveSub struct {
	// budget is the shared, tree-wide §6 budget every node in the tree charges.
	budget *Budget
	// queryIDs mints a unique id per sub-call, shared tree-wide so every sub-call —
	// including the concurrent fan-out QueryBatched drives through this one adapter —
	// gets a distinct id the tracer can pair its start and end on.
	queryIDs *atomic.Uint64
	// tracer observes this sub-call's start and end for the terminal layer.
	tracer QueryTracer
	// leaf makes the flat base-case sub-LLM call the leaf level falls back to.
	leaf func(prompt, evidence string) (string, error)
	// descend runs a full child controller loop at the given child context.
	descend func(child RecursionContext, prompt, evidence string) (string, error)
	// progress is the shared tree-wide work counter Sub bumps at each sub-call's start
	// and end, so a parent node's idle watchdog counts a descending child loop as
	// progress. It is nil where a unit test drives an adapter in isolation; bumpProgress
	// tolerates that.
	progress *atomic.Int64 `exhaustruct:"optional"`
	// rc is this node's immutable depth in the tree.
	rc RecursionContext
}

// Compile-time assertion that the adapter satisfies the repl sub-call seam.
var _ repl.SubLLM = (*recursiveSub)(nil)

// Sub answers one agent.Query sub-call: it mints a unique id, brackets the work
// with the tracer's start and end events sharing that id, then resolves the answer
// through the flat base case or a child controller loop. The events are stamped at
// CurrentDepth+1: the root node runs at depth 0, so its sub-calls render indented
// one level under their turn rather than flush with the code block. The returned
// error is non-nil only for a leaf base-case failure, which the interpreter
// surfaces as a turn error; budget breaches and child-loop failures come back as
// text with a nil error.
func (s *recursiveSub) Sub(prompt, evidence string) (string, error) {
	queryID := s.queryIDs.Add(1)
	depth := s.rc.CurrentDepth + 1

	s.bumpProgress()
	s.budget.RecordDepth(depth)
	s.tracer.QueryStart(queryID, prompt, depth)

	result, path, err := s.resolve(prompt, evidence)

	s.tracer.QueryEnd(queryID, result, depth)
	s.bumpProgress()

	slog.Info(
		"rlm sub-call",
		slog.Uint64("query_id", queryID),
		slog.Int("depth", depth),
		slog.String("path", path),
		slog.Int("prompt_len", len(prompt)),
		slog.Int("result_len", len(result)),
	)

	return result, err
}

// bumpProgress advances the shared tree-wide work counter so a parent node's idle
// watchdog counts this sub-call as progress rather than mistaking a busy descent for a
// wedge. It tolerates a nil counter so a unit test can drive an adapter in isolation
// without wiring one.
func (s *recursiveSub) bumpProgress() {
	if s.progress != nil {
		s.progress.Add(1)
	}
}

// resolve charges the shared sub-call budget and routes the call: an exhausted
// budget degrades to text, the leaf level takes the flat base case, and a non-leaf
// level descends one level deeper. It returns the routing path it took alongside the
// answer so Sub can record it in the run's observability log.
func (s *recursiveSub) resolve(prompt, evidence string) (answer, path string, err error) {
	if reserveErr := s.budget.ReserveSubCall(); reserveErr != nil {
		slog.Warn(
			"rlm budget breach",
			slog.String("budget", "sub_calls"),
			slog.Int("depth", s.rc.CurrentDepth+1),
		)

		return degradedText(subInvestigationSkipped, reserveErr), subCallPathSkipped, nil
	}

	if !s.rc.CanRecurse() {
		answer, err = s.leaf(prompt, evidence)

		return answer, subCallPathLeaf, err
	}

	return s.recurse(prompt, evidence)
}

// recurse claims one shared recursion frame, runs the child controller loop one
// level deeper, and releases the frame. The §6 per-path depth ceiling has already
// been enforced by CanRecurse on this node's immutable RecursionContext, which is
// correct under concurrent fan-out; EnterDepth is the tree-wide backstop on
// concurrently-active recursion frames, so a saturated frame budget or a failed
// child loop degrades to text the parent reasons over rather than a Go error. It
// reports the routing path it actually took alongside the answer: a saturated frame
// budget never recursed, so it logs as skipped, while a descend that ran — whether the
// child concluded or failed — logs as a recursion.
func (s *recursiveSub) recurse(prompt, evidence string) (answer, path string, err error) {
	if enterErr := s.budget.EnterDepth(); enterErr != nil {
		slog.Warn(
			"rlm budget breach",
			slog.String("budget", "depth"),
			slog.Int("depth", s.rc.CurrentDepth+1),
		)

		return degradedText(subInvestigationSkipped, enterErr), subCallPathSkipped, nil
	}

	defer s.budget.ExitDepth()

	answer, err = s.descend(s.rc.Child(), prompt, evidence)
	if err != nil {
		return degradedText(subInvestigationFailed, err), subCallPathRecurse, nil
	}

	return answer, subCallPathRecurse, nil
}

// degradedText renders a graceful sub-call degradation as the labeled text the
// parent receives in place of a sub-answer, so a budget breach or child-loop
// failure reads as a result rather than crashing the parent turn.
func degradedText(reason string, cause error) string {
	return reason + ": " + cause.Error()
}

// recursor builds the recursive sub-call adapters and child controller loops for
// one investigation tree. It captures the model seams and host surfaces every
// node reuses, the shared budget that caps the whole tree, the run's emitter, and
// the query tracer. The context each node runs under is threaded through method
// arguments and captured in the per-node closures, never stored on the struct, so
// the recursor holds no context.Context field.
type recursor struct {
	// deps carries the model seams and host surfaces every node reuses.
	deps *Deps
	// budget is the shared, tree-wide §6 budget every node charges.
	budget *Budget
	// emitter publishes each child controller loop's turn events.
	emitter *Emitter
	// tracer observes every sub-call's lifecycle for the terminal layer.
	tracer QueryTracer
	// queryIDs mints a tree-wide unique id per sub-call, shared into every node's
	// adapter so ids never collide across levels or across concurrent fan-out.
	queryIDs *atomic.Uint64
	// progress is the shared tree-wide work counter SetProgress installs on every
	// interpreter and recursiveSub.Sub bumps, so each node's idle watchdog sees the
	// whole tree's progress rather than only its own stdout.
	progress *atomic.Int64
}

// newRecursor binds the collaborators every node of one investigation tree reuses
// into the factory that builds its interpreters, adapters, and child loops. A nil
// tracer defaults to the no-op, so the recursion core runs with no terminal
// attached without the caller wiring a tracer. The fresh query-id counter is shared
// across every adapter the factory builds, so sub-call ids are unique tree-wide; the
// caller's progress counter is shared the same way so every node's idle watchdog reads
// one tree-wide work signal.
func newRecursor(deps *Deps, budget *Budget, emitter *Emitter, tracer QueryTracer, progress *atomic.Int64) *recursor {
	if tracer == nil {
		tracer = noopQueryTracer{}
	}

	return &recursor{
		deps:     deps,
		budget:   budget,
		emitter:  emitter,
		tracer:   tracer,
		queryIDs: new(atomic.Uint64),
		progress: progress,
	}
}

// interpreter assembles the mvm REPL session a controller at rc drives: the
// journal surface is decorated so every query records its returned entries as
// citable evidence in store, the citation store receives agent.Cite, and the
// sub-call seam is the recursive adapter bound at rc. It returns an oops error
// tagged with the rlm domain when repl.New rejects the assembled config.
func (r *recursor) interpreter(
	ctx context.Context,
	node RecursionContext,
	store *citations.Store,
) (*repl.Interpreter, error) {
	cfg := repl.Config{
		Host: repl.HostDeps{
			Journal: &recordingJournal{delegate: r.deps.Journal, store: store},
			Systemd: r.deps.Systemd,
		},
		Sink: store,
		Sub:  r.subFor(ctx, node),
		Budget: repl.QueryBudget{
			MaxDepth: repl.DefaultMaxDepth,
			// Seed the per-session sub-call guard from the shared tree budget rather
			// than the repl default, so a run configured with a larger Deps.MaxSubCalls
			// is capped by the tree-wide §6 budget instead of being rejected early.
			MaxSubCalls: r.budget.MaxSubCalls,
		},
	}

	interpreter, err := repl.New(&cfg)
	if err != nil {
		return nil, oops.
			In("rlm").
			Code("investigate_session_unbuilt").
			Wrapf(err, "assemble repl session")
	}

	// Share the run's one progress counter so this node's idle watchdog observes the
	// whole tree's work, not just what this interpreter prints to its own stdout.
	interpreter.SetProgress(r.progress)

	return interpreter, nil
}

// subFor builds the recursive sub-call seam a controller at rc drives. The leaf
// closure makes the flat base-case sub-LLM call under ctx, and the descend closure
// runs a child controller loop under ctx one level deeper; capturing ctx in the
// closures keeps it off the recursor and adapter structs.
func (r *recursor) subFor(ctx context.Context, node RecursionContext) *recursiveSub {
	return &recursiveSub{
		budget:   r.budget,
		queryIDs: r.queryIDs,
		tracer:   r.tracer,
		progress: r.progress,
		rc:       node,
		leaf: func(prompt, evidence string) (string, error) {
			return r.deps.Sub.Answer(ctx, prompt, evidence)
		},
		descend: func(child RecursionContext, prompt, evidence string) (string, error) {
			return r.runChild(ctx, child, prompt, evidence)
		},
	}
}

// runChild runs a full child controller loop for one recursive sub-call: a fresh
// interpreter, a fresh citation store, a fresh per-eval budget, and a sub-call seam
// one level deeper that shares the tree-wide budget. It returns the child's FINAL
// answer, or an error the calling adapter converts to graceful text.
func (r *recursor) runChild(
	ctx context.Context,
	child RecursionContext,
	prompt, evidence string,
) (string, error) {
	store := citations.NewStore()

	interpreter, err := r.interpreter(ctx, child, store)
	if err != nil {
		return "", err
	}

	session := r.childSession(store, prompt, evidence)

	answer, err := NewController(session, interpreter).Run(ctx)
	if err != nil {
		return "", err
	}

	return answer, nil
}

// childSession frames a child controller loop: the §14 sub-controller system
// prompt, the sub-question with its bounded context payload as the question, a
// fresh per-eval budget, and a fresh citation store, sharing the run's emitter so the
// child's turns stream onto the same trace channel. Like the root loop, a child runs
// unbounded by time or turn count — only the shared sub-call and depth budgets and the
// hard ctx bound it.
func (r *recursor) childSession(store *citations.Store, prompt, evidence string) *Session {
	budget := NewBudget()

	return &Session{
		Controller:   r.deps.Controller,
		Sub:          r.deps.Sub,
		Budget:       budget,
		Store:        store,
		Emitter:      r.emitter,
		Question:     composeChildQuestion(prompt, evidence),
		SystemPrompt: scenarios.SubControllerPrompt,
		History:      nil,
	}
}

// composeChildQuestion frames a recursive sub-call as a child investigation's
// question: the sub-prompt followed by the bounded context payload the parent
// handed down, so the child reasons from that evidence before issuing new queries.
func composeChildQuestion(prompt, evidence string) string {
	return prompt + "\n\nContext from the parent investigation:\n" + evidence
}
