package perf

import (
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestModelTokenCounters_Reload(t *testing.T) {
	initial := NewModelTokenCounters([]string{"keep", "remove"}, nil)
	initial.Observe("keep", 2, 1, 0)
	initial.Observe("remove", 9, 0, 0)

	next := NewModelTokenCounters([]string{"keep", "new"}, initial)
	// Completion from an old in-flight request still contributes to "keep".
	initial.Observe("keep", 3, 4, 1)
	initial.Observe("remove", 1, 0, 0)
	next.Observe("keep", 5, 7, -1)
	next.Observe("unknown", 42, 42, 42)
	next.Observe("new", 0, -1, 0)

	var out strings.Builder
	next.WritePrometheus(&out)
	body := out.String()
	assert.Contains(t, body, "llamaswap_model_input_tokens_total{model=\"keep\"} 10\n")
	assert.Contains(t, body, "llamaswap_model_output_tokens_total{model=\"keep\"} 12\n")
	assert.Contains(t, body, "llamaswap_model_cached_tokens_total{model=\"keep\"} 1\n")
	for _, absent := range []string{"remove", "new", "unknown"} {
		assert.NotContains(t, body, "model=\""+absent+"\"")
	}

	readded := NewModelTokenCounters([]string{"remove"}, next)
	assert.Empty(t, tokenScrape(readded))
	assert.Empty(t, tokenScrape(NewModelTokenCounters([]string{"keep"}, nil)))
}

func TestModelTokenCounters_ConcurrentScrapes(t *testing.T) {
	c := NewModelTokenCounters([]string{"m"}, nil)
	const goroutines, iterations = 8, 100
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range iterations {
				c.Observe("m", 2, 3, 1)
				_ = tokenScrape(c)
			}
		})
	}
	wg.Wait()
	body := tokenScrape(c)
	assert.Contains(t, body, "llamaswap_model_input_tokens_total{model=\"m\"} 1600\n")
	assert.Contains(t, body, "llamaswap_model_output_tokens_total{model=\"m\"} 2400\n")
	assert.Contains(t, body, "llamaswap_model_cached_tokens_total{model=\"m\"} 800\n")
}

func TestModelTokenCounters_Saturates(t *testing.T) {
	c := NewModelTokenCounters([]string{"m"}, nil)
	c.models["m"].totals[0] = math.MaxUint64 - 1
	c.Observe("m", 3, 0, -1)
	assert.Contains(t, tokenScrape(c), "llamaswap_model_input_tokens_total{model=\"m\"} 18446744073709551615\n")
}

func tokenScrape(c *ModelTokenCounters) string {
	var out strings.Builder
	c.WritePrometheus(&out)
	return out.String()
}
