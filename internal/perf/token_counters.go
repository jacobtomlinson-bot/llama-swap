package perf

import (
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"sync"
)

// ModelTokenCounters holds counters for one configuration's canonical model
// IDs. The registry is immutable; reloads share only entries still configured.
type ModelTokenCounters struct {
	models map[string]*modelTokenTotals
}

type modelTokenTotals struct {
	mu     sync.Mutex
	totals [3]uint64
}

func NewModelTokenCounters(names []string, previous *ModelTokenCounters) *ModelTokenCounters {
	c := &ModelTokenCounters{models: make(map[string]*modelTokenTotals, len(names))}
	for _, name := range names {
		var totals *modelTokenTotals
		if previous != nil {
			totals = previous.models[name]
		}
		if totals == nil {
			totals = &modelTokenTotals{}
		}
		c.models[name] = totals
	}
	return c
}

// Observe adds one normalized usage record. Zero and unknown (negative) fields
// contribute nothing. Saturation prevents even an extreme report from wrapping
// a counter backwards.
func (c *ModelTokenCounters) Observe(model string, input, output, cached int) {
	if c == nil || (input <= 0 && output <= 0 && cached <= 0) {
		return
	}
	m := c.models[model]
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, n := range [...]int{input, output, cached} {
		if n > 0 {
			m.totals[i] += min(uint64(n), math.MaxUint64-m.totals[i])
		}
	}
}

// WritePrometheus snapshots each model before writing, keeping the three totals
// consistent for that model without holding locks during network writes.
func (c *ModelTokenCounters) WritePrometheus(w io.Writer) {
	if c == nil {
		return
	}
	names := slices.Sorted(maps.Keys(c.models))
	snapshot := make([][3]uint64, len(names))
	for i, name := range names {
		m := c.models[name]
		m.mu.Lock()
		snapshot[i] = m.totals
		m.mu.Unlock()
	}
	for i, metric := range [...]struct{ name, help string }{
		{"llamaswap_model_input_tokens_total", "Total reported input tokens, including cached input tokens"},
		{"llamaswap_model_output_tokens_total", "Total reported generated output tokens"},
		{"llamaswap_model_cached_tokens_total", "Total reported input tokens served from the prompt cache"},
	} {
		writtenHelp := false
		for j, name := range names {
			if n := snapshot[j][i]; n > 0 {
				if !writtenHelp {
					fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", metric.name, metric.help, metric.name)
					writtenHelp = true
				}
				fmt.Fprintf(w, "%s{model=\"%s\"} %d\n", metric.name, sanitizeLabel(name), n)
			}
		}
	}
}
