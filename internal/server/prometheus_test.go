package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/perf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer_PrometheusCanonicalModel(t *testing.T) {
	cfg, err := config.LoadConfigFromReader(strings.NewReader(`
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
			fmt.Fprint(w, "data: {\"timings\":{\"prompt_per_second\":200,\"predicted_per_second\":40}}\n\ndata: [DONE]\n\n")
		case "failed":
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, `{"timings":{"prompt_per_second":999,"predicted_per_second":999}}`)
		case "no-rates":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"usage":{"prompt_tokens":12,"completion_tokens":7}}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"timings":{"prompt_per_second":150,"predicted_per_second":30}}`)
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
		s.ServeHTTP(w, req)
		require.Equal(t, status, w.Code, "%s", w.Body.String())
	}
	scrape := func() string {
		t.Helper()
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Header().Get("Content-Type"), "version=0.0.4")
		return w.Body.String()
	}

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
	post("/v1/chat/completions", "other", "", http.StatusOK)
	post("/v1/embeddings", "canonical", "", http.StatusOK)
	// Even a successful reply from a permissive router cannot put an
	// unknown request model into the Prometheus label set.
	post("/v1/chat/completions", "not-configured", "", http.StatusOK)

	body = scrape()
	assert.Contains(t, body, "# TYPE llamaswap_model_time_to_first_token_seconds gauge")
	assert.Contains(t, body, "llamaswap_model_time_to_first_token_seconds{model=\"canonical\"} ")
	assert.Contains(t, body, "llamaswap_model_prefill_tokens_per_second{model=\"canonical\"} 200\n")
	assert.Contains(t, body, "llamaswap_model_generation_tokens_per_second{model=\"canonical\"} 40\n")
	assert.Contains(t, body, "llamaswap_model_generation_tokens_per_second{model=\"other\"} 30\n")
	assert.NotContains(t, body, "llamaswap_model_time_to_first_token_seconds{model=\"other\"}")
	assert.NotContains(t, body, "model=\"nick\"")
	assert.NotContains(t, body, "model=\"ignored\"")
	assert.NotContains(t, body, "model=\"not-configured\"")
}

func TestResponseBodyCopier_FirstOutput(t *testing.T) {
	rec := newBodyCopier(httptest.NewRecorder())
	rec.prometheusModel = "m"
	rec.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(rec, ": comment\n\ndata: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
	assert.Zero(t, rec.firstOutput)
	fmt.Fprint(rec, "data: {\"choices\":[{\"delta\":{\"content\":\"hel")
	assert.Zero(t, rec.firstOutput)
	fmt.Fprint(rec, "lo\"}}]}\n\n")
	first := rec.firstOutput
	require.Positive(t, first)
	fmt.Fprint(rec, "data: {\"choices\":[{\"delta\":{\"content\":\"world\"}}]}\n\n")
	assert.Equal(t, first, rec.firstOutput)
}
