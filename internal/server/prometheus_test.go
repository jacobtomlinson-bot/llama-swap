package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/perf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cancelRequestKey struct{}

func TestServer_PrometheusCanonicalModel(t *testing.T) {
	cfg, err := config.LoadConfigFromReader(strings.NewReader(`
apiKeys: [test-key]
models:
  canonical:
    cmd: unused -port ${PORT}
    aliases: ["nick"]
  other:
    cmd: unused -port ${PORT}
`))
	require.NoError(t, err)
	local := newStubRouter([]string{"canonical", "other", "not-configured"}, "")
	local.serveHTTP = func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("X-Test-Mode") {
		case "stream":
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			fmt.Fprint(w, ": keepalive\r\n\r\ndata: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\r\n\r\n")
			w.(http.Flusher).Flush()
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":")
			fmt.Fprint(w, "\"first text\"}}]}\r\n\r\n")
			fmt.Fprint(w, "data: {\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":2}}\n\n")
			for range 2 {
				fmt.Fprint(w, "data: {\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":5,\"prompt_tokens_details\":{\"cached_tokens\":6}},\"timings\":{\"prompt_per_second\":200,\"predicted_per_second\":40}}\n\n")
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		case "partial-timings-stream":
			w.Header().Set("Content-Type", "text/event-stream")
			for range 2 {
				fmt.Fprint(w, `data: {"timings":{"prompt_n":11,"cache_n":4,"predicted_n":3,"prompt_per_second":150,"predicted_per_second":30}}`+"\n\n")
			}
			fmt.Fprint(w, `data: {"timings":{"predicted_n":5}}`+"\n\n")
			fmt.Fprint(w, `data: {"timings":{"prompt_per_second":250,"predicted_per_second":55}}`+"\n\ndata: [DONE]\n\n")
		case "terminal-usage":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, `data: {"timings":{"predicted_n":3}}`+"\n\n")
			fmt.Fprint(w, `data: {"usage":{"completion_tokens":5},"timings":{"prompt_per_second":260,"predicted_per_second":56}}`+"\n\ndata: [DONE]\n\n")
		case "infill-partial-timings":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, `data: {"timings":{"prompt_n":11,"predicted_n":3}}`+"\n\n")
			fmt.Fprint(w, `data: {"timings":{"prompt_per_second":270,"predicted_per_second":57}}`+"\n\ndata: [DONE]\n\n")
		case "zero-contradicts-cache":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, `data: {"usage":{"prompt_tokens":10}}`+"\n\n")
			fmt.Fprint(w, `data: {"usage":{"prompt_tokens":0,"prompt_tokens_details":{"cached_tokens":9}}}`+"\n\ndata: [DONE]\n\n")
		case "no-text-stream":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"timings\":{\"prompt_per_second\":150,\"predicted_per_second\":30}}\n\ndata: [DONE]\n\n")
		case "cached-only":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"usage\":{\"prompt_tokens_details\":{\"cached_tokens\":3}}}\n\ndata: [DONE]\n\n")
		case "mixed-scope":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"timings":{"prompt_n":2},"usage":{"prompt_tokens_details":{"cached_tokens":8}}}`)
		case "cache-exceeds-input":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"usage":{"prompt_tokens":2,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":9}}}`)
		case "zero-input":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"usage":{"prompt_tokens":0,"prompt_tokens_details":{"cached_tokens":9}},"timings":{"prompt_n":3}}`)
		case "processed-only":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"timings":{"prompt_n":4,"predicted_n":2}}`)
		case "zero-negative":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"usage":{"prompt_tokens":-9,"completion_tokens":0,"prompt_tokens_details":{"cached_tokens":-1}}}`)
		case "canceled":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"usage":{"prompt_tokens":900,"completion_tokens":800}}`)
			r.Context().Value(cancelRequestKey{}).(context.CancelFunc)()
		case "failed":
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, `{"usage":{"prompt_tokens":900,"completion_tokens":800},"timings":{"prompt_per_second":999,"predicted_per_second":999}}`)
		case "no-rates":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"usage":{"prompt_tokens":12,"completion_tokens":7}}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"usage":{"prompt_tokens":15,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":4}},"timings":{"prompt_per_second":150,"predicted_per_second":30}}`)
		}
	}
	s := newTestServerWithConfig(cfg, local, newStubRouter(nil, ""))
	t.Cleanup(func() { s.store.Close() })
	s.perf, err = perf.New(config.PerformanceConfig{Every: time.Second}, s.logs.ProxyLogs)
	require.NoError(t, err)

	post := func(path, model, mode string, status int) {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(fmt.Sprintf(`{"model":%q}`, model)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Test-Mode", mode)
		req.Header.Set("Authorization", "Bearer test-key")
		if mode == "canceled" {
			ctx, cancel := context.WithCancel(req.Context())
			defer cancel()
			req = req.WithContext(context.WithValue(ctx, cancelRequestKey{}, cancel))
		}
		s.ServeHTTP(w, req)
		require.Equal(t, status, w.Code, "%s", w.Body.String())
	}
	scrape := func() string {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.Header.Set("Authorization", "Bearer test-key")
		s.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Header().Get("Content-Type"), "version=0.0.4")
		return w.Body.String()
	}

	unauthenticated := httptest.NewRecorder()
	s.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusUnauthorized, unauthenticated.Code)
	assert.NotContains(t, unauthenticated.Body.String(), "llamaswap_model_")
	assert.NotContains(t, scrape(), "llamaswap_model_")
	post("/v1/chat/completions", "canonical", "", http.StatusOK)
	body := scrape()
	assert.Contains(t, body, "llamaswap_model_prefill_tokens_per_second{model=\"canonical\"} 150\n")
	assert.NotContains(t, body, "llamaswap_model_time_to_first_token_seconds")
	post("/v1/chat/completions", "nick", "stream", http.StatusOK)
	assert.Contains(t, scrape(), "llamaswap_model_prefill_tokens_per_second{model=\"canonical\"} 200\n")
	post("/upstream/nick/v1/chat/completions", "ignored", "", http.StatusOK)
	assert.Contains(t, scrape(), "llamaswap_model_prefill_tokens_per_second{model=\"canonical\"} 150\n")
	post("/v1/chat/completions", "nick", "stream", http.StatusOK)
	post("/v1/chat/completions", "nick", "no-rates", http.StatusOK)
	post("/v1/chat/completions", "canonical", "failed", http.StatusBadGateway)
	post("/v1/chat/completions", "canonical", "canceled", http.StatusOK)
	post("/v1/chat/completions", "canonical", "zero-negative", http.StatusOK)
	post("/v1/chat/completions", "other", "cached-only", http.StatusOK)
	post("/upstream/nick/v1/chat/completions", "ignored", "mixed-scope", http.StatusOK)
	post("/v1/chat/completions", "nick", "cache-exceeds-input", http.StatusOK)
	post("/v1/chat/completions", "other", "zero-input", http.StatusOK)
	post("/v1/chat/completions", "nick", "processed-only", http.StatusOK)
	post("/v1/chat/completions", "other", "no-text-stream", http.StatusOK)
	post("/v1/embeddings", "canonical", "", http.StatusOK)
	// Even a successful reply from a permissive router cannot put an
	// unknown request model into the Prometheus label set.
	post("/v1/chat/completions", "not-configured", "", http.StatusOK)

	body = scrape()
	for name, total := range map[string]int{"input": 88, "output": 26, "cached": 28} {
		metric := "llamaswap_model_" + name + "_tokens_total"
		assert.Contains(t, body, "# TYPE "+metric+" counter\n")
		assert.Contains(t, body, fmt.Sprintf("%s{model=\"canonical\"} %d\n", metric, total))
	}
	assert.Contains(t, body, "llamaswap_model_cached_tokens_total{model=\"other\"} 3\n")
	assert.NotContains(t, body, "llamaswap_model_input_tokens_total{model=\"other\"}")
	assert.NotContains(t, body, "llamaswap_model_output_tokens_total{model=\"other\"}")
	assert.Contains(t, body, "# TYPE llamaswap_model_time_to_first_token_seconds gauge")
	assert.Contains(t, body, "llamaswap_model_time_to_first_token_seconds{model=\"canonical\"} ")
	assert.Contains(t, body, "llamaswap_model_prefill_tokens_per_second{model=\"canonical\"} 200\n")
	assert.Contains(t, body, "llamaswap_model_generation_tokens_per_second{model=\"canonical\"} 40\n")
	assert.Contains(t, body, "llamaswap_model_generation_tokens_per_second{model=\"other\"} 30\n")
	assert.NotContains(t, body, "llamaswap_model_time_to_first_token_seconds{model=\"other\"}")
	assert.NotContains(t, body, "model=\"nick\"")
	assert.NotContains(t, body, "model=\"ignored\"")
	assert.NotContains(t, body, "model=\"not-configured\"")

	post("/v1/chat/completions", "nick", "partial-timings-stream", http.StatusOK)
	body = scrape()
	for name, total := range map[string]int{"input": 103, "output": 31, "cached": 32} {
		assert.Contains(t, body, fmt.Sprintf("llamaswap_model_%s_tokens_total{model=\"canonical\"} %d\n", name, total))
	}
	assert.Contains(t, body, "llamaswap_model_prefill_tokens_per_second{model=\"canonical\"} 250\n")
	assert.Contains(t, body, "llamaswap_model_generation_tokens_per_second{model=\"canonical\"} 55\n")
	assert.NotContains(t, body, "model=\"nick\"")
	for _, tc := range []struct {
		path, mode                          string
		in, out, cache, prefill, generation int
	}{
		{"/v1/chat/completions", "terminal-usage", 103, 36, 32, 260, 56},
		{"/infill", "infill-partial-timings", 114, 39, 32, 270, 57},
		{"/v1/chat/completions", "zero-contradicts-cache", 124, 39, 32, 270, 57},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			post(tc.path, "nick", tc.mode, http.StatusOK)
			body := scrape()
			for name, total := range map[string]int{"input": tc.in, "output": tc.out, "cached": tc.cache} {
				assert.Contains(t, body, fmt.Sprintf("llamaswap_model_%s_tokens_total{model=\"canonical\"} %d\n", name, total))
			}
			assert.Contains(t, body, fmt.Sprintf("llamaswap_model_prefill_tokens_per_second{model=\"canonical\"} %d\n", tc.prefill))
			assert.Contains(t, body, fmt.Sprintf("llamaswap_model_generation_tokens_per_second{model=\"canonical\"} %d\n", tc.generation))
			assert.NotContains(t, body, "model=\"nick\"")
		})
	}
}

func TestResponseBodyCopier_FirstOutput(t *testing.T) {
	for _, elapsed := range []time.Duration{0, 6 * time.Millisecond} {
		t.Run(elapsed.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rec := newBodyCopier(httptest.NewRecorder())
				rec.prometheusModel = "m"
				rec.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(rec, ": comment\n\ndata: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
				assert.Equal(t, -1.0, rec.ttftSeconds())
				fmt.Fprint(rec, "data: {\"choices\":[{\"delta\":{\"content\":\"hel")
				assert.Equal(t, -1.0, rec.ttftSeconds())
				time.Sleep(elapsed)
				fmt.Fprint(rec, "lo\"}}]}\n\n")
				require.Equal(t, elapsed.Seconds(), rec.ttftSeconds())
				// A later delta must not replace the first, even when it measured 0.
				time.Sleep(time.Millisecond)
				fmt.Fprint(rec, "data: {\"choices\":[{\"delta\":{\"content\":\"world\"}}]}\n\n")
				assert.Equal(t, elapsed.Seconds(), rec.ttftSeconds())
			})
		})
	}
}
