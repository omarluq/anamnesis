package rlm

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCapForHistoryLeavesSmallTextUnchanged(t *testing.T) {
	t.Parallel()

	text := "finding: checkout-api OOM-killed at 09:01"

	assert.Equal(t, text, capForHistory(text), "text within the cap re-enters history verbatim")
}

func TestCapForHistoryBoundsOversizedText(t *testing.T) {
	t.Parallel()

	// A turn ending on a bare []Entry expression renders a huge value; the cap must
	// bound what re-enters the controller transcript so the context cannot grow
	// without limit — the structural RLM property the prompt should not have to
	// enforce on its own.
	oversized := strings.Repeat("a", maxHistoryFieldBytes*3)

	capped := capForHistory(oversized)

	require.Less(t, len(capped), len(oversized), "an oversized value is truncated")
	assert.LessOrEqual(t, len(capped), maxHistoryFieldBytes+64, "the kept prefix stays near the byte cap")
	assert.True(t, strings.HasPrefix(oversized, capped[:maxHistoryFieldBytes]),
		"the retained text is the original head, not a re-summary")
	assert.Contains(t, capped, "bytes elided to bound controller context",
		"the elision marker records how much was dropped")
}

func TestCapForHistoryCutsOnRuneBoundary(t *testing.T) {
	t.Parallel()

	// Multi-byte runes straddling the byte cap must not be split, so the truncated
	// value stays valid UTF-8 rather than ending in a mangled half-rune.
	oversized := strings.Repeat("é", maxHistoryFieldBytes) // two bytes each, so over the cap

	capped := capForHistory(oversized)

	assert.True(t, utf8.ValidString(capped), "the truncated text remains valid UTF-8")
	assert.Contains(t, capped, "bytes elided", "the elision marker is appended")
}
