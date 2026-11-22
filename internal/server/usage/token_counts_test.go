package usage

import (
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
		{"timings cannot replace known total with an impossible cache count", "/completion",
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
