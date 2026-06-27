package rlm

import (
	"context"
	"fmt"
	"sync"

	"github.com/samber/lo"
	"github.com/samber/oops"

	"github.com/omarluq/anamnesis/internal/ana/citations"
	"github.com/omarluq/anamnesis/internal/ana/journal"
)

// finalKind records which terminal primitive, if any, the controller has called
// to end the investigation.
type finalKind int

const (
	// finalNone means no terminal primitive has been called yet.
	finalNone finalKind = iota
	// finalLiteral means Final supplied the answer as a literal string.
	finalLiteral
	// finalVariable means FinalVar named a REPL variable holding the answer.
	finalVariable
)

// Agent is the §7 RLM primitive surface a single investigation exposes to the
// controller's interpreted code: Query for recursive sub-LLM calls, Cite for
// evidence tracking, and Final / FinalVar for the terminal signal. It is built
// from one Session per run with NewAgent; the zero value is not usable because
// the sub-call seam, budget, store, and emitter must all be wired first.
//
// Query touches only concurrency-safe collaborators and is safe for the parallel
// fan-out QueryBatched will add; the terminal signals Final and FinalVar are
// driven sequentially by the single controller loop.
type Agent struct {
	// subCall answers one rendered sub-LLM call, bound to the run's context so
	// the §7 Query(prompt, ctx) surface needs no Go context parameter.
	subCall func(prompt, evidence string) (string, error)
	// budget reserves one sub-call per Query against the session hard limits.
	budget *Budget
	// store records the entries Cite attaches to the final answer.
	store *citations.Store
	// emitter publishes the sub-call trace events Query produces.
	emitter *Emitter
	// result holds the literal answer once Final has been called.
	result string
	// varName holds the REPL variable name once FinalVar has been called.
	varName string
	// kind tracks which terminal primitive, if any, has been called.
	kind finalKind
}

// NewAgent builds the primitive surface for one run from session, binding ctx as
// the context every recursive sub-LLM call is made under. The returned Agent
// drives session.Sub for Query, reserves against session.Budget, records into
// session.Store for Cite, and emits onto session.Emitter.
func NewAgent(ctx context.Context, session *Session) *Agent {
	sub := session.Sub

	return &Agent{
		subCall: func(prompt, evidence string) (string, error) {
			return sub.Answer(ctx, prompt, evidence)
		},
		budget:  session.Budget,
		store:   session.Store,
		emitter: session.Emitter,
		result:  "",
		varName: "",
		kind:    finalNone,
	}
}

// Query makes one recursive sub-LLM call: it reserves a sub-call against the
// budget, renders ctx as the bounded evidence the sub-LLM reasons over, emits a
// sub-call trace event, and returns the sub-LLM's terse answer. It implements the
// §7 agent.Query(prompt, ctx) primitive, so it takes no Go context. It panics
// with a clear oops error when the sub-call budget is exhausted or the sub-LLM
// call fails, so the controller loop surfaces the fault as a turn error rather
// than letting a silent empty answer through.
func (agent *Agent) Query(prompt string, ctx any) string {
	answer, err := agent.queryOne(prompt, ctx)
	if err != nil {
		panic(err)
	}

	return answer
}

// QueryBatched is the parallel fan-out of Query: it runs one sub-LLM call per
// (prompt, ctx) pair, each in its own host goroutine, and returns the answers in
// input order. Every pair reserves one sub-call against the budget and emits its
// own sub-call trace event, so a batch of N pairs spends exactly N of the
// sub-call budget. It implements the §7 agent.QueryBatched primitive, so it takes
// no Go context. It collapses N round-trips of latency into roughly one (SPEC
// §18) while keeping the shared counters and store concurrency-safe: the budget
// counters are atomic and the citation store is mutex-guarded, so the fan-out
// never races them. It panics with a clear oops error when prompts and ctxs
// differ in length, when the sub-call budget cannot cover a pair, or when a
// sub-LLM call fails, matching Query's fail-loud contract so the controller loop
// surfaces the fault as a turn error.
func (agent *Agent) QueryBatched(prompts []string, ctxs []any) []string {
	if len(prompts) != len(ctxs) {
		panic(oops.
			In("rlm").
			Code("agent_batch_mismatch").
			Errorf("agent.QueryBatched needs one ctx per prompt: got %d prompts and %d ctxs",
				len(prompts), len(ctxs)))
	}

	answers := make([]string, len(prompts))
	failures := make([]error, len(prompts))

	var waitGroup sync.WaitGroup

	waitGroup.Add(len(prompts))

	for index := range prompts {
		go func() {
			defer waitGroup.Done()

			answers[index], failures[index] = agent.queryOne(prompts[index], ctxs[index])
		}()
	}

	waitGroup.Wait()

	if failure := firstError(failures); failure != nil {
		panic(failure)
	}

	return answers
}

// Cite attaches entries, by cursor, to the final answer. Repeated calls
// accumulate; the session store later rejects any cited cursor a journal query
// never returned this session. It implements the §7 agent.Cite primitive.
func (agent *Agent) Cite(entries []journal.Entry) {
	agent.store.Cite(entries)
}

// Final records that the answer is the literal string answer, implementing the
// §7 agent.FINAL terminal signal.
func (agent *Agent) Final(answer string) {
	agent.result = answer
	agent.kind = finalLiteral
}

// FinalVar records that the answer is the current value of the REPL variable
// named varname, implementing the §7 agent.FINAL_VAR terminal signal for an
// answer assembled across turns.
func (agent *Agent) FinalVar(varname string) {
	agent.varName = varname
	agent.kind = finalVariable
}

// Done reports whether the controller has signaled a terminal answer through
// either Final or FinalVar.
func (agent *Agent) Done() bool {
	return agent.kind != finalNone
}

// Literal returns the literal answer and true when Final set it, or the empty
// string and false otherwise.
func (agent *Agent) Literal() (string, bool) {
	return agent.result, agent.kind == finalLiteral
}

// Variable returns the REPL variable name and true when FinalVar set it, or the
// empty string and false otherwise.
func (agent *Agent) Variable() (string, bool) {
	return agent.varName, agent.kind == finalVariable
}

// queryOne makes one recursive sub-LLM call shared by Query and the QueryBatched
// fan-out: it reserves one sub-call, emits its trace event, and returns the
// sub-LLM's answer, or a clear oops error when the budget is spent or the sub-LLM
// call fails. Returning the fault instead of panicking lets QueryBatched join
// every goroutine and raise one deterministic panic from the calling goroutine,
// while Query turns the same fault straight into its own panic.
func (agent *Agent) queryOne(prompt string, ctx any) (string, error) {
	if agent.budget.ReserveSubCall() != nil {
		return "", oops.
			In("rlm").
			Code("agent_sub_call_budget").
			Errorf("agent sub-call exhausted the sub-call budget of %d", agent.budget.MaxSubCalls)
	}

	agent.emitter.SubCall(prompt)

	answer, err := agent.subCall(prompt, fmt.Sprint(ctx))
	if err != nil {
		return "", oops.
			In("rlm").
			Code("agent_sub_call_failed").
			Wrapf(err, "agent sub-LLM call failed")
	}

	return answer, nil
}

// firstError returns the first non-nil error in errs by index, or nil when every
// entry is nil. QueryBatched uses it to surface a deterministic batch failure
// regardless of the order its goroutines happened to finish in.
func firstError(errs []error) error {
	failure, _ := lo.Find(errs, func(candidate error) bool {
		return candidate != nil
	})

	return failure
}
