package llm

import (
	"context"
	"encoding/json"
	"io"
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

func TestGeminiHonorsRetryInfo(t *testing.T) {
	var calls int
	var first time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			first = time.Now()
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"error":{"code":429,"message":"quota\nmore","details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"1.2s"}]}}`)
			return
		}
		if d := time.Since(first); d < 1200*time.Millisecond {
			t.Errorf("retried after %v, want >= 1.2s", d)
		}
		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"{}"}]}}],"usageMetadata":{"promptTokenCount":3}}`)
	}))
	defer srv.Close()
	g := NewGemini("k")
	g.BaseURL = srv.URL
	out, u, err := g.GenerateJSON(context.Background(), Request{Model: "m", Prompt: "p"})
	if err != nil || string(out) != "{}" || u.InputTokens != 3 || calls != 2 {
		t.Fatalf("out=%s u=%+v err=%v calls=%d", out, u, err, calls)
	}
}

func TestGeminiFailsFastOnLongRetryInfo(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"message":"daily quota","details":[{"retryDelay":"35061s"}]}}`)
	}))
	defer srv.Close()
	g := NewGemini("k")
	g.BaseURL = srv.URL
	start := time.Now()
	_, _, err := g.GenerateJSON(context.Background(), Request{Model: "m", Prompt: "p"})
	if err == nil || calls != 1 || time.Since(start) > time.Second {
		t.Fatalf("err=%v calls=%d took=%v", err, calls, time.Since(start))
	}
}
