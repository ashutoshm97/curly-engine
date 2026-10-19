// Package vision asks a Gemini vision model for the style of charts that a
// deck only contains as pictures (PDF exports, screenshots). It runs once per
// distinct image at extraction time; results are cached by image hash.
package vision

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/ashutoshm97/curly-engine/internal/llm"
	"github.com/ashutoshm97/curly-engine/internal/rawdeck"
)

type ChartReader struct {
	Client   llm.Client
	Model    string
	MaxCalls int // distinct images per deck; 0 = 6
}

const prompt = `This image is a chart from a presentation slide. Describe only its visual style, never its data.
Return: the chart type ("none" if it is not a chart); grouping for bars/areas; the fill colors of its series in order
(for pie/doughnut: slice colors clockwise from 12 o'clock) as #RRGGBB; whether values are printed on the chart and their color;
the legend position ("none" if absent); whether gridlines are drawn; and the doughnut hole size in percent of the radius.`

var schema = map[string]any{
	"type": "OBJECT",
	"properties": map[string]any{
		"type":         map[string]any{"type": "STRING", "enum": []string{"col", "bar", "line", "area", "pie", "doughnut", "none"}},
		"grouping":     map[string]any{"type": "STRING", "enum": []string{"clustered", "stacked", "percentStacked", "standard"}},
		"seriesColors": map[string]any{"type": "ARRAY", "items": map[string]any{"type": "STRING"}},
		"dataLabels":   map[string]any{"type": "BOOLEAN"},
		"labelColor":   map[string]any{"type": "STRING"},
		"legend":       map[string]any{"type": "STRING", "enum": []string{"none", "b", "t", "l", "r"}},
		"gridlines":    map[string]any{"type": "BOOLEAN"},
		"holePct":      map[string]any{"type": "INTEGER"},
	},
	"required": []string{"type"},
}

type answer struct {
	Type         string   `json:"type"`
	Grouping     string   `json:"grouping"`
	SeriesColors []string `json:"seriesColors"`
	DataLabels   bool     `json:"dataLabels"`
	LabelColor   string   `json:"labelColor"`
	Legend       string   `json:"legend"`
	Gridlines    bool     `json:"gridlines"`
	HolePct      int      `json:"holePct"`
}

var hexColor = regexp.MustCompile(`^#?[0-9A-Fa-f]{6}$`)

func color(s string) rawdeck.Color {
	if !hexColor.MatchString(s) {
		return rawdeck.Color{}
	}
	return rawdeck.Color{Hex: "#" + strings.ToUpper(strings.TrimPrefix(s, "#"))}
}

func browserMime(m string) bool {
	return m == "image/png" || m == "image/jpeg" || m == "image/webp" || m == "image/gif"
}

// Describe returns the chart style of one image, or nil if it is not a chart.
func (c *ChartReader) Describe(ctx context.Context, data []byte, mime string) (*rawdeck.Chart, error) {
	raw, _, err := c.Client.GenerateJSON(ctx, llm.Request{Model: c.Model, Prompt: prompt, Schema: schema, Images: []llm.Image{{Mime: mime, Data: data}}})
	if err != nil {
		return nil, err
	}
	var a answer
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("chart vision: %w", err)
	}
	if a.Type == "" || a.Type == "none" {
		return nil, nil
	}
	ch := &rawdeck.Chart{Type: a.Type, Grouping: a.Grouping, DataLabels: a.DataLabels, Gridlines: a.Gridlines, HolePct: a.HolePct}
	if a.Legend != "none" {
		ch.Legend = a.Legend
	}
	if ch.DataLabels {
		ch.LabelColor = color(a.LabelColor)
	}
	for _, s := range a.SeriesColors {
		if col := color(s); !col.IsZero() && len(ch.Series) < 12 {
			ch.Series = append(ch.Series, col)
		}
	}
	return ch, nil
}

// Annotate fills Chart on picture charts in d. Charts with no decodable image
// inherit the style of an annotated chart at the same position.
func (c *ChartReader) Annotate(ctx context.Context, d *rawdeck.Deck) (int, error) {
	max := c.MaxCalls
	if max <= 0 {
		max = 6
	}
	byRef := map[string]*rawdeck.Chart{}
	var done []*rawdeck.Element
	var pending []*rawdeck.Element
	var firstErr error
	calls := 0
	for _, s := range d.Slides {
		for _, el := range s.Elements {
			if el.Role != rawdeck.RoleChart || el.Chart != nil {
				continue
			}
			a, ok := d.Assets[el.ImageRef]
			if el.ImageRef == "" || !ok || !browserMime(a.Mime) {
				pending = append(pending, el)
				continue
			}
			ch, seen := byRef[el.ImageRef]
			if !seen {
				if calls >= max {
					pending = append(pending, el)
					continue
				}
				calls++
				var err error
				if ch, err = c.Describe(ctx, a.Data, a.Mime); err != nil {
					if firstErr == nil {
						firstErr = err
					}
					continue
				}
				byRef[el.ImageRef] = ch
			}
			if ch != nil {
				cp := *ch
				el.Chart = &cp
				done = append(done, el)
			}
		}
	}
	for _, el := range pending {
		for _, o := range done {
			if o.Box.IoU(el.Box) > 0.9 {
				cp := *o.Chart
				el.Chart = &cp
				break
			}
		}
	}
	return len(done), firstErr
}
