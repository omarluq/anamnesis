package openai

// tokensPerPriceUnit is the token batch size the price table is denominated in:
// prices are micro-dollars per 1000 tokens.
const tokensPerPriceUnit = 1000

// modelPrice is one model's token prices, in micro-dollars per 1000 tokens for
// input and output respectively.
type modelPrice struct {
	inputMicrosPer1K  int64
	outputMicrosPer1K int64
}

// modelPrices is the per-model token price table CostMicros reads. Prices are in
// micro-dollars per 1000 tokens; a model absent here prices to zero.
var modelPrices = map[string]modelPrice{
	// gpt-5.5 list price: $5/1M input, $30/1M output (micro-dollars per 1000 tokens).
	Model: {inputMicrosPer1K: 5000, outputMicrosPer1K: 30000},
}

// Usage accumulates the input and output token counts the OpenAI API reports
// across one or more calls, so the controller loop can total a session's
// consumption and price it with CostMicros.
type Usage struct {
	// TokensIn is the number of prompt (input) tokens consumed.
	TokensIn int
	// TokensOut is the number of completion (output) tokens produced.
	TokensOut int
}

// Add returns the field-wise sum of the receiver and delta, accumulating both
// the input and output token counts.
func (usage Usage) Add(delta Usage) Usage {
	return Usage{
		TokensIn:  usage.TokensIn + delta.TokensIn,
		TokensOut: usage.TokensOut + delta.TokensOut,
	}
}

// CostMicros prices tokensIn input and tokensOut output tokens for model in
// micro-dollars (millionths of a US dollar). Models absent from the price table,
// including the empty string, cost zero so callers can total unknown models
// without special-casing them.
func CostMicros(model string, tokensIn, tokensOut int) int64 {
	price, known := modelPrices[model]
	if !known {
		return 0
	}

	inputCost := int64(tokensIn) * price.inputMicrosPer1K
	outputCost := int64(tokensOut) * price.outputMicrosPer1K

	return (inputCost + outputCost) / tokensPerPriceUnit
}
