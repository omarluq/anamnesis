package tui

import (
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/samber/hot"
)

const lexerCacheCapacity = 64

// LexerEngine caches chroma lexer auto-detection results so that repeated
// renders of the same untagged code block skip the expensive O(N) scan of
// all 278+ registered lexers.
type LexerEngine struct {
	cache *hot.HotCache[string, chroma.Lexer]
}

// NewLexerEngine creates a lexer engine backed by a W-TinyLFU cache.
// The cache has no TTL because lexer detection is deterministic for
// identical text — the same input always selects the same lexer.
func NewLexerEngine() LexerEngine {
	cache := hot.NewHotCache[string, chroma.Lexer](hot.WTinyLFU, lexerCacheCapacity).Build()

	return LexerEngine{cache: cache}
}

// IteratorFor returns a token iterator for untagged code by looking up the
// cached lexer, or running full analysis on the first encounter. Both outcomes
// are cached: a successful match stores the detected lexer, while a no-match
// stores a nil sentinel so that plain or untagged snippets short-circuit the
// O(N) registry scan on every subsequent render instead of re-running it.
func (engine *LexerEngine) IteratorFor(text string) (chroma.Iterator, bool) {
	cached, found := engine.cache.MustGet(text)
	if found {
		// A nil sentinel marks a previously analyzed no-match.
		if cached == nil {
			return nil, false
		}

		return tokenizeCode(cached, text)
	}

	highest := float32(0)

	var bestLexer chroma.Lexer

	for _, lexer := range lexers.GlobalLexerRegistry.Lexers {
		weight := lexer.AnalyseText(text)
		if weight > highest {
			highest = weight
			bestLexer = lexer
		}
	}

	// Cache the outcome unconditionally — when no lexer matches, bestLexer is
	// nil, recording the no-match so future lookups of this key short-circuit.
	engine.cache.Set(text, bestLexer)

	if highest == 0 {
		return nil, false
	}

	return tokenizeCode(bestLexer, text)
}
