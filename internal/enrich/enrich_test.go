package enrich

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/ashutoshm97/curly-engine/internal/llm"
	"github.com/ashutoshm97/curly-engine/internal/normalize"
	"github.com/ashutoshm97/curly-engine/internal/ooxml"
)

func TestEnrich(t *testing.T) {
	data, err := os.ReadFile("../../testdata/acme.pptx")
	if err != nil {
		t.Fatal(err)
	}
	d, err := ooxml.Extract(data, ooxml.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	p, err := normalize.Build(d, normalize.Options{ID: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	before := len(p.Layouts)
	last := p.Layouts[len(p.Layouts)-1].ID
	ans := map[string]any{
		"layouts": []any{
			map[string]any{"id": p.Layouts[0].ID, "name": "Cover", "hide": true},
			map[string]any{"id": p.Layouts[1].ID, "name": "Key points", "darkVariant": true},
			map[string]any{"id": last, "name": "Footnotes", "hide": true, "hideReason": "legal footnotes"},
			map[string]any{"id": "L99", "name": "ignored"},
		},
		"chartColors": []string{"accent1", "#FF0000", "accent2"}, "chartType": "line",
		"quote":  map[string]any{"Color": "notAToken", "Step": "h1", "Italic": true},
		"accent": "accent2",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := json.Marshal(ans)
		json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": string(b)}}}}}})
	}))
	defer srv.Close()
	g := llm.NewGemini("k")
	g.BaseURL = srv.URL
	n, err := (&Enricher{Client: g, Model: "m"}).Enrich(context.Background(), p)
	if err != nil || n == 0 {
		t.Fatalf("changes %d, err %v", n, err)
	}
	if p.Layouts[0].Name != "Cover" || p.Layouts[0].Hidden {
		t.Errorf("first layout = %+v (renamed, never hidden)", p.Layouts[0])
	}
	if l := p.Layout(last); !l.Hidden || l.HiddenReason != "legal footnotes" {
		t.Errorf("footnote layout not hidden: %+v", l)
	}
	names := map[string]bool{}
	for _, l := range p.Layouts[before:] {
		if l.Origin != "llm" {
			t.Errorf("added layout %s without origin", l.ID)
		}
		names[l.Name] = true
	}
	for _, want := range []string{"Title + chart", "Quote", "Agenda", "Timeline", "Key points (dark)"} {
		if !names[want] {
			t.Errorf("missing AI layout %q (have %v)", want, names)
		}
	}
	for _, c := range p.Components {
		if c.Type == "chart" && c.Origin == "llm" {
			if c.Chart.Type != "line" || len(c.Chart.Series) != 2 {
				t.Errorf("chart spec = %+v (invalid tokens must be dropped)", c.Chart)
			}
		}
		if c.ID == "C.quote" && (c.Text.Color != "dk1" || c.Text.Step != "h1" || !c.Text.Italic) {
			t.Errorf("quote = %+v", c.Text)
		}
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}
