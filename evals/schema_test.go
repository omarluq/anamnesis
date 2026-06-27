package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func repeatWords(count int) string {
	words := make([]string, count)
	for i := range words {
		words[i] = "lorem"
	}

	return strings.Join(words, " ")
}

func TestTier2Keywords(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		answer      string
		expected    []string
		forbidden   []string
		wantMissing []string
		wantPresent []string
		wantPass    bool
	}{
		{
			name:        "all-expected-present-none-forbidden",
			answer:      "The OOM killer reclaimed memory from checkout-api.",
			expected:    []string{keywordOOM, "memory"},
			forbidden:   []string{keywordUnknownError},
			wantMissing: []string{},
			wantPresent: []string{},
			wantPass:    true,
		},
		{
			name:        "missing-expected-keyword-fails",
			answer:      "The OOM killer reclaimed memory from checkout-api.",
			expected:    []string{keywordOOM, "disk"},
			forbidden:   []string{},
			wantMissing: []string{"disk"},
			wantPresent: []string{},
			wantPass:    false,
		},
		{
			name:        "forbidden-keyword-present-fails",
			answer:      "An unknown error preceded the OOM event.",
			expected:    []string{keywordOOM},
			forbidden:   []string{keywordUnknownError},
			wantMissing: []string{},
			wantPresent: []string{keywordUnknownError},
			wantPass:    false,
		},
		{
			name:        "keyword-match-is-case-insensitive",
			answer:      "the oom killer fired on checkout-api at 09:00",
			expected:    []string{keywordOOM, "Checkout-API"},
			forbidden:   []string{},
			wantMissing: []string{},
			wantPresent: []string{},
			wantPass:    true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := Tier2(testCase.answer, testCase.expected, testCase.forbidden)
			assert.Equal(t, testCase.wantPass, got.Pass)
			assert.Equal(t, testCase.wantMissing, got.MissingKeywords)
			assert.Equal(t, testCase.wantPresent, got.ForbiddenPresent)
		})
	}
}

func TestTier2LengthBoundary(t *testing.T) {
	t.Parallel()

	atLimit := Tier2(repeatWords(MaxAnswerWords), nil, nil)
	assert.Equal(t, MaxAnswerWords, atLimit.WordCount)
	assert.True(t, atLimit.Pass)

	overLimit := Tier2(repeatWords(MaxAnswerWords+1), nil, nil)
	assert.Equal(t, MaxAnswerWords+1, overLimit.WordCount)
	assert.False(t, overLimit.Pass)
}

func TestTier2LengthGateIsIndependentOfKeywords(t *testing.T) {
	t.Parallel()

	answer := repeatWords(MaxAnswerWords+1) + " OOM"
	got := Tier2(answer, []string{"OOM"}, nil)

	assert.Empty(t, got.MissingKeywords)
	assert.Empty(t, got.ForbiddenPresent)
	assert.Equal(t, MaxAnswerWords+2, got.WordCount)
	assert.False(t, got.Pass)
}
