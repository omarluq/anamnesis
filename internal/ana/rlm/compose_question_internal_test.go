package rlm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestComposeQuestion proves the prior-conversation preamble is folded ahead of the
// user's question, and that an empty preamble leaves the bare question untouched so
// the first question of a session stays cold.
func TestComposeQuestion(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "what crashed?", composeQuestion("", "what crashed?"),
		"no prior context returns the bare question")
	assert.Equal(t,
		"Earlier in this session:\n\nQ: a\nA: b\n\nfollow up",
		composeQuestion("Earlier in this session:\n\nQ: a\nA: b", "follow up"),
		"a non-empty preamble is folded ahead of the question")
}
