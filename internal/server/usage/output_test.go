package usage

import "testing"

func TestUsage_SSELineHasOutput(t *testing.T) {
	for _, tt := range []struct {
		name, line string
		want       bool
	}{
		{"chat", `data: {"choices":[{"delta":{"content":"text"}}]}`, true},
		{"legacy completion", `data: {"choices":[{"text":" token"}]}`, true},
		{"llama completion", `data: {"content":"text","stop":false}`, true},
		{"responses", `data: {"type":"response.output_text.delta","delta":"text"}`, true},
		{"messages", `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"text"}}`, true},
		{"role", `data: {"choices":[{"delta":{"role":"assistant","content":""}}]}`, false},
		{"reasoning", `data: {"choices":[{"delta":{"reasoning_content":"text"}}]}`, false},
		{"tools", `data: {"choices":[{"delta":{"tool_calls":[{"index":0}]}}]}`, false},
		{"usage", `data: {"choices":[],"usage":{"completion_tokens":1}}`, false},
		{"loading", `data: {"choices":[],"progress":{"status":"loading"}}`, false},
		{"done", `data: [DONE]`, false},
		{"invalid", `data: {"content":"text"`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := SSELineHasOutput([]byte(tt.line)); got != tt.want {
				t.Errorf("SSELineHasOutput(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}
