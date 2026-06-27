package openai_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/omarluq/anamnesis/internal/openai"
)

func TestCostMicros(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		model     string
		tokensIn  int
		tokensOut int
		want      int64
	}{
		{
			name:      "the flagship model prices both token streams",
			model:     openai.Model,
			tokensIn:  1000,
			tokensOut: 1000,
			want:      35000,
		},
		{
			name:      "unknown model is free",
			model:     "gpt-4o",
			tokensIn:  5000,
			tokensOut: 5000,
			want:      0,
		},
		{
			name:      "empty model is free",
			model:     "",
			tokensIn:  10,
			tokensOut: 10,
			want:      0,
		},
		{
			name:      "zero tokens cost nothing",
			model:     openai.Model,
			tokensIn:  0,
			tokensOut: 0,
			want:      0,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := openai.CostMicros(testCase.model, testCase.tokensIn, testCase.tokensOut)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestUsageAddAccumulatesBothFields(t *testing.T) {
	t.Parallel()

	total := openai.Usage{TokensIn: 10, TokensOut: 4}.
		Add(openai.Usage{TokensIn: 20, TokensOut: 8})

	assert.Equal(t, openai.Usage{TokensIn: 30, TokensOut: 12}, total)
}

func TestUsageAddIsAdditiveAcrossManyDeltas(t *testing.T) {
	t.Parallel()

	deltas := []openai.Usage{
		{TokensIn: 1, TokensOut: 2},
		{TokensIn: 3, TokensOut: 4},
		{TokensIn: 5, TokensOut: 6},
	}

	total := openai.Usage{TokensIn: 0, TokensOut: 0}
	for _, delta := range deltas {
		total = total.Add(delta)
	}

	assert.Equal(t, openai.Usage{TokensIn: 9, TokensOut: 12}, total)
}
