package repl_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/repl"
)

// mockSubLLM is a testify mock of the repl.SubLLM sub-call seam. agent.Query and
// agent.QueryBatched drive Sub once per (prompt, ctx) pair, so the test scripts the
// reply with .On("Sub", prompt, evidence).Return(reply, nil) and the recorder
// proves the rendered evidence reached the seam — making a testify mock the right
// double rather than a bespoke fake.
type mockSubLLM struct {
	mock.Mock
}

// Sub records prompt and evidence and replays the reply scripted via
// .On("Sub", prompt, evidence).Return(reply, err).
func (m *mockSubLLM) Sub(prompt, evidence string) (string, error) {
	args := m.Called(prompt, evidence)

	return args.String(0), args.Error(1)
}

// compile-time assertion that mockSubLLM satisfies the SubLLM seam.
var _ repl.SubLLM = (*mockSubLLM)(nil)

// fullBudget returns a QueryBudget with the SPEC §6 ceilings, the budget a healthy
// session runs under, so a test that is not probing a guard reads unambiguously.
func fullBudget() repl.QueryBudget {
	return repl.QueryBudget{MaxDepth: repl.DefaultMaxDepth, MaxSubCalls: repl.DefaultMaxSubCalls}
}

// TestQueryReturnsSubReplyWithRenderedContext registers the query surface, evaluates
// agent.Query with a []string ctx, and proves the reply crosses back as the turn's
// retval while the seam saw the prompt and the ctx rendered to evidence — the §7
// contract that ctx is handed to the sub-call as %v-rendered text.
func TestQueryReturnsSubReplyWithRenderedContext(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	sub := new(mockSubLLM)
	sub.On("Sub", "summarize the unit", "[a]").Return("the unit looks healthy", nil)

	repl.RegisterQuery(interpreter, sub, fullBudget())

	result, err := interpreter.Eval("turn_0", `agent.Query("summarize the unit", []string{"a"})`)
	require.NoError(t, err)

	require.True(t, result.Retval.IsValid(), "the sub-LLM reply crosses back as the turn retval")
	assert.Equal(t, "the unit looks healthy", result.Retval.String())

	sub.AssertExpectations(t)
	sub.AssertCalled(t, "Sub", "summarize the unit", "[a]")
}

// TestQueryBatchedReturnsRepliesInOrder evaluates agent.QueryBatched over two
// (prompt, ctx) pairs and proves both replies cross back in the input order with
// len == 2, so the parallel fan-out preserves position regardless of completion
// order and each pair reached the seam with its own rendered evidence.
func TestQueryBatchedReturnsRepliesInOrder(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	sub := new(mockSubLLM)
	sub.On("Sub", "summarize boot a", "[entry-a]").Return("boot a degraded", nil)
	sub.On("Sub", "summarize boot b", "[entry-b]").Return("boot b healthy", nil)

	repl.RegisterQuery(interpreter, sub, fullBudget())

	const src = `replies := agent.QueryBatched(
	[]string{"summarize boot a", "summarize boot b"},
	[]any{[]string{"entry-a"}, []string{"entry-b"}},
)
fmt.Println(replies[0])
fmt.Println(replies[1])
len(replies)`

	result, err := interpreter.Eval("turn_0", src)
	require.NoError(t, err)

	assert.Equal(t, "boot a degraded\nboot b healthy\n", result.Stdout,
		"the batched replies print back in the input order")

	require.True(t, result.Retval.IsValid(), "len(replies) crosses back as the turn retval")
	assert.Equal(t, int64(2), result.Retval.Int())

	sub.AssertExpectations(t)
	sub.AssertNumberOfCalls(t, "Sub", 2)
}

// TestQueryBudgetExhaustionSurfacesInResult registers the query surface with a
// zero sub-call budget and proves an agent.Query call ends the turn with a
// budget-exceeded error: the message is written to the captured stdout, Eval
// reports it, no retval crosses back, and the seam is never reached.
func TestQueryBudgetExhaustionSurfacesInResult(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	sub := new(mockSubLLM)

	repl.RegisterQuery(interpreter, sub, repl.QueryBudget{MaxDepth: repl.DefaultMaxDepth, MaxSubCalls: 0})

	result, err := interpreter.Eval("turn_0", `agent.Query("p", []string{"a"})`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sub-call budget exhausted")

	assert.Contains(t, result.Stdout, "sub-call budget exhausted",
		"the budget breach is visible in the turn's stdout")
	assert.False(t, result.Retval.IsValid(), "a budget-blocked turn yields no retval")

	sub.AssertNotCalled(t, "Sub")
}

// TestQueryDepthGuardForbidsRecursionBeyondCeiling registers the query surface
// with a zero depth ceiling and proves an agent.Query call is refused before any
// sub-call: the depth guard ends the turn with a recursion-depth error in stdout
// and from Eval, distinct from the sub-call budget guard.
func TestQueryDepthGuardForbidsRecursionBeyondCeiling(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	sub := new(mockSubLLM)

	repl.RegisterQuery(interpreter, sub, repl.QueryBudget{MaxDepth: 0, MaxSubCalls: repl.DefaultMaxSubCalls})

	result, err := interpreter.Eval("turn_0", `agent.Query("p", []string{"a"})`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max recursion depth")

	assert.Contains(t, result.Stdout, "max recursion depth",
		"the depth breach is visible in the turn's stdout")
	assert.False(t, result.Retval.IsValid(), "a depth-blocked turn yields no retval")

	sub.AssertNotCalled(t, "Sub")
}

// TestRegisterQueryPreservesAgentTerminalPrimitives registers the agent terminal
// surface and then the query surface on the same interpreter, and proves both
// coexist: agent.Query runs and its reply, assigned across turns, resolves through
// agent.FINAL_VAR — so re-importing the agent package did not drop FINAL_VAR.
func TestRegisterQueryPreservesAgentTerminalPrimitives(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	repl.RegisterAgent(interpreter, new(mockCitationSink))

	sub := new(mockSubLLM)
	sub.On("Sub", "is ssh healthy?", "[a]").Return("ssh is healthy", nil)

	repl.RegisterQuery(interpreter, sub, fullBudget())

	_, err := interpreter.Eval("turn_0", `summary := agent.Query("is ssh healthy?", []string{"a"})`)
	require.NoError(t, err)

	_, err = interpreter.Eval("turn_1", `agent.FINAL_VAR("summary")`)
	require.NoError(t, err)

	answer, ok := interpreter.Final()
	require.True(t, ok, "FINAL_VAR still resolves after the query surface is registered")
	assert.Equal(t, "ssh is healthy", answer)

	sub.AssertCalled(t, "Sub", "is ssh healthy?", "[a]")
}

// TestRegisterAgentPreservesQueryPrimitives registers the query surface first and
// the agent terminal surface second — the reverse of the production order — and
// proves both still coexist: agent.Query runs and its reply, assigned across
// turns, resolves through agent.FINAL_VAR, so re-importing the agent package from
// RegisterAgent re-emitted Query/QueryBatched rather than dropping them. Paired
// with TestRegisterQueryPreservesAgentTerminalPrimitives it proves the two
// registrations are order-independent.
func TestRegisterAgentPreservesQueryPrimitives(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	sub := new(mockSubLLM)
	sub.On("Sub", "is ssh healthy?", "[a]").Return("ssh is healthy", nil)

	repl.RegisterQuery(interpreter, sub, fullBudget())
	repl.RegisterAgent(interpreter, new(mockCitationSink))

	_, err := interpreter.Eval("turn_0", `summary := agent.Query("is ssh healthy?", []string{"a"})`)
	require.NoError(t, err)

	_, err = interpreter.Eval("turn_1", `agent.FINAL_VAR("summary")`)
	require.NoError(t, err)

	answer, ok := interpreter.Final()
	require.True(t, ok, "FINAL_VAR resolves after the agent surface is registered second")
	assert.Equal(t, "ssh is healthy", answer)

	sub.AssertCalled(t, "Sub", "is ssh healthy?", "[a]")
}

// TestQueryBatchedRejectsArityMismatch proves agent.QueryBatched aborts the turn
// before any sub-call when handed more prompts than ctxs: the arity-mismatch error
// reaches stdout and Eval, no retval crosses back, and the seam is never touched.
func TestQueryBatchedRejectsArityMismatch(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	sub := new(mockSubLLM)

	repl.RegisterQuery(interpreter, sub, fullBudget())

	const src = `agent.QueryBatched([]string{"a", "b"}, []any{[]string{"x"}})`

	result, err := interpreter.Eval("turn_0", src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "one ctx per prompt")

	assert.Contains(t, result.Stdout, "one ctx per prompt",
		"the arity mismatch is visible in the turn's stdout")
	assert.False(t, result.Retval.IsValid(), "an aborted batch yields no retval")

	sub.AssertNotCalled(t, "Sub")
}

// TestQuerySurfacesSubCallFailure proves a failed sub-LLM call ends the turn through
// fail: agent.Query reports the failure on stdout, Eval surfaces it, and no reply
// crosses back as the turn retval.
func TestQuerySurfacesSubCallFailure(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	sub := new(mockSubLLM)
	sub.On("Sub", "p", "[a]").Return("", assert.AnError)

	repl.RegisterQuery(interpreter, sub, fullBudget())

	result, err := interpreter.Eval("turn_0", `agent.Query("p", []string{"a"})`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sub-call failed")

	assert.Contains(t, result.Stdout, "sub-call failed",
		"the sub-call failure is visible in the turn's stdout")
	assert.False(t, result.Retval.IsValid(), "a failed sub-call yields no retval")

	sub.AssertCalled(t, "Sub", "p", "[a]")
}

// TestQueryBatchedSurfacesSubCallFailure proves a single failed branch of a
// QueryBatched fan-out surfaces rather than being swallowed by the successful
// branch: failOnAny ends the turn with the sub-call failure on stdout and from Eval.
func TestQueryBatchedSurfacesSubCallFailure(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	sub := new(mockSubLLM)
	sub.On("Sub", "boot a", "[entry-a]").Return("", assert.AnError)
	sub.On("Sub", "boot b", "[entry-b]").Return("boot b healthy", nil)

	repl.RegisterQuery(interpreter, sub, fullBudget())

	const src = `agent.QueryBatched(
	[]string{"boot a", "boot b"},
	[]any{[]string{"entry-a"}, []string{"entry-b"}},
)`

	result, err := interpreter.Eval("turn_0", src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sub-call failed")

	assert.Contains(t, result.Stdout, "sub-call failed",
		"the failed fan-out branch is visible in the turn's stdout")
	assert.False(t, result.Retval.IsValid(), "a failed batch yields no retval")
}
