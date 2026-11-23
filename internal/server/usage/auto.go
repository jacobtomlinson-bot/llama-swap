package usage

import (
	"time"

	"github.com/tidwall/gjson"
)

// autoExtractor is used when the upstream is unknown, and for llama-swap
// peers, which pass through whatever their own upstream returns. It reads
// the API usage block and then applies every server's extra fields it finds,
// in the order oMLX rates, TabbyAPI rates, TensorFold, llama-server timings
// (plus the kyojin and radiance fields kept in the same block), vLLM metrics,
// so a more precise source overrides a less precise one. Hipfire's explicit
// prefill and decode rates are applied after generic timings and before vLLM
// metrics.
type autoExtractor struct{}

func (autoExtractor) newParser(path string, start time.Time) usageParser {
	return &autoParser{start: start, read: apiReaderFor(path), api: newAPIUsage()}
}

type autoParser struct {
	start      time.Time
	read       apiReader
	api        apiUsage
	block      gjson.Result // last usage block
	timings    timingBlock
	metrics    gjson.Result
	tensorfold usageBlock
}

func (p *autoParser) event(ev gjson.Result) {
	ev = lastArrayElement(ev)
	p.timings.readEvent(ev, p.read, &p.api)
	if b := ev.Get("usage"); b.IsObject() {
		p.block = b
	}
	if m := ev.Get("metrics"); m.IsObject() {
		p.metrics = m
	}
	p.tensorfold.update(ev.Get("tensorfold"), "cached")
}

func (p *autoParser) usage() Usage {
	u := p.api.toUsage(p.start)
	applyOMLXRates(&u, p.block)
	applyTabbyAPIRates(&u, p.block) // TabbyAPI's fields win when both appear
	applyTensorFold(&u, p.tensorfold)
	applyTimings(&u, p.timings)
	applyKyojinDrafts(&u, p.timings.last)
	applyRadianceTotal(&u, p.timings.last)
	applyHipfireTimings(&u, p.timings.last)
	applyVLLMMetrics(&u, p.metrics)
	return u
}
