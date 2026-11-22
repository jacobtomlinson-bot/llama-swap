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
