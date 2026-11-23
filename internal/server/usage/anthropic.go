package usage

import "github.com/tidwall/gjson"

type anthropicPrompt struct {
	input, cached, cacheWrite int
	hasInput                  bool
}

// readAnthropicUsage reads Anthropic Messages usage: input_tokens,
// output_tokens, cache_read_input_tokens and cache_creation_input_tokens.
// Anthropic's input_tokens leaves out both cache counts, so they are added
// back to make the input the whole prompt. A stream reports them in two
// places: message.usage in message_start (input and cache counts) and usage
// in message_delta (the final output count). A non-streaming response has a
// single top-level usage. Thinking tokens are part of output_tokens and are
// not reported separately, so reasoning stays unknown.
func readAnthropicUsage(ev gjson.Result, u *apiUsage) int {
	frameTotal := -1
	raw := &u.anthropic
	for _, path := range []string{"message.usage", "usage"} {
		b := ev.Get(path)
		if !b.IsObject() {
			continue
		}
		hasInput := setCount(&raw.input, b.Get("input_tokens"))
		raw.hasInput = raw.hasInput || hasInput
		hasCache := setReported(&raw.cached, b.Get("cache_read_input_tokens"))
		hasWrite := setCount(&raw.cacheWrite, b.Get("cache_creation_input_tokens"))
		promptReported := hasInput || hasCache || hasWrite
		if promptReported && raw.hasInput {
			if total := addCounts(raw.input, raw.cached, raw.cacheWrite); total >= 0 {
				frameTotal = total
				u.recordFullPrompt(total)
			}
		}
		if hasCache {
			u.recordCache(raw.cached, frameTotal)
		}
		found := setCount(&u.output, b.Get("output_tokens")) || promptReported
		if found {
			u.found = true
		}
	}
	return frameTotal
}
