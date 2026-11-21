package perf

import (
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"sync"
)

// ModelMetrics stores the latest known value of each metric for each canonical
// model. The zero value is ready to use; observations don't depend on the
// activity store or its retention policy.
type ModelMetrics struct {
	mu     sync.Mutex
	models map[string][3]sample
}

type sample struct {
	value float64
	valid bool
}

// Observe updates only the values present in a completed request. Negative
// values mean not reported. Zero is a valid observation for all three gauges.
func (m *ModelMetrics) Observe(model string, ttft, prefill, generation float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	values := m.models[model]
	changed := false
	for i, value := range [...]float64{ttft, prefill, generation} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			continue
		}
		values[i] = sample{value, true}
		changed = true
	}
	if changed {
		if m.models == nil {
			m.models = make(map[string][3]sample)
		}
		m.models[model] = values
	}
}

// WritePrometheus appends grouped Prometheus text format gauge families.
// Snapshot before writing so a slow scraper doesn't block request completion.
func (m *ModelMetrics) WritePrometheus(w io.Writer) {
	m.mu.Lock()
	models := maps.Clone(m.models)
	m.mu.Unlock()

	names := slices.Sorted(maps.Keys(models))
	for i, metric := range [...]struct{ name, help string }{
		{"llamaswap_model_time_to_first_token_seconds", "Latest observed proxy time to first streamed text delta in seconds"},
		{"llamaswap_model_prefill_tokens_per_second", "Latest reported prefill rate in uncached prompt tokens per second"},
		{"llamaswap_model_generation_tokens_per_second", "Latest reported decode rate in generated tokens per second"},
	} {
		writtenHelp := false
		for _, name := range names {
			if s := models[name][i]; s.valid {
				if !writtenHelp {
					fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n", metric.name, metric.help, metric.name)
					writtenHelp = true
				}
				fmt.Fprintf(w, "%s{model=\"%s\"} %g\n", metric.name, sanitizeLabel(name), s.value)
			}
		}
	}
}
