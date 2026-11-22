package usage

import (
	"time"

	"github.com/tidwall/gjson"
)

// llamaServerExtractor reads the API usage block plus llama-server's
// timings block, which is more precise and overrides the counts.
type llamaServerExtractor struct{}

func (llamaServerExtractor) newParser(path string, start time.Time) usageParser {
	return &llamaServerParser{start: start, read: apiReaderFor(path), api: newAPIUsage()}
}

type llamaServerParser struct {
	start   time.Time
	read    apiReader
	api     apiUsage
	timings usageBlock
}

func (p *llamaServerParser) event(ev gjson.Result) {
	ev = lastArrayElement(ev)
	p.read(ev, &p.api)
	p.timings.update(ev.Get("timings"), "cache_n")
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

// applyTimings applies a llama-server timings block. prompt_n counts only
// the prompt tokens processed and cache_n the ones reused from the cache, so
// the whole prompt is prompt_n + cache_n (the same number usage.prompt_tokens
// reports). predicted_n is the output count, prompt_per_second and
// predicted_per_second the rates, prompt_ms + predicted_ms the duration
// (when longer than wall time), and draft_n / draft_n_accepted the
// speculative decoding counters.
func applyTimings(u *Usage, block usageBlock) {
	timings := block.last
	if !timings.Exists() {
		return
	}
	u.Found = true
	setReported(&u.Tokens.CachedTokens, block.cache)
	currentCache := timings.Get("cache_n")
	if n, ok := reportedCount(timings.Get("prompt_n")); ok {
		if c, ok := reportedCount(currentCache); ok {
			// A matched timings pair can refine the total, but not if
			// the claimed hits contradict an already known full prompt.
			if input := addCounts(n, c); input >= 0 &&
				(u.prompt != promptFull || c <= u.Tokens.InputTokens) {
				u.Tokens.InputTokens, u.prompt = input, promptFull
			}
		} else if !currentCache.Exists() && u.prompt == promptUnknown && u.Tokens.CachedTokens < 0 {
			// Legacy processed-only fallback (e.g. infill). Never add a
			// cache count from another source/frame to this prompt_n.
			u.Tokens.InputTokens, u.prompt = n, promptProcessed
		}
	}
	setReported(&u.Tokens.OutputTokens, timings.Get("predicted_n"))
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
