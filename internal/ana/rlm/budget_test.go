package rlm_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/samber/oops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/rlm"
)

func TestBudgetLimitSentinels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		limit   func(budget *rlm.Budget) int
		consume func(budget *rlm.Budget) error
		name    string
		code    string
	}{
		{
			name:    "depth",
			code:    "budget_depth_exceeded",
			limit:   func(budget *rlm.Budget) int { return budget.MaxDepth },
			consume: func(budget *rlm.Budget) error { return budget.EnterDepth() },
		},
		{
			name:    "sub-calls",
			code:    "budget_sub_calls_exceeded",
			limit:   func(budget *rlm.Budget) int { return budget.MaxSubCalls },
			consume: func(budget *rlm.Budget) error { return budget.ReserveSubCall() },
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			budget := rlm.NewBudget()
			limit := testCase.limit(budget)

			for range limit {
				require.NoError(t, testCase.consume(budget))
			}

			err := testCase.consume(budget)
			require.Error(t, err)

			var oopsErr oops.OopsError

			require.ErrorAs(t, err, &oopsErr)
			assert.Equal(t, testCase.code, oopsErr.Code())
		})
	}
}

func TestBudgetDepthGaugeRecovers(t *testing.T) {
	t.Parallel()

	budget := rlm.NewBudget()

	for range budget.MaxDepth {
		require.NoError(t, budget.EnterDepth())
	}

	err := budget.EnterDepth()
	require.Error(t, err)

	var oopsErr oops.OopsError

	require.ErrorAs(t, err, &oopsErr)
	assert.Equal(t, "budget_depth_exceeded", oopsErr.Code())

	budget.ExitDepth()
	assert.NoError(t, budget.EnterDepth())
}

func TestBudgetReserveSubCallConcurrent(t *testing.T) {
	t.Parallel()

	budget := rlm.NewBudget()

	const goroutines = 256

	var (
		waitGroup sync.WaitGroup
		accepted  atomic.Int64
	)

	waitGroup.Add(goroutines)

	for range goroutines {
		go func() {
			defer waitGroup.Done()

			if budget.ReserveSubCall() == nil {
				accepted.Add(1)
			}
		}()
	}

	waitGroup.Wait()

	assert.LessOrEqual(t, accepted.Load(), int64(budget.MaxSubCalls))
	assert.Equal(t, int64(budget.MaxSubCalls), accepted.Load())
}
