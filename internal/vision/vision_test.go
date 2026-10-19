package vision

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ashutoshm97/curly-engine/internal/llm"
	"github.com/ashutoshm97/curly-engine/internal/rawdeck"
)

func TestAnnotate(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"inlineData"`) || !strings.Contains(string(body), `"mimeType":"image/png"`) {
			t.Errorf("request has no image: %.200s", body)
		}
		ans, _ := json.Marshal(map[string]any{"type": "doughnut", "seriesColors": []string{"#0f1632", "a4acaf", "nope"}, "dataLabels": true, "labelColor": "#FFFFFF", "legend": "none", "holePct": 60})
		json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": string(ans)}}}}}})
	}))
	defer srv.Close()
	g := llm.NewGemini("test")
	g.BaseURL = srv.URL
	box := rawdeck.Box{X: 0.1, Y: 0.2, W: 0.5, H: 0.6}
	withRef := &rawdeck.Element{Kind: rawdeck.KindImage, Role: rawdeck.RoleChart, Box: box, ImageRef: "r1"}
	again := &rawdeck.Element{Kind: rawdeck.KindImage, Role: rawdeck.RoleChart, Box: box, ImageRef: "r1"}
	noRef := &rawdeck.Element{Kind: rawdeck.KindImage, Role: rawdeck.RoleChart, Box: box}
	d := &rawdeck.Deck{Slides: []*rawdeck.Slide{{Elements: []*rawdeck.Element{withRef}}, {Elements: []*rawdeck.Element{noRef}}, {Elements: []*rawdeck.Element{again}}}}
	d.AddAsset("r1", "image/png", []byte("png"))
	n, err := (&ChartReader{Client: g, Model: "m"}).Annotate(context.Background(), d)
	if err != nil || n != 2 || calls != 1 {
		t.Fatalf("annotated %d, calls %d, err %v", n, calls, err)
	}
	ch := withRef.Chart
	if ch.Type != "doughnut" || ch.HolePct != 60 || ch.Legend != "" || len(ch.Series) != 2 || ch.Series[0].Hex != "#0F1632" || ch.Series[1].Hex != "#A4ACAF" || ch.LabelColor.Hex != "#FFFFFF" {
		t.Errorf("chart = %+v", ch)
	}
	if noRef.Chart == nil || noRef.Chart.Type != "doughnut" {
		t.Error("chart without image did not inherit the style at the same position")
	}
}
