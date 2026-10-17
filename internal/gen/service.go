package gen

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/ashutoshm97/curly-engine/internal/content"
	"github.com/ashutoshm97/curly-engine/internal/jobs"
	"github.com/ashutoshm97/curly-engine/internal/layout"
	"github.com/ashutoshm97/curly-engine/internal/llm"
	"github.com/ashutoshm97/curly-engine/internal/profile"
	"github.com/ashutoshm97/curly-engine/internal/render"
	"github.com/ashutoshm97/curly-engine/internal/scene"
	"github.com/ashutoshm97/curly-engine/internal/store"
)

const JobType = "generate"

type Payload struct {
	DeckID string `json:"deckId"`
	Slides int    `json:"slides"`
}

type Result struct {
	DeckID string    `json:"deckId"`
	Slides int       `json:"slides"`
	Usage  llm.Usage `json:"usage"`
	Ms     int64     `json:"ms"`
}

// SlideEvent is streamed over SSE as each slide is composed.
type SlideEvent struct {
	Type  string `json:"type"` // outline | slide
	Index int    `json:"index,omitempty"`
	Total int    `json:"total,omitempty"`
	Title string `json:"title,omitempty"`
	SVG   string `json:"svg,omitempty"`
}

type Service struct {
	Store       *store.Store
	Writer      Writer
	Concurrency int
	AssetURL    render.AssetURL
}

func (s *Service) Handle(ctx context.Context, j *store.Job, rep jobs.Reporter) (any, error) {
	start := time.Now()
	var pl Payload
	if err := json.Unmarshal([]byte(j.Payload), &pl); err != nil {
		return nil, jobs.Permanent{Err: err}
	}
	d, err := s.Store.GetDeck(ctx, pl.DeckID)
	if err != nil {
		return nil, jobs.Permanent{Err: err}
	}
	x, _, err := s.Store.ProfileXML(ctx, d.ProfileID, d.ProfileVersion)
	if err != nil {
		return nil, jobs.Permanent{Err: err}
	}
	p, err := profile.Parse(x)
	if err != nil {
		return nil, jobs.Permanent{Err: err}
	}
	n := min(max(pl.Slides, 1), 30)
	rep.Stage("plan")
	o, usage, err := s.Writer.Plan(ctx, p, d.Prompt, n)
	if err != nil {
		return nil, err
	}
	Sanitize(p, o, n)
	rep.Data(SlideEvent{Type: "outline", Total: len(o.Slides), Title: o.Title})
	rep.Stage("fill")
	sc := &scene.Deck{ProfileID: p.ID, Title: o.Title, WidthPt: p.WidthPt, HeightPt: p.HeightPt, Slides: make([]*scene.Slide, len(o.Slides))}
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(max(s.Concurrency, 1))
	for i := range o.Slides {
		g.Go(func() error {
			c, u, err := s.Writer.Fill(gctx, p, o, i, d.Prompt)
			if err != nil {
				return fmt.Errorf("slide %d: %w", i+1, err)
			}
			sl := layout.Compose(p, c, i+1)
			mu.Lock()
			sc.Slides[i] = sl
			usage = add(usage, u)
			mu.Unlock()
			rep.Data(SlideEvent{Type: "slide", Index: i + 1, Total: len(o.Slides), SVG: render.SVG(sl, p.WidthPt, p.HeightPt, s.AssetURL)})
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	b, _ := json.Marshal(sc)
	d.Title, d.Status, d.Scene = o.Title, "ready", b
	if err := s.Store.SaveDeck(ctx, d); err != nil {
		return nil, err
	}
	return Result{DeckID: d.ID, Slides: len(sc.Slides), Usage: usage, Ms: time.Since(start).Milliseconds()}, nil
}

func add(a, b llm.Usage) llm.Usage {
	a.InputTokens += b.InputTokens
	a.OutputTokens += b.OutputTokens
	a.CachedTokens += b.CachedTokens
	return a
}

// Sanitize forces the outline to n slides with valid layout ids.
func Sanitize(p *profile.StyleProfile, o *Outline, n int) {
	fallback := p.Layouts[0].ID
	if c := byKind(p, "content"); len(c) > 0 {
		fallback = c[0].ID
	}
	for i := range o.Slides {
		if p.Layout(o.Slides[i].Layout) == nil {
			o.Slides[i].Layout = fallback
		}
	}
	for len(o.Slides) < n {
		o.Slides = append(o.Slides, OutlineItem{Layout: fallback, Title: sections[len(o.Slides)%len(sections)], Intent: "content"})
	}
	o.Slides = o.Slides[:n]
	if o.Title == "" {
		o.Title = "Untitled deck"
	}
}

// Preview composes a layout with its extracted example content.
func Preview(p *profile.StyleProfile, l *profile.Layout) *scene.Slide {
	c := content.Slide{Layout: l.ID}
	for _, s := range l.Slots {
		ex := s.Example
		switch {
		case s.Type == "table":
			c.Table = &content.Table{Header: []string{"Column A", "Column B", "Column C"}, Rows: [][]string{{"Row 1", "12", "34%"}, {"Row 2", "56", "78%"}, {"Row 3", "90", "12%"}}}
		case s.Type == "image":
			c.ImagePrompt = "Image"
		case s.Name == "title":
			c.Title = or(ex, l.Name)
		case s.Name == "subtitle":
			c.Subtitle = or(ex, "Subtitle")
		case s.Type == "list":
			c.Bullets = []string{"First point", "Second point", "Third point"}
		default:
			c.Body = or(ex, "Body text")
		}
	}
	for range l.Repeats {
		c.Items = []content.Item{{Value: "42%", Label: "Metric"}, {Value: "3.1x", Label: "Metric"}, {Value: "$8M", Label: "Metric"}}
	}
	return layout.Compose(p, c, 1)
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
