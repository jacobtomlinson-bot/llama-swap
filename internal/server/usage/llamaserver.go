package usage

import (
	"time"

	"github.com/tidwall/gjson"
)

// llamaServerExtractor reads the API usage block plus llama-server's
// timings block, which takes precedence for output within the same event.
type llamaServerExtractor struct{}

func (llamaServerExtractor) newParser(path string, start time.Time) usageParser {
	return &llamaServerParser{start: start, read: apiReaderFor(path), api: newAPIUsage()}
}

type llamaServerParser struct {
	start   time.Time
	read    apiReader
	api     apiUsage
	timings timingBlock
}

func (p *llamaServerParser) event(ev gjson.Result) {
	ev = lastArrayElement(ev)
	p.timings.readEvent(ev, p.read, &p.api)
}

func (p *llamaServerParser) usage() Usage {
	u := p.api.toUsage(p.start)
	applyTimings(&u, p.timings)
	return u
}

// lastArrayElement unwraps /infill responses, which are arrays with the
// usage and timings in the last element (#463).
func lastArrayElement(ev gjson.Result) gjson.Result {
	if !ev.IsArray() {
		return ev
	}
	arr := ev.Array()
	if len(arr) == 0 {
		return gjson.Result{}
	}
	return arr[len(arr)-1]
}

// timingBlock retains token observations independently of the latest
// timings block. A whole prompt must come from one frame, never from
// combining a processed count and a cache count reported separately.
type timingBlock struct {
	usageBlock
	wholePrompt, pairedCache int
	hasWholePrompt           bool
	processed                gjson.Result
}

func (b *timingBlock) readEvent(ev gjson.Result, read apiReader, api *apiUsage) {
	read(ev, api)
	timings := ev.Get("timings")
	if !timings.IsObject() {
		return
	}
	b.usageBlock.update(timings, "cache_n")
	processed, cache := timings.Get("prompt_n"), timings.Get("cache_n")
	n, hasProcessed := reportedCount(processed)
	c, hasCache := reportedCount(cache)
	if hasProcessed && hasCache {
		if total := addCounts(n, c); total >= 0 {
			b.wholePrompt, b.pairedCache, b.hasWholePrompt = total, c, true
		}
	} else if hasProcessed && !cache.Exists() {
		b.processed = processed
	}
	// Accumulate output in stream order. Timings win within a frame;
	// a new API count can still supersede an earlier timing count.
	setReported(&api.output, timings.Get("predicted_n"))
}

// applyTimings applies a llama-server timings block. prompt_n counts only
// the prompt tokens processed and cache_n the ones reused from the cache, so
// the whole prompt is prompt_n + cache_n (the same number usage.prompt_tokens
// reports). predicted_n is the output count, prompt_per_second and
// predicted_per_second the rates, prompt_ms + predicted_ms the duration
// (when longer than wall time), and draft_n / draft_n_accepted the
// speculative decoding counters.
func applyTimings(u *Usage, block timingBlock) {
	timings := block.last
	if !timings.Exists() {
		return
	}
	u.Found = true
	setReported(&u.Tokens.CachedTokens, block.cache)
	if block.hasWholePrompt {
		// A matched pair can refine the total, unless its cache count
		// contradicts an already known full prompt.
		if u.prompt != promptFull || block.pairedCache <= u.Tokens.InputTokens {
			u.Tokens.InputTokens, u.prompt = block.wholePrompt, promptFull
		}
	} else if u.prompt == promptUnknown && u.Tokens.CachedTokens < 0 {
		// Legacy processed-only fallback (e.g. infill). Never add a
		// cache count from another source/frame to this prompt_n.
		if setReported(&u.Tokens.InputTokens, block.processed) {
			u.prompt = promptProcessed
		}
	}
	if v := timings.Get("prompt_per_second"); v.Exists() {
		u.Tokens.PromptPerSecond = v.Float()
	}
	if v := timings.Get("predicted_per_second"); v.Exists() {
		u.Tokens.TokensPerSecond = v.Float()
	}
	if ms := int(timings.Get("prompt_ms").Float() + timings.Get("predicted_ms").Float()); ms > u.DurationMs {
		u.DurationMs = ms
	}
	if drafted, accepted := timings.Get("draft_n"), timings.Get("draft_n_accepted"); drafted.Exists() && accepted.Exists() {
		u.Tokens.DraftTokens = int(drafted.Int())
		u.Tokens.DraftAccTokens = int(accepted.Int())
	}
}
