package usage

import (
	"time"

	"github.com/tidwall/gjson"
)

// llamaServerExtractor reads the API usage block plus llama-server's
// timings block, whose valid counts take precedence within the same event.
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

// timingBlock keeps the latest performance data and a processed-only
// fallback. It merges validated token counts into apiUsage as frames arrive.
type timingBlock struct {
	last, processed gjson.Result
}

func (b *timingBlock) readEvent(ev gjson.Result, read apiReader, api *apiUsage) {
	frameInput := read(ev, api)
	b.update(ev.Get("timings"), api, frameInput)
}

func (b *timingBlock) update(timings gjson.Result, api *apiUsage, frameInput int) {
	if !timings.IsObject() {
		return
	}
	b.last = timings
	processed, cache := timings.Get("prompt_n"), timings.Get("cache_n")
	// prompt_n counts processed tokens only; cache_n is the reused
	// portion. The pair is required to derive the whole prompt.
	n, hasProcessed := reportedCount(processed)
	c, hasCache := reportedCount(cache)
	switch {
	case hasProcessed && hasCache:
		// A complete pair is a new prompt observation. Check the cache
		// against any explicit API total in this frame, not an old
		// prompt this pair is about to replace.
		if total := addCounts(n, c); total >= 0 && (frameInput < 0 || c <= frameInput) {
			api.recordFullPrompt(total)
			api.cached = c
		}
	case hasCache:
		// Without a pair, a cache update must fit the retained prompt.
		api.recordCache(c, frameInput)
	case hasProcessed && !cache.Exists():
		b.processed = processed
	}
	// Accumulate output in stream order. Timings win within a frame;
	// a new API count can still supersede an earlier timing count.
	setReported(&api.output, timings.Get("predicted_n"))
}

// applyTimings adds the processed-only fallback and the final rates,
// duration, and speculative decoding counts. Ordinary token counts were
// already merged in event order by update.
func applyTimings(u *Usage, block timingBlock) {
	timings := block.last
	if !timings.Exists() {
		return
	}
	u.Found = true
	if u.prompt == promptUnknown && u.Tokens.CachedTokens < 0 {
		// Never add cache reported by another source or frame to the
		// legacy processed-only fallback (e.g. infill).
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
