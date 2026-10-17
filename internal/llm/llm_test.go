package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestGeminiRequestAndCache(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.URL.Path != "/models/gemini-2.5-flash:generateContent" || r.Header.Get("x-goog-api-key") != "k" {
			t.Errorf("bad request %s", r.URL.Path)
		}
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		cfg := body["generationConfig"].(map[string]any)
		if cfg["responseMimeType"] != "application/json" || cfg["thinkingConfig"].(map[string]any)["thinkingBudget"].(float64) != 0 {
			t.Errorf("config = %v", cfg)
		}
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"{\"ok\":"},{"text":"true}"}]}}],"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":3}}`))
	}))
	defer srv.Close()
	g := NewGemini("k")
	g.BaseURL = srv.URL
	c := NewCached(g, nil, 16, time.Hour)
	req := Request{Model: "gemini-2.5-flash", Prompt: "hi", Schema: map[string]any{"type": "OBJECT"}}
	out, u, err := c.GenerateJSON(context.Background(), req)
	if err != nil || string(out) != `{"ok":true}` || u.InputTokens != 12 {
		t.Fatalf("out=%s u=%+v err=%v", out, u, err)
	}
	_, u, _ = c.GenerateJSON(context.Background(), req)
	if !u.CacheHit || calls.Load() != 2 {
		t.Errorf("cache miss: calls=%d", calls.Load())
	}
}
