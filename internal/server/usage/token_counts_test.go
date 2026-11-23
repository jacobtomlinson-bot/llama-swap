package usage

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUsage_ReportedTokenCounts(t *testing.T) {
	for _, tc := range []struct {
		title, path, body     string
		input, output, cached int
	}{
		{"Anthropic cache writes are input not hits", "/v1/messages",
			`{"usage":{"input_tokens":0,"output_tokens":6,"cache_creation_input_tokens":7,"cache_read_input_tokens":3}}`, 10, 6, 3},
		{"Anthropic cache-only usage does not invent input total", "/v1/messages",
			`{"usage":{"cache_read_input_tokens":3}}`, 0, 0, 3},
		{"do not join processed timings with API cache", "/completion",
			`{"timings":{"prompt_n":2},"usage":{"prompt_tokens_details":{"cached_tokens":8}}}`, 0, 0, 8},
		{"do not join processed timings with explicit zero API cache", "/completion",
			`{"timings":{"prompt_n":2},"usage":{"prompt_tokens_details":{"cached_tokens":0}}}`, 0, 0, 0},
		{"do not join processed timings with TensorFold cache", "/completion",
			`{"timings":{"prompt_n":2},"tensorfold":{"cached":9}}`, 0, 0, 9},
		{"keep valid explicit totals when cache exceeds input", "/completion",
			`{"usage":{"prompt_tokens":2,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":9}}}`, 2, 3, -1},
		{"explicit zero total is not missing", "/completion",
			`{"usage":{"prompt_tokens":0,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":9}},"timings":{"prompt_n":2}}`, 0, 3, -1},
		{"processed-only fallback without cache", "/infill",
			`{"timings":{"prompt_n":2,"predicted_n":3}}`, 2, 3, -1},
		{"zero matched timing cache is valid", "/infill",
			`{"timings":{"prompt_n":2,"cache_n":0,"predicted_n":3}}`, 2, 3, 0},
		{"timings cannot replace a same-frame API total with an impossible cache count", "/completion",
			`{"usage":{"prompt_tokens":2,"completion_tokens":3},"timings":{"prompt_n":2,"cache_n":9}}`, 2, 3, -1},
		{"llama timings include cached tokens", "/completion",
			`{"timings":{"prompt_n":10,"cache_n":2,"predicted_n":5}}`, 12, 5, 2},
		{"llama timings reject invalid components", "/completion",
			`{"usage":{"prompt_tokens":12,"completion_tokens":5},"timings":{"prompt_n":10,"cache_n":-1,"predicted_n":-1}}`, 12, 5, -1},
		{"llama timings do not add hits to a negative prompt count", "/completion",
			`{"timings":{"prompt_n":-1,"cache_n":9,"predicted_n":3}}`, 0, 3, 9},
		{"invalid count types and overflow are not observations", "/v1/responses",
			`{"usage":{"input_tokens":18446744073709551619,"output_tokens":2.5,"input_tokens_details":{"cached_tokens":"5"}}}`, 0, 0, -1},
	} {
		t.Run(tc.title, func(t *testing.T) {
			got, err := Extract(hint.Unknown, tc.path, "application/json", []byte(tc.body), time.Now())
			require.NoError(t, err)
			assert.True(t, got.Found)
			assert.Equal(t, tc.input, got.Tokens.InputTokens)
			assert.Equal(t, tc.output, got.Tokens.OutputTokens)
			assert.Equal(t, tc.cached, got.Tokens.CachedTokens)
		})
	}
}

func TestUsage_CacheAcrossPartialTimingFrames(t *testing.T) {
	const terminal = `{"timings":{"prompt_n":2,"predicted_n":3,"prompt_per_second":4,"predicted_per_second":5}}`
	for _, upstream := range []hint.Upstream{hint.Unknown, hint.LlamaServer, hint.Halogen, hint.Radiance, hint.Kyojin} {
		t.Run(string(upstream), func(t *testing.T) {
			u := parseUsageStream(t, upstream, "/v1/chat/completions", `{"timings":{"cache_n":9}}`, terminal)
			assert.True(t, u.Found)
			assert.Equal(t, 0, u.Tokens.InputTokens)
			assert.Equal(t, 9, u.Tokens.CachedTokens)
			assert.Equal(t, 3, u.Tokens.OutputTokens)
			assert.Equal(t, 4.0, u.Tokens.PromptPerSecond)
			assert.Equal(t, 5.0, u.Tokens.TokensPerSecond)
		})
	}
	u := parseUsageStream(t, hint.Unknown, "/v1/chat/completions",
		`{"tensorfold":{"cached":9}}`, `{"tensorfold":{"tokens_per_second":7}}`, terminal)
	assert.Equal(t, 0, u.Tokens.InputTokens)
	assert.Equal(t, 9, u.Tokens.CachedTokens)
}

func TestUsage_CompleteCountsAcrossPartialTimingFrames(t *testing.T) {
	const (
		complete = `{"timings":{"prompt_n":11,"cache_n":4,"predicted_n":3,"prompt_per_second":150,"predicted_per_second":30}}`
		rates    = `{"timings":{"prompt_per_second":250,"predicted_per_second":55}}`
	)
	for _, upstream := range []hint.Upstream{hint.Unknown, hint.LlamaServer, hint.Halogen, hint.Radiance, hint.Kyojin} {
		t.Run(string(upstream), func(t *testing.T) {
			u := parseUsageStream(t, upstream, "/v1/chat/completions",
				complete, `{"timings":{"prompt_n":70,"predicted_n":5}}`,
				`{"timings":{"prompt_n":-1,"cache_n":-1,"predicted_n":-1}}`, rates)
			assert.Equal(t, 15, u.Tokens.InputTokens)
			assert.Equal(t, 5, u.Tokens.OutputTokens)
			assert.Equal(t, 4, u.Tokens.CachedTokens)
			assert.Equal(t, 250.0, u.Tokens.PromptPerSecond)
			assert.Equal(t, 55.0, u.Tokens.TokensPerSecond)

			u = parseUsageStream(t, upstream, "/v1/chat/completions",
				complete, `{"timings":{"prompt_n":0,"cache_n":0,"predicted_n":0}}`, rates)
			assert.Equal(t, 0, u.Tokens.InputTokens)
			assert.Equal(t, 0, u.Tokens.OutputTokens)
			assert.Equal(t, 0, u.Tokens.CachedTokens)

			u = parseUsageStream(t, upstream, "/v1/chat/completions",
				complete, `{"timings":{"cache_n":16}}`, rates)
			assert.Equal(t, 15, u.Tokens.InputTokens)
			assert.Equal(t, 3, u.Tokens.OutputTokens)
			assert.Equal(t, 4, u.Tokens.CachedTokens)

			u = parseUsageStream(t, upstream, "/v1/chat/completions",
				`{"usage":{"prompt_tokens":2}}`, complete, rates)
			assert.Equal(t, 15, u.Tokens.InputTokens)
			assert.Equal(t, 3, u.Tokens.OutputTokens)
			assert.Equal(t, 4, u.Tokens.CachedTokens)
		})
	}
}

func TestUsage_TimingOutputPrecedence(t *testing.T) {
	const rates = `{"timings":{"predicted_per_second":56}}`
	for _, upstream := range []hint.Upstream{hint.Unknown, hint.LlamaServer, hint.Halogen, hint.Radiance, hint.Kyojin} {
		t.Run(string(upstream), func(t *testing.T) {
			for _, tc := range []struct {
				name, first, second string
				want                int
			}{
				{"later API", `{"timings":{"predicted_n":3}}`, `{"usage":{"completion_tokens":5}}`, 5},
				{"later timings", `{"usage":{"completion_tokens":3}}`, `{"timings":{"predicted_n":5}}`, 5},
				{"same-frame timings", `{}`, `{"usage":{"completion_tokens":3},"timings":{"predicted_n":5}}`, 5},
				{"ignore invalid same-frame timings", `{}`, `{"usage":{"completion_tokens":5},"timings":{"predicted_n":-1}}`, 5},
				{"ignore API zero placeholder", `{"timings":{"predicted_n":5}}`, `{"usage":{"completion_tokens":0}}`, 5},
			} {
				t.Run(tc.name, func(t *testing.T) {
					u := parseUsageStream(t, upstream, "/v1/chat/completions", tc.first, tc.second, rates)
					assert.Equal(t, tc.want, u.Tokens.OutputTokens)
					assert.Equal(t, 56.0, u.Tokens.TokensPerSecond)
				})
			}
		})
	}
}

func TestUsage_ProcessedTimingFallbackAcrossFrames(t *testing.T) {
	const processed = `{"timings":{"prompt_n":11,"predicted_n":3}}`
	const rates = `{"timings":{"prompt_per_second":270}}`
	for _, tc := range []struct {
		name          string
		frames        []string
		input, cached int
	}{
		{"omitted counts", []string{processed, rates}, 11, -1},
		{"earlier timing cache", []string{`{"timings":{"cache_n":4}}`, processed, rates}, 0, 4},
		{"later timing cache", []string{processed, `{"timings":{"cache_n":4}}`, rates}, 0, 4},
		{"later explicit API cache zero", []string{processed, `{"usage":{"prompt_tokens_details":{"cached_tokens":0}}}`, rates}, 0, 0},
		{"earlier explicit API prompt zero", []string{`{"usage":{"prompt_tokens":0}}`, processed, rates}, 0, -1},
		{"later full API input", []string{processed, `{"usage":{"prompt_tokens":7}}`, rates}, 7, -1},
		{"earlier TensorFold cache", []string{`{"tensorfold":{"cached":4}}`, processed, rates}, 0, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := parseUsageStream(t, hint.Unknown, "/infill", tc.frames...)
			assert.Equal(t, tc.input, u.Tokens.InputTokens)
			assert.Equal(t, tc.cached, u.Tokens.CachedTokens)
			assert.Equal(t, 3, u.Tokens.OutputTokens)
			assert.Equal(t, 270.0, u.Tokens.PromptPerSecond)
		})
	}
}

func TestUsage_RejectsContradictoryCacheBeforeMerging(t *testing.T) {
	u := parseUsageStream(t, hint.OpenAI, "/v1/chat/completions",
		`{"usage":{"prompt_tokens":10,"prompt_tokens_details":{"cached_tokens":2}}}`,
		`{"usage":{"prompt_tokens":0,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":9}}}`)
	assert.Equal(t, 10, u.Tokens.InputTokens)
	assert.Equal(t, 5, u.Tokens.OutputTokens)
	assert.Equal(t, 2, u.Tokens.CachedTokens)

	u = parseUsageStream(t, hint.OpenAI, "/v1/responses",
		`{"response":{"usage":{"input_tokens":10}}}`,
		`{"response":{"usage":{"input_tokens":0,"output_tokens":5,"input_tokens_details":{"cached_tokens":9}}}}`)
	assert.Equal(t, 10, u.Tokens.InputTokens)
	assert.Equal(t, 5, u.Tokens.OutputTokens)
	assert.Equal(t, -1, u.Tokens.CachedTokens)
}

func TestUsage_TokenObservationOrder(t *testing.T) {
	const (
		api     = `{"usage":{"prompt_tokens":20,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":6}}}`
		timing  = `{"timings":{"prompt_n":11,"cache_n":4,"predicted_n":3}}`
		same    = `{"usage":{"prompt_tokens":20,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":6}},"timings":{"prompt_n":11,"cache_n":4,"predicted_n":3}}`
		rates   = `{"timings":{"prompt_per_second":250,"predicted_per_second":55}}`
		apiZero = `{"usage":{"prompt_tokens":0,"completion_tokens":0}}`
	)
	for _, upstream := range []hint.Upstream{hint.Unknown, hint.LlamaServer, hint.Halogen, hint.Radiance, hint.Kyojin} {
		t.Run(string(upstream), func(t *testing.T) {
			for _, tc := range []struct {
				name                string
				frames              []string
				input, output, hits int
			}{
				{"API after timing", []string{timing, api}, 20, 5, 6},
				{"timing after API", []string{api, timing}, 15, 3, 4},
				{"pair after smaller API total", []string{`{"usage":{"prompt_tokens":2,"prompt_tokens_details":{"cached_tokens":1}}}`, timing}, 15, 3, 4},
				{"pair after earlier API zero", []string{apiZero, timing}, 15, 3, 4},
				{"pair with same-frame API zero", []string{api, `{"usage":{"prompt_tokens":0},"timings":{"prompt_n":11,"cache_n":4,"predicted_n":3}}`}, 20, 3, 6},
				{"overflowing pair does not update the cache", []string{api, fmt.Sprintf(`{"timings":{"prompt_n":%d,"cache_n":1,"predicted_n":3}}`, math.MaxInt)}, 20, 3, 6},
				{"same-frame backend precedence", []string{same}, 15, 3, 4},
				{"cache before prompt, compatible", []string{`{"timings":{"cache_n":4}}`, `{"usage":{"prompt_tokens":7}}`}, 7, 0, 4},
				{"cache before prompt, incompatible", []string{`{"timings":{"cache_n":8}}`, `{"usage":{"prompt_tokens":7}}`}, 7, 0, -1},
				{"API cache only updates within bound", []string{timing, `{"usage":{"prompt_tokens_details":{"cached_tokens":7}}}`}, 15, 3, 7},
				{"timing cache only updates within bound", []string{api, `{"timings":{"cache_n":7}}`}, 20, 5, 7},
				{"known zero bounds later cache", []string{apiZero, `{"timings":{"cache_n":1}}`}, 0, 0, -1},
				{"API zero placeholder", []string{timing, apiZero}, 15, 3, 4},
				{"zero timing pair", []string{timing, `{"timings":{"prompt_n":0,"cache_n":0,"predicted_n":0}}`}, 0, 0, 0},
			} {
				t.Run(tc.name, func(t *testing.T) {
					u := parseUsageStream(t, upstream, "/v1/chat/completions", append(tc.frames, rates)...)
					assert.Equal(t, tc.input, u.Tokens.InputTokens)
					assert.Equal(t, tc.output, u.Tokens.OutputTokens)
					assert.Equal(t, tc.hits, u.Tokens.CachedTokens)
					assert.Equal(t, 250.0, u.Tokens.PromptPerSecond)
					assert.Equal(t, 55.0, u.Tokens.TokensPerSecond)
				})
			}
		})
	}
}

func TestUsage_ProviderPromptComponentsStaySeparate(t *testing.T) {
	u := parseUsageStream(t, hint.Unknown, "/v1/messages",
		`{"message":{"usage":{"input_tokens":3,"cache_read_input_tokens":2,"cache_creation_input_tokens":1}}}`,
		`{"timings":{"prompt_n":8,"cache_n":2,"predicted_n":3}}`,
		`{"usage":{"output_tokens":5}}`)
	assert.Equal(t, 10, u.Tokens.InputTokens)
	assert.Equal(t, 5, u.Tokens.OutputTokens)
	assert.Equal(t, 2, u.Tokens.CachedTokens)

	for _, upstream := range []hint.Upstream{hint.Unknown, hint.TensorFold} {
		u := parseUsageStream(t, upstream, "/v1/chat/completions",
			`{"usage":{"prompt_tokens":10,"prompt_tokens_details":{"cached_tokens":2}}}`,
			`{"tensorfold":{"cached":11}}`, `{"tensorfold":{"tokens_per_second":7}}`)
		assert.Equal(t, 10, u.Tokens.InputTokens)
		assert.Equal(t, 2, u.Tokens.CachedTokens)
		assert.Equal(t, 7.0, u.Tokens.TokensPerSecond)
	}
}
