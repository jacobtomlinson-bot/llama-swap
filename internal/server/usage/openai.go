package usage

import "github.com/tidwall/gjson"

// readOpenAIUsage reads the OpenAI usage block:
//
//   - chat/completions, completions, embeddings: usage.prompt_tokens,
//     completion_tokens, prompt_tokens_details.cached_tokens and
//     completion_tokens_details.reasoning_tokens
//   - Responses API: usage (non-streaming) or response.usage (the
//     response.completed event) with input_tokens, output_tokens,
//     input_tokens_details.cached_tokens and
//     output_tokens_details.reasoning_tokens
func readOpenAIUsage(ev gjson.Result, u *apiUsage) int {
	frameTotal := -1
	for _, path := range []string{"usage", "response.usage"} {
		b := ev.Get(path)
		if !b.IsObject() {
			continue
		}
		input := b.Get("prompt_tokens")
		if !input.Exists() {
			input = b.Get("input_tokens")
		}
		// Validate the cache against the total in this block, not an
		// earlier retained prompt that might mask a contradiction.
		frameInput, hasFrameInput := reportedCount(input)
		found := hasFrameInput
		if hasFrameInput {
			// API streams may repeat a zero placeholder after a count.
			frameTotal = frameInput
			if frameInput > 0 || u.prompt != promptFull {
				u.recordFullPrompt(frameInput)
			}
		}
		if v := b.Get("completion_tokens"); v.Exists() {
			found = setCount(&u.output, v) || found
		} else {
			found = setCount(&u.output, b.Get("output_tokens")) || found
		}
		cache, hasCache := reportedCount(b.Get("prompt_tokens_details.cached_tokens"))
		if !hasCache {
			cache, hasCache = reportedCount(b.Get("input_tokens_details.cached_tokens"))
		}
		if hasCache {
			u.recordCache(cache, frameTotal)
			found = true
		}
		if !setReported(&u.reasoning, b.Get("completion_tokens_details.reasoning_tokens")) {
			setReported(&u.reasoning, b.Get("output_tokens_details.reasoning_tokens"))
		}
		if found {
			u.found = true
		}
	}
	return frameTotal
}
