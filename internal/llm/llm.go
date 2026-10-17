// Package llm wraps Google Gemini (REST generateContent with JSON-schema
// output) behind a small interface, plus a two-level response cache.
package llm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/ashutoshm97/curly-engine/internal/store"
)

type Request struct {
	Model       string
	System      string
	Prompt      string
	Schema      map[string]any
	Temperature float64
}

type Usage struct {
	InputTokens  int  `json:"inputTokens"`
	OutputTokens int  `json:"outputTokens"`
	CachedTokens int  `json:"cachedTokens"`
	CacheHit     bool `json:"cacheHit,omitempty"`
}

type Client interface {
	GenerateJSON(ctx context.Context, r Request) ([]byte, Usage, error)
}

const DefaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

type Gemini struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client
	Retries int
}

func NewGemini(key string) *Gemini {
	return &Gemini{APIKey: key, BaseURL: DefaultBaseURL, HTTP: &http.Client{Timeout: 60 * time.Second}, Retries: 3}
}

type part struct {
	Text string `json:"text"`
}

type gcontent struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type gRequest struct {
	System   *gcontent      `json:"systemInstruction,omitempty"`
	Contents []gcontent     `json:"contents"`
	Config   map[string]any `json:"generationConfig"`
}

type gResponse struct {
	Candidates []struct {
		Content      gcontent `json:"content"`
		FinishReason string   `json:"finishReason"`
	} `json:"candidates"`
	Usage struct {
		Prompt int `json:"promptTokenCount"`
		Output int `json:"candidatesTokenCount"`
		Cached int `json:"cachedContentTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Message string `json:"message"`
		Details []struct {
			RetryDelay string `json:"retryDelay"`
		} `json:"details"`
	} `json:"error"`
}

// MaxRetryDelay caps how long a server-requested RetryInfo delay is honored.
const MaxRetryDelay = 65 * time.Second

func (r *gResponse) retryDelay() time.Duration {
	if r.Error == nil {
		return 0
	}
	for _, d := range r.Error.Details {
		if v, err := time.ParseDuration(d.RetryDelay); err == nil && v > 0 {
			return v
		}
	}
	return 0
}

// GenerateJSON calls models/{model}:generateContent with thinking disabled and a response schema.
func (g *Gemini) GenerateJSON(ctx context.Context, r Request) ([]byte, Usage, error) {
	cfg := map[string]any{
		"responseMimeType": "application/json",
		"temperature":      r.Temperature,
		"thinkingConfig":   map[string]any{"thinkingBudget": 0},
	}
	if r.Schema != nil {
		cfg["responseSchema"] = r.Schema
	}
	body := gRequest{Contents: []gcontent{{Role: "user", Parts: []part{{Text: r.Prompt}}}}, Config: cfg}
	if r.System != "" {
		body.System = &gcontent{Parts: []part{{Text: r.System}}}
	}
	payload, _ := json.Marshal(body)
	url := fmt.Sprintf("%s/models/%s:generateContent", g.BaseURL, r.Model)
	var lastErr error
	var wait time.Duration
	for attempt := 0; attempt <= g.Retries; attempt++ {
		if attempt > 0 {
			wait = max(wait, time.Duration(1<<(attempt-1))*500*time.Millisecond)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, Usage{}, ctx.Err()
			}
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-goog-api-key", g.APIKey)
		resp, err := g.HTTP.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		var gr gResponse
		_ = json.Unmarshal(data, &gr)
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("gemini %d", resp.StatusCode)
			if gr.Error != nil {
				lastErr = fmt.Errorf("gemini %d: %s", resp.StatusCode, firstLine(gr.Error.Message))
			}
			// A long server delay means a daily quota is exhausted: fail fast.
			if wait = gr.retryDelay(); wait > MaxRetryDelay {
				return nil, Usage{}, fmt.Errorf("%w (retry in %s)", lastErr, wait.Round(time.Minute))
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			msg := resp.Status
			if gr.Error != nil {
				msg = gr.Error.Message
			}
			return nil, Usage{}, fmt.Errorf("gemini: %s", msg)
		}
		if len(gr.Candidates) == 0 || len(gr.Candidates[0].Content.Parts) == 0 {
			return nil, Usage{}, errors.New("gemini: empty response")
		}
		var out bytes.Buffer
		for _, p := range gr.Candidates[0].Content.Parts {
			out.WriteString(p.Text)
		}
		return out.Bytes(), Usage{InputTokens: gr.Usage.Prompt, OutputTokens: gr.Usage.Output, CachedTokens: gr.Usage.Cached}, nil
	}
	return nil, Usage{}, fmt.Errorf("gemini: retries exhausted: %w", lastErr)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Cached memoizes identical requests in an in-process LRU backed by the SQLite cache table.
type Cached struct {
	Next  Client
	Store *store.Store
	TTL   time.Duration
	mem   *lru.Cache[string, []byte]
}

func NewCached(next Client, st *store.Store, size int, ttl time.Duration) *Cached {
	m, _ := lru.New[string, []byte](size)
	return &Cached{Next: next, Store: st, TTL: ttl, mem: m}
}

func Key(r Request) string {
	s, _ := json.Marshal(r.Schema)
	h := sha256.New()
	for _, v := range []string{r.Model, r.System, r.Prompt, string(s), fmt.Sprint(r.Temperature)} {
		h.Write([]byte(v))
		h.Write([]byte{0})
	}
	return "llm:" + hex.EncodeToString(h.Sum(nil))
}

func (c *Cached) GenerateJSON(ctx context.Context, r Request) ([]byte, Usage, error) {
	k := Key(r)
	if v, ok := c.mem.Get(k); ok {
		return v, Usage{CacheHit: true}, nil
	}
	if c.Store != nil {
		if v, ok := c.Store.CacheGet(ctx, k); ok {
			c.mem.Add(k, v)
			return v, Usage{CacheHit: true}, nil
		}
	}
	v, u, err := c.Next.GenerateJSON(ctx, r)
	if err != nil {
		return nil, u, err
	}
	c.mem.Add(k, v)
	if c.Store != nil {
		c.Store.CachePut(ctx, k, v, c.TTL)
	}
	return v, u, nil
}
