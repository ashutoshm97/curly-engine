// Package gen produces deck content (LLM or offline) and composes it into scenes.
package gen

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ashutoshm97/curly-engine/internal/content"
	"github.com/ashutoshm97/curly-engine/internal/llm"
	"github.com/ashutoshm97/curly-engine/internal/profile"
)

// Outline is the planner output: one entry per slide.
type Outline struct {
	Title  string        `json:"title"`
	Slides []OutlineItem `json:"slides"`
}

type OutlineItem struct {
	Layout string `json:"layout"`
	Title  string `json:"title"`
	Intent string `json:"intent"`
}

type Writer interface {
	Plan(ctx context.Context, p *profile.StyleProfile, prompt string, n int) (*Outline, llm.Usage, error)
	Fill(ctx context.Context, p *profile.StyleProfile, o *Outline, i int, prompt string) (content.Slide, llm.Usage, error)
}

// Catalog is the compact, style-free layout description sent to the LLM (stable prefix → implicit caching).
func Catalog(p *profile.StyleProfile) string {
	var b strings.Builder
	for _, l := range p.Layouts {
		if l.Hidden {
			continue
		}
		fmt.Fprintf(&b, "%s [%s] %s:", l.ID, l.Kind, l.Name)
		for _, s := range l.Slots {
			fmt.Fprintf(&b, " %s(%s", s.Name, s.Type)
			if s.MaxChars > 0 {
				fmt.Fprintf(&b, " ≤%d chars", s.MaxChars)
			}
			if s.MaxRows > 0 {
				fmt.Fprintf(&b, " ≤%d rows ≤%d cols", s.MaxRows, s.MaxCols)
			}
			if s.Type == "chart" {
				if c := p.Component(s.Component); c != nil && c.Chart != nil {
					fmt.Fprintf(&b, " %s", c.Chart.Type)
					if c.Chart.Type == "pie" || c.Chart.Type == "doughnut" {
						b.WriteString(" 1 series")
					}
				}
				b.WriteString(" ≤12 categories ≤6 series")
			}
			b.WriteString(")")
		}
		for _, r := range l.Repeats {
			fmt.Fprintf(&b, " items(%d-%d × value+label)", r.Min, r.Max)
		}
		b.WriteString("\n")
	}
	return b.String()
}

var outlineSchema = map[string]any{
	"type": "OBJECT",
	"properties": map[string]any{
		"title": map[string]any{"type": "STRING"},
		"slides": map[string]any{"type": "ARRAY", "items": map[string]any{
			"type": "OBJECT",
			"properties": map[string]any{
				"layout": map[string]any{"type": "STRING"},
				"title":  map[string]any{"type": "STRING"},
				"intent": map[string]any{"type": "STRING"},
			},
			"required": []string{"layout", "title", "intent"},
		}},
	},
	"required": []string{"title", "slides"},
}

var slideSchema = map[string]any{
	"type": "OBJECT",
	"properties": map[string]any{
		"title":       map[string]any{"type": "STRING"},
		"subtitle":    map[string]any{"type": "STRING"},
		"body":        map[string]any{"type": "STRING"},
		"bullets":     map[string]any{"type": "ARRAY", "items": map[string]any{"type": "STRING"}},
		"items":       map[string]any{"type": "ARRAY", "items": map[string]any{"type": "OBJECT", "properties": map[string]any{"value": map[string]any{"type": "STRING"}, "label": map[string]any{"type": "STRING"}}, "required": []string{"value", "label"}}},
		"table":       map[string]any{"type": "OBJECT", "properties": map[string]any{"header": map[string]any{"type": "ARRAY", "items": map[string]any{"type": "STRING"}}, "rows": map[string]any{"type": "ARRAY", "items": map[string]any{"type": "ARRAY", "items": map[string]any{"type": "STRING"}}}}},
		"imagePrompt": map[string]any{"type": "STRING"},
		"chart": map[string]any{"type": "OBJECT", "properties": map[string]any{
			"categories": map[string]any{"type": "ARRAY", "items": map[string]any{"type": "STRING"}},
			"series": map[string]any{"type": "ARRAY", "items": map[string]any{"type": "OBJECT", "properties": map[string]any{
				"name": map[string]any{"type": "STRING"}, "values": map[string]any{"type": "ARRAY", "items": map[string]any{"type": "NUMBER"}}}, "required": []string{"name", "values"}}}},
			"required": []string{"categories", "series"}},
		"notes": map[string]any{"type": "STRING"},
	},
	"required": []string{"title"},
}

const system = "You write concise, specific presentation content. Styling is handled elsewhere: never describe colors, fonts or positions. Respect character limits. Return only JSON matching the schema."

type LLMWriter struct {
	Client    llm.Client
	PlanModel string
	FillModel string
}

func (w *LLMWriter) Plan(ctx context.Context, p *profile.StyleProfile, prompt string, n int) (*Outline, llm.Usage, error) {
	msg := fmt.Sprintf("LAYOUTS:\n%s\nPick a layout id per slide. Start with a [title] layout; use [data] layouts for numbers/tables; end with a closing or title layout if available.\n\nWrite an outline of exactly %d slides for:\n%s", Catalog(p), n, prompt)
	raw, u, err := w.Client.GenerateJSON(ctx, llm.Request{Model: w.PlanModel, System: system, Prompt: msg, Schema: outlineSchema, Temperature: 0.4})
	if err != nil {
		return nil, u, err
	}
	var o Outline
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, u, fmt.Errorf("plan: %w", err)
	}
	return &o, u, nil
}

func (w *LLMWriter) Fill(ctx context.Context, p *profile.StyleProfile, o *Outline, i int, prompt string) (content.Slide, llm.Usage, error) {
	it := o.Slides[i]
	var titles []string
	for _, s := range o.Slides {
		titles = append(titles, s.Title)
	}
	msg := fmt.Sprintf("LAYOUTS:\n%s\nDeck: %q (%d slides: %s)\nTopic: %s\n\nWrite slide %d using layout %s. Title: %q. Intent: %s.\nFill only the fields that layout's slots need (title/subtitle/body/bullets/items/table/chart/imagePrompt); keep within the char limits. A chart is numbers only: categories plus series of values, one value per category. Each bullet is one short point (no bullet characters or line breaks).",
		Catalog(p), o.Title, len(o.Slides), strings.Join(titles, " | "), prompt, i+1, it.Layout, it.Title, it.Intent)
	raw, u, err := w.Client.GenerateJSON(ctx, llm.Request{Model: w.FillModel, System: system, Prompt: msg, Schema: slideSchema, Temperature: 0.7})
	if err != nil {
		return content.Slide{}, u, err
	}
	var s content.Slide
	if err := json.Unmarshal(raw, &s); err != nil {
		return content.Slide{}, u, fmt.Errorf("fill: %w", err)
	}
	s.Layout = it.Layout
	if s.Title == "" {
		s.Title = it.Title
	}
	return s, u, nil
}
