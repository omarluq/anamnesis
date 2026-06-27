package repl

import "github.com/samber/oops"

// Config carries the live collaborators a REPL session is wired from: the host
// read surfaces interpreted code calls, the citation sink agent.Cite forwards to,
// the sub-LLM seam agent.Query and agent.QueryBatched drive, and the budget that
// caps that fan-out. The controller builds one Config per investigation and hands
// it to New; the zero value is not usable because every collaborator must be
// supplied before a session can evaluate a turn.
type Config struct {
	// Host bundles the journal and systemd read surfaces exposed as host packages.
	Host HostDeps
	// Sink receives the journal entries agent.Cite attaches to the final answer.
	Sink CitationSink
	// Sub answers the bounded sub-calls agent.Query and agent.QueryBatched drive.
	Sub SubLLM
	// Budget caps the recursion depth and per-session sub-call count of that fan-out.
	Budget QueryBudget
}

// New assembles a REPL session for the controller and returns the interpreter it
// will drive. The interpreter starts with the Go standard library loaded, then
// gains the journal and systemd host packages from cfg.Host and the agent
// primitive façade — FINAL, FINAL_VAR, Cite, Query and QueryBatched — wired to
// cfg.Sink and cfg.Sub under cfg.Budget. The returned *Interpreter satisfies the
// controller's eval seam, so the loop drives Eval each turn and resolves the
// terminal answer with Final. cfg is taken by pointer because the collaborator
// bundle is heavy and read-only here. It returns an oops error tagged with the
// repl domain when a collaborator is unset or a host surface lacks a declared method.
func New(cfg *Config) (*Interpreter, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	interpreter := NewInterpreter()

	if err := cfg.Host.Register(interpreter); err != nil {
		return nil, err
	}

	// RegisterAgent must precede RegisterQuery so the query surface re-emits the
	// terminal primitives onto the agent package rather than dropping them.
	RegisterAgent(interpreter, cfg.Sink)
	RegisterQuery(interpreter, cfg.Sub, cfg.Budget)

	return interpreter, nil
}

// validate reports the first unset collaborator as an oops error tagged with the
// repl domain, so New fails loudly at construction rather than panicking deep in
// an interpreted agent.Cite or agent.Query call once a nil sink or seam is reached.
// The Budget ceilings are guarded too: a zero MaxDepth or MaxSubCalls is treated as
// unset, since either leaves reserve refusing the first sub-call of every turn.
func (cfg *Config) validate() error {
	switch {
	case cfg == nil:
		return oops.In("repl").Code("session_config_nil").Errorf("repl.New: config is nil")
	case cfg.Host.Journal == nil:
		return oops.In("repl").Code("session_journal_unset").Errorf("repl.New: journal host surface is unset")
	case cfg.Host.Systemd == nil:
		return oops.In("repl").Code("session_systemd_unset").Errorf("repl.New: systemd host surface is unset")
	case cfg.Sink == nil:
		return oops.In("repl").Code("session_sink_unset").Errorf("repl.New: citation sink is unset")
	case cfg.Sub == nil:
		return oops.In("repl").Code("session_sub_unset").Errorf("repl.New: sub-LLM seam is unset")
	case cfg.Budget.MaxDepth <= 0:
		return oops.In("repl").Code("session_budget_depth_unset").Errorf("repl.New: budget MaxDepth must be positive")
	case cfg.Budget.MaxSubCalls <= 0:
		return oops.
			In("repl").
			Code("session_budget_calls_unset").
			Errorf("repl.New: budget MaxSubCalls must be positive")
	default:
		return nil
	}
}
