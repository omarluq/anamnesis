package terminal

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/omarluq/anamnesis/internal/transcript"
)

// TestPriorConversationPairsQuestionsWithAnswers proves the preamble pairs each user
// question with the assistant answer that follows it and excludes the in-flight
// question that has no answer yet, so a follow-up run sees only completed exchanges.
func TestPriorConversationPairsQuestionsWithAnswers(t *testing.T) {
	t.Parallel()

	history := []chatMessage{
		newChatMessage(transcript.RoleUser, "what oom'd?"),
		newChatMessage(transcript.RoleThinking, "looking"),
		newChatMessage(transcript.RoleAssistant, "nginx was oom-killed at 09:02"),
		newChatMessage(transcript.RoleUser, "what about ssh?"),
	}

	got := priorConversation(history)

	assert.Contains(t, got, "Earlier in this session:")
	assert.Contains(t, got, "Q: what oom'd?")
	assert.Contains(t, got, "A: nginx was oom-killed at 09:02")
	assert.NotContains(t, got, "what about ssh?",
		"the in-flight question has no answer yet, so it is excluded")
}

// TestPriorConversationEmptyWithoutCompletedExchange proves the first question of a
// session — or a lone unanswered one — yields no preamble, so the run starts cold.
func TestPriorConversationEmptyWithoutCompletedExchange(t *testing.T) {
	t.Parallel()

	assert.Empty(t, priorConversation(nil), "no history yields no preamble")
	assert.Empty(t,
		priorConversation([]chatMessage{newChatMessage(transcript.RoleUser, "first")}),
		"a lone unanswered question yields no preamble")
}

// TestPriorConversationCapsToRecentExchanges proves only the last maxPriorExchanges
// pairs cross forward, so a long session can never grow the preamble without bound.
func TestPriorConversationCapsToRecentExchanges(t *testing.T) {
	t.Parallel()

	history := make([]chatMessage, 0, 2*(maxPriorExchanges+3))

	for index := range maxPriorExchanges + 3 {
		label := strconv.Itoa(index)
		history = append(history,
			newChatMessage(transcript.RoleUser, "q"+label),
			newChatMessage(transcript.RoleAssistant, "a"+label),
		)
	}

	got := priorConversation(history)

	assert.Equal(t, maxPriorExchanges, strings.Count(got, "Q: "),
		"only the last maxPriorExchanges pairs are carried")
	assert.NotContains(t, got, "q0", "the oldest exchanges beyond the cap are dropped")
	assert.Contains(t, got, "q"+strconv.Itoa(maxPriorExchanges+2),
		"the most recent exchange is kept")
}

// TestPriorConversationTruncatesLongAnswers proves an over-long prior answer is
// truncated so the full raw answer never crosses into the next run's context.
func TestPriorConversationTruncatesLongAnswers(t *testing.T) {
	t.Parallel()

	longAnswer := strings.Repeat("x", maxPriorAnswerRunes+100)
	history := []chatMessage{
		newChatMessage(transcript.RoleUser, "q"),
		newChatMessage(transcript.RoleAssistant, longAnswer),
	}

	got := priorConversation(history)

	assert.Contains(t, got, "…", "an over-long answer is truncated with an ellipsis")
	assert.NotContains(t, got, longAnswer, "the full over-long answer never crosses forward")
}

// TestTruncateRunes proves truncation leaves short text alone and cuts long text on a
// rune boundary so a multi-byte character is never split mid-encoding.
func TestTruncateRunes(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "abc", truncateRunes("abc", 5), "text within the limit is unchanged")
	assert.Equal(t, "ab…", truncateRunes("abcd", 2),
		"text over the limit is cut with an ellipsis")
	assert.Equal(t, "héll…", truncateRunes("héllo wörld", 4),
		"truncation cuts on a rune boundary, never mid-character")
}
