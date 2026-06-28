package rlm

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// tracedQuery records one QueryStart or QueryEnd lifecycle call.
type tracedQuery struct {
	text  string
	id    uint64
	depth int
}

// recordingTracer captures the QueryStart/QueryEnd lifecycle a recursiveSub
// drives, so a test can assert the id, depth, and text each event carried. The
// mutex keeps it safe for the concurrent fan-out paths even though the
// adapter-logic tests drive it sequentially.
type recordingTracer struct {
	startCalls []tracedQuery
	endCalls   []tracedQuery
	mu         sync.Mutex
}

// Compile-time assertion that the recorder satisfies the tracer seam.
var _ QueryTracer = (*recordingTracer)(nil)

// QueryStart records the start of sub-call queryID.
func (r *recordingTracer) QueryStart(queryID uint64, prompt string, depth int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.startCalls = append(r.startCalls, tracedQuery{text: prompt, id: queryID, depth: depth})
}

// QueryEnd records the completion of sub-call queryID.
func (r *recordingTracer) QueryEnd(queryID uint64, result string, depth int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.endCalls = append(r.endCalls, tracedQuery{text: result, id: queryID, depth: depth})
}

// mockLeafSub is a testify mock of the leaf sub-LLM seam the flat base case calls,
// so a test scripts the base-case reply with .On("Answer", ...).Return(...).
type mockLeafSub struct {
	mock.Mock
}

// Answer records its arguments and replays the scripted base-case reply.
func (m *mockLeafSub) Answer(ctx context.Context, prompt, evidence string) (string, error) {
	args := m.Called(ctx, prompt, evidence)

	return args.String(0), args.Error(1)
}

// failingDescend returns a descend closure that fails the test if a node descends
// into a child loop, used where the adapter must take the flat leaf path instead.
func failingDescend(t *testing.T) func(RecursionContext, string, string) (string, error) {
	t.Helper()

	return func(RecursionContext, string, string) (string, error) {
		t.Error("node must not descend into a child controller loop")

		return "", nil
	}
}

// failingLeaf returns a leaf closure that fails the test if a node takes the flat
// base case, used where the adapter must recurse or degrade instead.
func failingLeaf(t *testing.T) func(string, string) (string, error) {
	t.Helper()

	return func(string, string) (string, error) {
		t.Error("node must not take the flat base-case path")

		return "", nil
	}
}

func TestRecursionContextChildAndCanRecurse(t *testing.T) {
	t.Parallel()

	root := RecursionContext{CurrentDepth: 0, MaxDepth: 2}
	assert.True(t, root.CanRecurse(), "the root may recurse below MaxDepth")

	child := root.Child()
	assert.Equal(t, 1, child.CurrentDepth, "Child increments the depth")
	assert.Equal(t, 2, child.MaxDepth, "Child leaves MaxDepth fixed")
	assert.Equal(t, 0, root.CurrentDepth, "Child leaves the immutable receiver unchanged")

	leaf := child.Child()
	assert.Equal(t, 2, leaf.CurrentDepth, "Child climbs to MaxDepth")
	assert.False(t, leaf.CanRecurse(), "a node at MaxDepth is a leaf")
}

func TestRecursiveSubLeafFallsBackToFlatCall(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	budget := NewBudget()
	tracer := new(recordingTracer)
	leafSub := new(mockLeafSub)
	leafSub.On("Answer", ctx, "leaf prompt", "[evidence]").Return("flat answer", nil).Once()

	sub := &recursiveSub{
		queryIDs: new(atomic.Uint64),
		budget:   budget,
		tracer:   tracer,
		rc:       RecursionContext{CurrentDepth: 2, MaxDepth: 2},
		leaf:     func(prompt, evidence string) (string, error) { return leafSub.Answer(ctx, prompt, evidence) },
		descend:  failingDescend(t),
	}

	answer, err := sub.Sub("leaf prompt", "[evidence]")
	require.NoError(t, err)
	assert.Equal(t, "flat answer", answer, "a leaf node returns the flat base-case reply")
	leafSub.AssertExpectations(t)

	require.Len(t, tracer.startCalls, 1)
	assert.Equal(t, 3, tracer.startCalls[0].depth, "the leaf sub-call renders one level under its node depth")
}

func TestRecursiveSubRecursesAndTracesDepth(t *testing.T) {
	t.Parallel()

	budget := NewBudget()
	tracer := new(recordingTracer)

	var descended RecursionContext

	sub := &recursiveSub{
		queryIDs: new(atomic.Uint64),
		budget:   budget,
		tracer:   tracer,
		rc:       RecursionContext{CurrentDepth: 0, MaxDepth: 3},
		leaf:     failingLeaf(t),
		descend: func(child RecursionContext, _, _ string) (string, error) {
			descended = child

			return "child answer", nil
		},
	}

	answer, err := sub.Sub("decompose", "[evidence]")
	require.NoError(t, err)
	assert.Equal(t, "child answer", answer, "a non-leaf node splices the child's answer back")
	assert.Equal(t, 1, descended.CurrentDepth, "the child runs one level deeper")
	assert.Equal(t, 3, descended.MaxDepth, "the child keeps the tree's MaxDepth")

	require.Len(t, tracer.startCalls, 1)
	assert.Equal(t, 1, tracer.startCalls[0].depth, "the root sub-call renders one level under the depth-0 turn")
	assert.Equal(t, uint64(1), tracer.startCalls[0].id, "the first sub-call is minted id 1")
	require.Len(t, tracer.endCalls, 1)
	assert.Equal(t, tracer.startCalls[0].id, tracer.endCalls[0].id, "the end shares its start's id")
	assert.Equal(t, "child answer", tracer.endCalls[0].text, "QueryEnd carries the spliced result")
}

func TestRecursiveSubSubCallBudgetExhaustedReturnsText(t *testing.T) {
	t.Parallel()

	budget := NewBudget()
	budget.MaxSubCalls = 0
	tracer := new(recordingTracer)

	sub := &recursiveSub{
		queryIDs: new(atomic.Uint64),
		budget:   budget,
		tracer:   tracer,
		rc:       RecursionContext{CurrentDepth: 0, MaxDepth: 3},
		leaf:     failingLeaf(t),
		descend:  failingDescend(t),
	}

	answer, err := sub.Sub("anything", "[evidence]")
	require.NoError(t, err, "an exhausted sub-call budget degrades to text, not a Go error")
	assert.Contains(t, answer, "sub-investigation skipped")
	assert.Contains(t, answer, "sub-call budget")
}

func TestRecursiveSubDepthBudgetExhaustedReturnsText(t *testing.T) {
	t.Parallel()

	budget := NewBudget()
	budget.MaxDepth = 1
	tracer := new(recordingTracer)

	// Saturate the shared depth gauge so the adapter's EnterDepth is refused.
	require.NoError(t, budget.EnterDepth())

	sub := &recursiveSub{
		queryIDs: new(atomic.Uint64),
		budget:   budget,
		tracer:   tracer,
		rc:       RecursionContext{CurrentDepth: 0, MaxDepth: 1},
		leaf:     failingLeaf(t),
		descend:  failingDescend(t),
	}

	answer, err := sub.Sub("decompose", "[evidence]")
	require.NoError(t, err, "an exhausted depth budget degrades to text, not a Go error")
	assert.Contains(t, answer, "sub-investigation skipped")
	assert.Contains(t, answer, "depth")
}

func TestRecursiveSubChildFailureReturnsText(t *testing.T) {
	t.Parallel()

	budget := NewBudget()
	tracer := new(recordingTracer)

	sub := &recursiveSub{
		queryIDs: new(atomic.Uint64),
		budget:   budget,
		tracer:   tracer,
		rc:       RecursionContext{CurrentDepth: 0, MaxDepth: 3},
		leaf:     failingLeaf(t),
		descend: func(RecursionContext, string, string) (string, error) {
			return "", errors.New("child controller stalled")
		},
	}

	answer, err := sub.Sub("decompose", "[evidence]")
	require.NoError(t, err, "a failed child loop degrades to text, not a Go error")
	assert.Contains(t, answer, "sub-investigation failed")
	assert.Contains(t, answer, "child controller stalled")
}

func TestSharedBudgetCapsTwoLevelTreeNotPerChild(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	budget := NewBudget()
	budget.MaxSubCalls = 2
	budget.MaxDepth = 1
	tracer := new(recordingTracer)
	leafSub := new(mockLeafSub)
	leafSub.On("Answer", ctx, "leaf", "[x]").Return("leaf answer", nil).Once()

	// One query-id counter is shared across both levels, mirroring the tree-wide
	// counter newRecursor threads into every node's adapter in production.
	queryIDs := new(atomic.Uint64)

	// The leaf adapter stands in for the child controller's own agent.Query.
	leaf := &recursiveSub{
		queryIDs: queryIDs,
		budget:   budget,
		tracer:   tracer,
		rc:       RecursionContext{CurrentDepth: 1, MaxDepth: 1},
		leaf:     func(prompt, evidence string) (string, error) { return leafSub.Answer(ctx, prompt, evidence) },
		descend:  failingDescend(t),
	}
	root := &recursiveSub{
		queryIDs: queryIDs,
		budget:   budget,
		tracer:   tracer,
		rc:       RecursionContext{CurrentDepth: 0, MaxDepth: 1},
		leaf:     failingLeaf(t),
		descend: func(RecursionContext, string, string) (string, error) {
			return leaf.Sub("leaf", "[x]")
		},
	}

	answer, err := root.Sub("decompose", "[evidence]")
	require.NoError(t, err)
	assert.Equal(t, "leaf answer", answer)
	leafSub.AssertExpectations(t)

	// The root spent one sub-call and the leaf the second: the whole 2-level tree
	// shares one cap, so nothing remains.
	require.Error(t, budget.ReserveSubCall(), "the shared sub-call budget is spent across both levels")
}

func TestSharedBudgetCapFiresAcrossTwoLevelTree(t *testing.T) {
	t.Parallel()

	budget := NewBudget()
	budget.MaxSubCalls = 1
	budget.MaxDepth = 1
	tracer := new(recordingTracer)
	leafSub := new(mockLeafSub)

	queryIDs := new(atomic.Uint64)

	leaf := &recursiveSub{
		queryIDs: queryIDs,
		budget:   budget,
		tracer:   tracer,
		rc:       RecursionContext{CurrentDepth: 1, MaxDepth: 1},
		leaf: func(prompt, evidence string) (string, error) {
			return leafSub.Answer(context.Background(), prompt, evidence)
		},
		descend: failingDescend(t),
	}
	root := &recursiveSub{
		queryIDs: queryIDs,
		budget:   budget,
		tracer:   tracer,
		rc:       RecursionContext{CurrentDepth: 0, MaxDepth: 1},
		leaf:     failingLeaf(t),
		descend: func(RecursionContext, string, string) (string, error) {
			return leaf.Sub("leaf", "[x]")
		},
	}

	answer, err := root.Sub("decompose", "[evidence]")
	require.NoError(t, err)
	assert.Contains(t, answer, "sub-investigation skipped",
		"the root spends the only sub-call, so the leaf finds the shared budget spent")
	leafSub.AssertNotCalled(t, "Answer")
}
