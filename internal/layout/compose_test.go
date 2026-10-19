package layout

import (
	"os"
	"strings"
	"testing"

	"github.com/ashutoshm97/curly-engine/internal/content"
	"github.com/ashutoshm97/curly-engine/internal/normalize"
	"github.com/ashutoshm97/curly-engine/internal/ooxml"
	"github.com/ashutoshm97/curly-engine/internal/profile"
	"github.com/ashutoshm97/curly-engine/internal/render"
)

func acme(t *testing.T) *profile.StyleProfile {
	data, err := os.ReadFile("../../testdata/acme.pptx")
	if err != nil {
		t.Fatal(err)
	}
	d, err := ooxml.Extract(data, ooxml.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	p, err := normalize.Build(d, normalize.Options{ID: "acme", Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestComposeAllLayouts(t *testing.T) {
	p := acme(t)
	long := strings.Repeat("This sentence is deliberately long to force shrinking and truncation. ", 30)
	slides := []content.Slide{
		{Layout: "L1", Title: "Nimbus Launch Plan", Subtitle: "Go-to-market review"},
		{Layout: "L2", Title: "Why now", Bullets: []string{"Demand is up 40%", "Costs are down", "Competitors are slow"}, ImagePrompt: "team at whiteboard"},
		{Layout: "L2", Title: "Overflow", Bullets: []string{long}},
		{Layout: "L3", Title: "Traction", Items: []content.Item{{Value: "3.4x", Label: "Faster"}, {Value: "120k", Label: "Users"}, {Value: "$2M", Label: "ARR"}}},
		{Layout: "L4", Title: "Pricing", Table: &content.Table{Header: []string{"Plan", "Price"}, Rows: [][]string{{"Starter", "$12"}, {"Pro", "$29"}}}},
	}
	for i, c := range slides {
		s := Compose(p, c, i+1)
		if len(s.Elements) == 0 {
			t.Fatalf("slide %d empty", i+1)
		}
		for _, e := range s.Elements {
			if e.X < 0 || e.Y < 0 || e.X+e.W > p.WidthPt+0.5 || e.Y+e.H > p.HeightPt+0.5 {
				t.Errorf("slide %d %s out of bounds: %+v", i+1, e.Slot, e)
			}
			if e.Text != nil && len(e.Text.Lines) > 0 {
				last := e.Text.Lines[len(e.Text.Lines)-1]
				if last.Baseline > e.H+e.Text.SizePt*0.3 {
					t.Errorf("slide %d %s overflows: baseline %.1f > h %.1f", i+1, e.Slot, last.Baseline, e.H)
				}
			}
		}
		svg := render.SVG(s, p.WidthPt, p.HeightPt, func(r string) string { return "/api/assets/" + r })
		if os.Getenv("DUMP") != "" {
			os.WriteFile("/tmp/slide"+string(rune('0'+i+1))+".svg", []byte(svg), 0o644)
		}
	}
	over := Compose(p, slides[2], 3)
	for _, e := range over.Elements {
		if e.Slot == "body" && !e.Text.Truncated {
			t.Error("expected overflow body to be truncated")
		}
	}
}
