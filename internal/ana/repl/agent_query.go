package repl

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/samber/oops"
)

const (
	// DefaultMaxDepth is the §6 recursion-depth ceiling for a session's sub-calls:
	// a controller running at depth 0 may orchestrate sub-LLM fan-out down to this
	// many levels before agent.Query refuses to recurse further.
	DefaultMaxDepth = 3
	// DefaultMaxSubCalls is the §6 per-session sub-call budget: the total number of
	// agent.Query and agent.QueryBatched sub-calls a single investigation may spend
	// before the budget is exhausted.
	DefaultMaxSubCalls = 30
)

// SubLLM is the bounded, non-recursive sub-call seam that agent.Query and
// agent.QueryBatched drive. Each Sub answers prompt using only the rendered
// evidence and returns the reply text, so the repl package forwards sub-calls to
// the real internal/openai sub-LLM caller in production and to a testify mock
// under test. The seam carries no context.Context: a session threads cancellation
// and the §6 wall-time budget through a closure around the live client, keeping
// the agent layer free of a stored context.
type SubLLM interface {
	// Sub answers prompt over the rendered evidence and returns the reply text.
	Sub(prompt, evidence string) (string, error)
}

// QueryBudget caps the bounded sub-call fan-out a session may drive. agent.Query
// and agent.QueryBatched refuse, with a clear error written to the interpreter's
// stdout and surfaced from Eval, once either ceiling is reached.
type QueryBudget struct {
	// MaxDepth is the deepest recursion level a sub-call may run at (§6: 3).
	MaxDepth int
	// MaxSubCalls is the most sub-calls a session may spend in total (§6: 30).
	MaxSubCalls int
}

// RegisterQuery exposes the bounded sub-call primitives agent.Query and
// agent.QueryBatched to interpreter, so controller source can fan a question out
// to sub against the depth and sub-call ceilings in budget. Because mvm replaces a
// package's symbol table wholesale on import, RegisterQuery re-emits the terminal
// primitives RegisterAgent installed (FINAL, FINAL_VAR, Cite) alongside the query
// pair, so the two registrations coexist on the agent package regardless of order.
func RegisterQuery(interpreter *Interpreter, sub SubLLM, budget QueryBudget) {
	runner := &queryRunner{
		sub:         sub,
		interpreter: interpreter,
		budget:      budget,
		mu:          sync.Mutex{},
		subCalls:    0,
		depth:       0,
	}

	symbols := map[string]reflect.Value{
		"Query":        reflect.ValueOf(runner.query),
		"QueryBatched": reflect.ValueOf(runner.queryBatched),
	}

	mergeAgentSurface(interpreter, symbols)
	importSurface(interpreter, "agent", symbols)
}

// mergeAgentSurface adds the terminal primitives of the Agent already bound to
// interpreter into symbols, so re-importing the agent package preserves FINAL,
// FINAL_VAR and Cite rather than dropping them. It is a no-op when no Agent is
// bound, which is the case when a session registers the query surface alone.
func mergeAgentSurface(interpreter *Interpreter, symbols map[string]reflect.Value) {
	agent, ok := loadAgent(interpreter)
	if !ok {
		return
	}

	symbols["FINAL"] = reflect.ValueOf(agent.recordFinal)
	symbols["FINAL_VAR"] = reflect.ValueOf(agent.recordFinalVar)
	symbols["Cite"] = reflect.ValueOf(agent.cite)
}

// renderEvidence renders the ctx value agent.Query was handed into the evidence
// string a sub-call ships. SPEC §7 admits any %v-renderable Go value as ctx —
// commonly []journal.Entry, []string or map[string]int — so a plain fmt render is
// the contract; the openai sub-call caller truncates oversized evidence later.
func renderEvidence(ctx any) string {
	return fmt.Sprint(ctx)
}

// queryRunner backs agent.Query and agent.QueryBatched for one session. It holds
// the SubLLM seam the calls drive and the interpreter whose captured stdout a
// budget breach is reported on, and tracks the sub-calls spent against the
// session budget. The counter is mutex-guarded because §11 warns that
// QueryBatched fan-out and a future recursive caller may touch it concurrently.
// The depth field stays 0 in the current non-recursive seam — nothing bumps it —
// so the §6 depth ceiling only fires when a caller sets MaxDepth 0; depth is
// scaffolding for a future recursive sub-caller that would increment it.
type queryRunner struct {
	sub         SubLLM
	interpreter *Interpreter
	budget      QueryBudget
	mu          sync.Mutex
	subCalls    int
	depth       int
}

// query backs agent.Query: it reserves one sub-call against the budget, renders
// ctx into evidence, makes the single non-recursive sub-LLM call and returns its
// reply. A budget breach or a failed sub-call ends the turn through fail, so the
// controller sees the error in stdout and Eval reports it.
func (runner *queryRunner) query(prompt string, ctx any) string {
	runner.guard(1)

	reply, err := runner.callSub(prompt, ctx)
	if err != nil {
		runner.fail(oops.In("repl").Code("sub_call_failed").Wrapf(err, "agent.Query sub-call failed"))
	}

	return reply
}

// queryBatched backs agent.QueryBatched: it reserves one sub-call per prompt, fans
// the (prompt, ctx) pairs out concurrently and returns their replies in the input
// order. A length mismatch or an exhausted budget ends the turn before any
// sub-call runs.
func (runner *queryRunner) queryBatched(prompts []string, ctxs []any) []string {
	if len(prompts) != len(ctxs) {
		runner.fail(oops.
			In("repl").
			Code("query_arity_mismatch").
			Errorf("agent.QueryBatched needs one ctx per prompt: got %d prompts and %d ctxs",
				len(prompts), len(ctxs)))
	}

	runner.guard(len(prompts))

	return runner.fanOut(prompts, ctxs)
}

// fanOut runs one sub-call per (prompt, ctx) pair across host goroutines and
// collects their replies into a slice indexed by position, so the results return
// in the input order regardless of completion order. It waits for every sub-call
// before reporting the first failure, so no goroutine outlives the turn.
func (runner *queryRunner) fanOut(prompts []string, ctxs []any) []string {
	replies := make([]string, len(prompts))
	failures := make([]error, len(prompts))
	group := sync.WaitGroup{}

	for index := range prompts {
		group.Go(func() {
			replies[index], failures[index] = runner.callSub(prompts[index], ctxs[index])
		})
	}

	group.Wait()
	runner.failOnAny(failures)

	return replies
}

// failOnAny ends the turn through fail at the first non-nil sub-call error, so a
// single failed branch of a fan-out surfaces to the controller rather than being
// swallowed by the surrounding successes.
func (runner *queryRunner) failOnAny(failures []error) {
	for _, failure := range failures {
		if failure != nil {
			runner.fail(oops.
				In("repl").
				Code("sub_call_failed").
				Wrapf(failure, "agent.QueryBatched sub-call failed"))
		}
	}
}

// callSub renders ctx into evidence and makes one sub-LLM call through the seam,
// returning the reply and any error for the caller to act on.
func (runner *queryRunner) callSub(prompt string, ctx any) (string, error) {
	return runner.sub.Sub(prompt, renderEvidence(ctx))
}

// guard reserves calls sub-calls against the budget and ends the turn through
// fail when the depth or sub-call ceiling is reached, so the controller never
// overruns the §6 limits.
func (runner *queryRunner) guard(calls int) {
	if err := runner.reserve(calls); err != nil {
		runner.fail(err)
	}
}

// reserve charges calls sub-calls to the session budget under the mutex,
// returning a depth or budget error without charging anything when the reservation
// would breach a ceiling. It checks depth before the sub-call count so a session
// forbidden from recursing reports that first.
func (runner *queryRunner) reserve(calls int) error {
	runner.mu.Lock()
	defer runner.mu.Unlock()

	if runner.depth >= runner.budget.MaxDepth {
		return oops.
			In("repl").
			Code("recursion_depth_exhausted").
			Errorf("agent.Query at depth %d cannot recurse further: max recursion depth is %d",
				runner.depth, runner.budget.MaxDepth)
	}

	if runner.subCalls+calls > runner.budget.MaxSubCalls {
		return oops.
			In("repl").
			Code("sub_call_budget_exhausted").
			Errorf("agent sub-call budget exhausted: %d of %d sub-calls spent, %d more requested",
				runner.subCalls, runner.budget.MaxSubCalls, calls)
	}

	runner.subCalls += calls

	return nil
}

// fail reports err on the interpreter's captured stdout so the controller sees the
// breach in its next-turn context, then panics with err so the turn ends and
// Interpreter.Eval surfaces the same error. This is the §6 contract that a budget
// breach is a clear error visible in mvm stdout.
func (runner *queryRunner) fail(err error) {
	runner.report(err.Error())
	panic(err)
}

// report writes notice as a line to the interpreter's stdout buffer, the same
// buffer interpreted fmt.Print output is captured into. The bytes.Buffer write
// cannot fail; the error is handled only to satisfy the linter and would itself
// surface through Eval if it ever did.
func (runner *queryRunner) report(notice string) {
	if _, err := fmt.Fprintln(&runner.interpreter.stdout, notice); err != nil {
		panic(oops.In("repl").Code("stdout_unwritable").Wrapf(err, "report agent budget breach"))
	}
}
