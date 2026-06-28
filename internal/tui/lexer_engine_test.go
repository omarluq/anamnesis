package tui_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/tui"
)

func TestLexerEngineCachesAnalysisResult(t *testing.T) {
	t.Parallel()

	engine := tui.NewLexerEngine()

	// Go code without a language tag — should detect the Go lexer.
	text := "func main() {\n\tfmt.Println(\"hello\")\n}"

	// First call runs the full analysis (cache miss).
	iter1, found := engine.IteratorFor(text)
	require.True(t, found, "expected lexer detection to succeed on first call")

	tokens1 := iter1.Tokens()

	// Second call should return the cached lexer (cache hit).
	iter2, found := engine.IteratorFor(text)
	require.True(t, found, "expected lexer detection to succeed on second call")

	tokens2 := iter2.Tokens()

	require.Equal(t, tokens1, tokens2, "the cached lexer must yield an identical token stream")
}

func TestLexerEngineReturnsFalseForNoMatch(t *testing.T) {
	t.Parallel()

	engine := tui.NewLexerEngine()

	// Plain text with no distinguishing features.
	_, found := engine.IteratorFor("just some plain words")
	require.False(t, found, "expected no lexer match for plain text")
}

func TestLexerEngineDetectsGoCode(t *testing.T) {
	t.Parallel()

	engine := tui.NewLexerEngine()
	text := "package main\n\nfunc main() {}"

	iter, found := engine.IteratorFor(text)
	require.True(t, found, "expected engine to detect a lexer for Go code")

	tokens := iter.Tokens()
	require.NotEmpty(t, tokens, "expected non-empty token stream")

	values := make([]string, 0, len(tokens))
	for _, token := range tokens {
		values = append(values, token.Value)
	}

	assert.Contains(t, values, "package", "expected the 'package' keyword in the token stream")
}
