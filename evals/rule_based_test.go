package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	toolBoots  = "journal.Boots"
	toolQuery  = "journal.Query"
	toolCounts = "journal.Counts"
)

func TestTier1(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		expectedTools []string
		wantMissing   []string
		run           RunOutput
		minDepth      int
		wantDepthMet  bool
		wantPass      bool
	}{
		{
			name: "all-tools-depth-and-final-pass",
			run: RunOutput{
				ToolsCalled:    []string{toolBoots, toolQuery, toolCounts},
				Answer:         "checkout-api hit an OOM event at 09:00.",
				RecursionDepth: 2,
				FinalCalled:    true,
			},
			expectedTools: []string{toolBoots, toolQuery},
			wantMissing:   []string{},
			minDepth:      2,
			wantDepthMet:  true,
			wantPass:      true,
		},
		{
			name: "missing-expected-tool-fails",
			run: RunOutput{
				ToolsCalled:    []string{toolBoots},
				Answer:         "checkout-api looks healthy.",
				RecursionDepth: 2,
				FinalCalled:    true,
			},
			expectedTools: []string{toolBoots, toolQuery},
			wantMissing:   []string{toolQuery},
			minDepth:      1,
			wantDepthMet:  true,
			wantPass:      false,
		},
		{
			name: "depth-below-min-fails",
			run: RunOutput{
				ToolsCalled:    []string{toolBoots, toolQuery},
				Answer:         "shallow look at the journal.",
				RecursionDepth: 1,
				FinalCalled:    true,
			},
			expectedTools: []string{toolBoots, toolQuery},
			wantMissing:   []string{},
			minDepth:      2,
			wantDepthMet:  false,
			wantPass:      false,
		},
		{
			name: "final-absent-fails",
			run: RunOutput{
				ToolsCalled:    []string{toolBoots, toolQuery},
				Answer:         "",
				RecursionDepth: 2,
				FinalCalled:    false,
			},
			expectedTools: []string{toolBoots, toolQuery},
			wantMissing:   []string{},
			minDepth:      2,
			wantDepthMet:  true,
			wantPass:      false,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := Tier1(testCase.run, testCase.expectedTools, testCase.minDepth)
			assert.Equal(t, testCase.wantPass, got.Pass)
			assert.Equal(t, testCase.wantMissing, got.MissingTools)
			assert.Equal(t, testCase.wantDepthMet, got.DepthMet)
			assert.Equal(t, testCase.run.FinalCalled, got.FinalCalled)
		})
	}
}

func TestTier1DepthBoundaryIsInclusive(t *testing.T) {
	t.Parallel()

	run := RunOutput{
		ToolsCalled:    []string{toolBoots},
		Answer:         "boundary case.",
		RecursionDepth: 3,
		FinalCalled:    true,
	}

	got := Tier1(run, []string{toolBoots}, 3)
	assert.True(t, got.DepthMet)
	assert.True(t, got.Pass)
}

func TestTier1ExtraToolsDoNotFail(t *testing.T) {
	t.Parallel()

	run := RunOutput{
		ToolsCalled:    []string{toolBoots, toolQuery, toolCounts},
		Answer:         "extra tools are harmless.",
		RecursionDepth: 0,
		FinalCalled:    true,
	}

	got := Tier1(run, []string{toolBoots}, 0)
	assert.Empty(t, got.MissingTools)
	assert.True(t, got.Pass)
}

func TestTier1ReportsEveryMissingTool(t *testing.T) {
	t.Parallel()

	run := RunOutput{
		ToolsCalled:    []string{toolBoots},
		Answer:         "only boots ran.",
		RecursionDepth: 1,
		FinalCalled:    true,
	}

	got := Tier1(run, []string{toolBoots, toolQuery, toolCounts}, 1)
	assert.Equal(t, []string{toolQuery, toolCounts}, got.MissingTools)
	assert.False(t, got.Pass)
}

func TestTier1ReportsDepthReachedAndMinDepth(t *testing.T) {
	t.Parallel()

	run := RunOutput{
		ToolsCalled:    []string{toolBoots},
		Answer:         "depth reporting.",
		RecursionDepth: 4,
		FinalCalled:    true,
	}

	// DepthReached echoes the run's recursion depth and MinDepth echoes the required
	// floor; the two are distinct (4 vs 2) so a swap of the two assignments fails.
	got := Tier1(run, []string{toolBoots}, 2)
	assert.Equal(t, 4, got.DepthReached)
	assert.Equal(t, 2, got.MinDepth)
}
