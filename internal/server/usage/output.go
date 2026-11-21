package usage

import "github.com/tidwall/gjson"

// SSELineHasOutput identifies a single-line SSE data event with a nonempty
// generated text delta. It does not count role, tool, reasoning, usage,
// loading-state, or terminal-only events.
func SSELineHasOutput(line []byte) bool {
	data := sseData(line)
	if len(data) == 0 || !gjson.ValidBytes(data) {
		return false
	}
	ev := gjson.ParseBytes(data)
	nonempty := func(v gjson.Result) bool {
		return v.Type == gjson.String && v.Str != ""
	}
	switch ev.Get("type").Str {
	case "response.output_text.delta":
		return nonempty(ev.Get("delta"))
	case "content_block_delta": // Anthropic Messages
		return ev.Get("delta.type").Str == "text_delta" && nonempty(ev.Get("delta.text"))
	default:
		if nonempty(ev.Get("content")) { // native llama-server completion
			return true
		}
		found := false
		ev.Get("choices").ForEach(func(_, choice gjson.Result) bool {
			found = nonempty(choice.Get("delta.content")) || nonempty(choice.Get("text"))
			return !found
		})
		return found
	}
}
