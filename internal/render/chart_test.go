package render

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"

	"github.com/ashutoshm97/curly-engine/internal/profile"
	"github.com/ashutoshm97/curly-engine/internal/scene"
)

func TestChartRender(t *testing.T) {
	for _, typ := range []string{"col", "bar", "line", "area", "pie", "doughnut"} {
		c := &scene.Chart{Type: typ, Grouping: "stacked", Categories: []string{"Q1", "Q2", "Q3"},
			Series: []scene.ChartSeries{{Name: "A", Values: []float64{1, 2.5, 3}}, {Name: "B & C", Values: []float64{2, 1, 4}}},
			Colors: []string{"#C00000", "#7F7F7F"}, DataLabels: true, NumFmt: `"$"#,##0.0`, Legend: "b", Gridlines: true,
			Font: "Arial", FontSizePt: 11, TextColor: "#151515"}
		el := &scene.Element{Kind: "chart", X: 40, Y: 120, W: 640, H: 340, Chart: c}
		svg := SVG(&scene.Slide{Background: "#FFFFFF", Elements: []*scene.Element{el}}, 720, 540, nil)
		if err := xml.Unmarshal([]byte(svg), new(struct{})); err != nil {
			t.Fatalf("%s: svg not well-formed: %v", typ, err)
		}
		legend := "B &amp; C"
		if typ == "pie" || typ == "doughnut" {
			legend = ">Q2<"
		}
		if !strings.Contains(svg, "#C00000") || !strings.Contains(svg, legend) {
			t.Errorf("%s: svg missing series color or legend", typ)
		}
		data, err := PPTX(&scene.Deck{WidthPt: 720, HeightPt: 540, Slides: []*scene.Slide{{Background: "#FFFFFF", Elements: []*scene.Element{el}}}}, &profile.StyleProfile{WidthPt: 720, HeightPt: 540, Tokens: profile.Tokens{Fonts: []profile.FontToken{{Role: "heading", Family: "Arial"}, {Role: "body", Family: "Arial"}}}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		var chart, types, rels string
		for _, f := range z.File {
			r, _ := f.Open()
			b, _ := io.ReadAll(r)
			switch f.Name {
			case "ppt/charts/chart1.xml":
				chart = string(b)
			case "[Content_Types].xml":
				types = string(b)
			case "ppt/slides/_rels/slide1.xml.rels":
				rels = string(b)
			}
		}
		if err := xml.Unmarshal([]byte(chart), new(struct{})); err != nil {
			t.Fatalf("%s: chart xml: %v", typ, err)
		}
		if !strings.Contains(chart, `val="C00000"`) || !strings.Contains(types, "/ppt/charts/chart1.xml") || !strings.Contains(rels, "../charts/chart1.xml") {
			t.Errorf("%s: chart part not wired", typ)
		}
	}
}

func TestFmtNum(t *testing.T) {
	for _, c := range []struct {
		v    float64
		f, w string
	}{{12.34, `"$"#,##0.0`, "$12.3"}, {-3, "0%", "-3%"}, {2.5, "", "2.5"}, {4, "General", "4"}} {
		if g := fmtNum(c.v, c.f); g != c.w {
			t.Errorf("fmtNum(%v,%q) = %q, want %q", c.v, c.f, g, c.w)
		}
	}
}
