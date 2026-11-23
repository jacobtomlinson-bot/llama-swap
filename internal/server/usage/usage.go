// Package usage reads token usage from inference server responses and
// normalizes it to store.TokenMetrics. Each upstream has an extractor in
// <upstream>.go; Extract picks one from the request's hint.Upstream. See
// docs/development/upstream-compat.md.
package usage

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mostlygeek/llama-swap/internal/hint"
	"github.com/mostlygeek/llama-swap/internal/store"
	"github.com/tidwall/gjson"
)

// Usage is the normalized usage record every extractor produces. Token
// fields the upstream did not report are -1 (see store.TokenMetrics).
type Usage struct {
	// Found reports whether the response carried any usage data at all.
	Found      bool
	Tokens     store.TokenMetrics
	DurationMs int
	prompt     promptScope
}

type promptScope uint8

const (
	promptUnknown promptScope = iota
	promptFull
	promptProcessed // a processed count without an independently reported cache count
)

// checked rejects a cache count that cannot be a subset of a known full
// prompt. In particular, a reported zero total is not a missing total.
func (u Usage) checked() Usage {
	if u.prompt == promptFull && u.Tokens.CachedTokens > u.Tokens.InputTokens {
		u.Tokens.CachedTokens = -1
	}
	return u
}

// usageExtractor knows how one kind of upstream reports usage. Each
// implementation lives in its own <upstream>.go file.
type usageExtractor interface {
	// newParser returns per-request state. path is the endpoint path the
	// middleware meters, e.g. /v1/chat/completions or /infill. start is when
	// the request began, used for wall-clock duration.
	newParser(path string, start time.Time) usageParser
}

// usageParser is fed every JSON event of one response: each SSE data: line,
// or the whole body once for a non-streaming response.
type usageParser interface {
	event(ev gjson.Result)
	// usage is called after the last event.
	usage() Usage
}

// extractorFor returns the extractor for u. Unknown upstreams, generic
// OpenAI-compatible servers (any local server capcompat could not name,
// which may still add timings or rates), and upstreams that pass through
// another server's format (llama-swap peers) use auto, which reads every
// format llama-swap knows.
func extractorFor(u hint.Upstream) usageExtractor {
	switch u {
	case hint.LlamaServer:
		return llamaServerExtractor{}
	case hint.VLLM:
		return vllmExtractor{}
	case hint.TabbyAPI:
		return tabbyAPIExtractor{}
	case hint.OMLX:
		return omlxExtractor{}
	case hint.TensorFold:
		return tensorFoldExtractor{}
	case hint.Kyojin:
		return kyojinExtractor{}
	case hint.Radiance:
		return radianceExtractor{}
	case hint.Halogen:
		return halogenExtractor{}
	case hint.Hipfire:
		return hipfireExtractor{}
	case hint.OpenAI, hint.Anthropic, hint.OpenRouter:
		return apiExtractor{}
	default:
		return autoExtractor{}
	}
}

// Extract runs a decoded (already decompressed) response body through
// the extractor for u. A server-sent event stream is fed one data: line at a
// time; anything else is parsed as one JSON document.
func Extract(u hint.Upstream, path, contentType string, body []byte, start time.Time) (Usage, error) {
	p := extractorFor(u).newParser(path, start)

	if IsEventStream(contentType) {
		forEachSSEData(body, func(data []byte) {
			if gjson.ValidBytes(data) {
				p.event(gjson.ParseBytes(data))
			}
		})
		usage := p.usage()
		if !usage.Found {
			return Usage{}, fmt.Errorf("no valid JSON data found in stream")
		}
		return usage.checked(), nil
	}

	if !gjson.ValidBytes(body) {
		return Usage{}, fmt.Errorf("invalid JSON in response body")
	}
	parsed := gjson.ParseBytes(body)
	p.event(parsed)
	usage := p.usage()
	// A non-streaming response with a usage object counts as reported even
	// when it names no token field the extractor recognizes.
	if !usage.Found && parsed.Get("usage").Exists() {
		usage.Found = true
	}
	return usage.checked(), nil
}

// IsEventStream identifies an SSE response independently of optional parameters.
func IsEventStream(contentType string) bool {
	mediaType, _, _ := strings.Cut(contentType, ";")
	return strings.EqualFold(strings.TrimSpace(mediaType), "text/event-stream")
}

// forEachSSEData calls fn with the payload of every non-empty data: line in
// an event stream, skipping the [DONE] terminator.
func forEachSSEData(body []byte, fn func(data []byte)) {
	for offset := 0; offset < len(body); {
		nl := bytes.IndexByte(body[offset:], '\n')
		var line []byte
		if nl == -1 {
			line = body[offset:]
			offset = len(body)
		} else {
			line = body[offset : offset+nl]
			offset += nl + 1
		}

		if data := sseData(line); len(data) != 0 {
			fn(data)
		}
	}
}

func sseData(line []byte) []byte {
	data, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("data:"))
	if !ok {
		return nil
	}
	data = bytes.TrimSpace(data)
	if bytes.Equal(data, []byte("[DONE]")) {
		return nil
	}
	return data
}

// apiUsage accumulates normalized counts for one request in event order.
// Anthropic's raw components stay separate from the normalized totals.
type apiUsage struct {
	input, output     int
	cached, reasoning int // -1 when not reported
	found             bool
	prompt            promptScope
	anthropic         anthropicPrompt
}

func newAPIUsage() apiUsage { return apiUsage{cached: -1, reasoning: -1} }

func (a *apiUsage) recordFullPrompt(input int) {
	a.input, a.prompt = input, promptFull
	if a.cached > input {
		a.cached = -1
	}
}

// A cache observation must fit both a full prompt explicitly reported in
// this frame (including an API zero placeholder) and the retained total.
func (a *apiUsage) cacheFits(cache, frameInput int) bool {
	return (frameInput < 0 || cache <= frameInput) &&
		(a.prompt != promptFull || cache <= a.input)
}

func (a *apiUsage) recordCache(cache, frameInput int) {
	if a.cacheFits(cache, frameInput) {
		a.cached = cache
	}
}

// apiReader merges API observations and returns the frame's full prompt
// count, or -1 if absent. Backend-specific counts use that bound before
// updating the same accumulator.
type apiReader func(ev gjson.Result, u *apiUsage) int

// apiReaderFor picks the wire format from the endpoint path. The Anthropic
// Messages API (/v1/messages, or /v/messages without the version) has its
// own usage shape; every other metered endpoint uses OpenAI's.
func apiReaderFor(path string) apiReader {
	if strings.HasSuffix(path, "/messages") {
		return readAnthropicUsage
	}
	return readOpenAIUsage
}

// toUsage turns the counts into a Usage with every server-reported field
// (rates, draft counts) unknown and the duration measured from start.
func (a apiUsage) toUsage(start time.Time) Usage {
	return Usage{
		Found:  a.found,
		prompt: a.prompt,
		Tokens: store.TokenMetrics{
			InputTokens:     a.input,
			OutputTokens:    a.output,
			CachedTokens:    a.cached,
			ReasoningTokens: a.reasoning,
			PromptPerSecond: -1,
			TokensPerSecond: -1,
			DraftTokens:     -1,
			DraftAccTokens:  -1,
		},
		DurationMs: int(time.Since(start).Milliseconds()),
	}
}

// reportedCount accepts only representable, nonnegative integer token counts.
func reportedCount(v gjson.Result) (int, bool) {
	if v.Type != gjson.Number {
		return 0, false
	}
	n, err := strconv.Atoi(v.Raw)
	return n, err == nil && n >= 0
}

// addCounts combines nonnegative reported components. An unrepresentable sum
// is unknown, not a wrapped or estimated token count.
func addCounts(counts ...int) int {
	var total int
	for _, n := range counts {
		if n > math.MaxInt-total {
			return -1
		}
		total += n
	}
	return total
}

// setCount retains earlier positive values when a later stream frame carries
// a zero placeholder. It reports whether v is a valid token count.
func setCount(dst *int, v gjson.Result) bool {
	n, ok := reportedCount(v)
	if ok && n > 0 {
		*dst = n
	}
	return ok
}

// setReported stores an explicit count, including zero. Invalid or unknown
// fields do not erase an earlier observation.
func setReported(dst *int, v gjson.Result) bool {
	n, ok := reportedCount(v)
	if ok {
		*dst = n
	}
	return ok
}

// apiExtractor reads only the API usage block. It serves hosted APIs
// (OpenAI, Anthropic, OpenRouter), which add nothing to it.
type apiExtractor struct{}

func (apiExtractor) newParser(path string, start time.Time) usageParser {
	return &apiParser{start: start, read: apiReaderFor(path), api: newAPIUsage()}
}

type apiParser struct {
	start time.Time
	read  apiReader
	api   apiUsage
}

func (p *apiParser) event(ev gjson.Result) { p.read(ev, &p.api) }
func (p *apiParser) usage() Usage          { return p.api.toUsage(p.start) }
