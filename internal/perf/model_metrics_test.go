package perf

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestModelMetrics_ValidValuesAndLabels(t *testing.T) {
	var metrics ModelMetrics
	model := "first\"\\\nmodel"
	metrics.Observe(model, 0.25, 0, 42)
	metrics.Observe(model, 0, -1, math.Inf(1))
	metrics.Observe("missing", 0, math.NaN(), -1)

	var out strings.Builder
	metrics.WritePrometheus(&out)
	body := out.String()
	assert.Contains(t, body, "llamaswap_model_time_to_first_token_seconds{model=\"first\\\"\\\\\\nmodel\"} 0.25\n")
	assert.Contains(t, body, "llamaswap_model_prefill_tokens_per_second{model=\"first\\\"\\\\\\nmodel\"} 0\n")
	assert.Contains(t, body, "llamaswap_model_generation_tokens_per_second{model=\"first\\\"\\\\\\nmodel\"} 42\n")
	assert.NotContains(t, body, "missing")
}
