package openai

import (
	"strings"

	"github.com/openai/openai-go/v3/responses"
	"github.com/samber/oops"
)

// ParseEffort resolves a case-insensitive reasoning-effort name to the Responses
// API enum the controller and sub-LLM calls pass under ReasoningParam. It
// is the single source of truth mapping the configured effort string onto the SDK
// value, accepting the same {none, minimal, low, medium, high, xhigh} set
// config.Validate guards. An unknown name returns the zero effort and an oops error
// tagged with the config domain — config.Validate rejects bad values at load, so a
// failure here means a value reached construction unvalidated and must fail loudly
// rather than silently send an empty effort. Surrounding whitespace is trimmed so a
// stray space in a config or env value never defeats the match.
func ParseEffort(name string) (responses.ReasoningEffort, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "none":
		return responses.ReasoningEffortNone, nil
	case "minimal":
		return responses.ReasoningEffortMinimal, nil
	case "low":
		return responses.ReasoningEffortLow, nil
	case "medium":
		return responses.ReasoningEffortMedium, nil
	case "high":
		return responses.ReasoningEffortHigh, nil
	case "xhigh":
		return responses.ReasoningEffortXhigh, nil
	default:
		return "", oops.
			In("config").
			Code("invalid_reasoning_effort").
			Errorf("unknown reasoning effort %q: want one of none, minimal, low, medium, high, xhigh", name)
	}
}
