package gen

import (
	"context"
	"encoding/json"
	"errors"
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
	Images      *Images // nil: image slots keep placeholders
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
	p, err := s.profile(ctx, d)
	if err != nil {
		return nil, jobs.Permanent{Err: err}
	}
	rep.Stage("plan")
	sc, usage, err := Generate(ctx, p, s.Writer, d.Prompt, pl.Slides, s.Concurrency, Hooks{
		Decorate: s.Images.Fill,
		Outline: func(o *Outline) {
			rep.Data(SlideEvent{Type: "outline", Total: len(o.Slides), Title: o.Title})
			rep.Stage("fill")
		},
		Slide: func(sl *scene.Slide, total int) {
			rep.Data(SlideEvent{Type: "slide", Index: sl.Index, Total: total, SVG: render.SVG(sl, p.WidthPt, p.HeightPt, s.AssetURL)})
		},
	})
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(sc)
	d.Title, d.Status, d.Scene = sc.Title, "ready", b
	if err := s.Store.SaveDeck(ctx, d); err != nil {
		return nil, err
	}
	return Result{DeckID: d.ID, Slides: len(sc.Slides), Usage: usage, Ms: time.Since(start).Milliseconds()}, nil
}

func (s *Service) profile(ctx context.Context, d *store.Deck) (*profile.StyleProfile, error) {
	x, _, err := s.Store.ProfileXML(ctx, d.ProfileID, d.ProfileVersion)
	if err != nil {
		return nil, err
	}
	return profile.Parse(x)
}

// RegenerateSlide rewrites slide n (1-based) of a ready deck with one Fill call and persists it.
func (s *Service) RegenerateSlide(ctx context.Context, deckID string, n int, hint string) (*scene.Slide, *profile.StyleProfile, llm.Usage, error) {
	d, err := s.Store.GetDeck(ctx, deckID)
	if err != nil {
		return nil, nil, llm.Usage{}, err
	}
	sc := &scene.Deck{}
	if err := json.Unmarshal(d.Scene, sc); err != nil || n < 1 || n > len(sc.Slides) {
		return nil, nil, llm.Usage{}, ErrNoSlide
	}
	p, err := s.profile(ctx, d)
	if err != nil {
		return nil, nil, llm.Usage{}, err
	}
	sl, u, err := Regenerate(ctx, p, s.Writer, sc, d.Prompt, n, hint)
	if err != nil {
		return nil, nil, u, err
	}
	s.Images.Fill(ctx, sl)
	b, _ := json.Marshal(sc)
	d.Scene = b
	return sl, p, u, s.Store.SaveDeck(ctx, d)
}

var ErrNoSlide = errors.New("no such slide")

// Hooks receive progress while a deck is generated.
type Hooks struct {
	Outline  func(o *Outline)
	Decorate func(ctx context.Context, s *scene.Slide) // e.g. image generation, before Slide
	Slide    func(s *scene.Slide, total int)
}

// Generate plans n slides, fills them concurrently and composes each into a scene slide.
func Generate(ctx context.Context, p *profile.StyleProfile, w Writer, prompt string, n, concurrency int, h Hooks) (*scene.Deck, llm.Usage, error) {
	n = min(max(n, 1), 30)
	o, usage, err := w.Plan(ctx, p, prompt, n)
	if err != nil {
		return nil, usage, err
	}
	Sanitize(p, o, n)
	if h.Outline != nil {
		h.Outline(o)
	}
	sc := &scene.Deck{ProfileID: p.ID, Title: o.Title, WidthPt: p.WidthPt, HeightPt: p.HeightPt, Slides: make([]*scene.Slide, len(o.Slides))}
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(max(concurrency, 1))
	for i := range o.Slides {
		g.Go(func() error {
			c, u, err := w.Fill(gctx, p, o, i, prompt)
			if err != nil {
				return fmt.Errorf("slide %d: %w", i+1, err)
			}
			sl := compose(p, o, i, c)
			if h.Decorate != nil {
				h.Decorate(gctx, sl)
			}
			mu.Lock()
			sc.Slides[i] = sl
			usage = add(usage, u)
			mu.Unlock()
			if h.Slide != nil {
				h.Slide(sl, len(o.Slides))
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, usage, err
	}
	return sc, usage, nil
}

// Regenerate replaces slide n in sc. The outline is rebuilt from the stored
// slides; a variation suffix keeps the LLM cache from returning the old answer.
func Regenerate(ctx context.Context, p *profile.StyleProfile, w Writer, sc *scene.Deck, prompt string, n int, hint string) (*scene.Slide, llm.Usage, error) {
	o := &Outline{Title: sc.Title}
	for _, s := range sc.Slides {
		o.Slides = append(o.Slides, OutlineItem{Layout: s.Layout, Title: s.Title, Intent: s.Intent})
	}
	Sanitize(p, o, len(o.Slides))
	if hint != "" {
		o.Slides[n-1].Intent += ". Revision request: " + hint
	}
	c, u, err := w.Fill(ctx, p, o, n-1, fmt.Sprintf("%s\n(alternative version %d)", prompt, time.Now().UnixNano()))
	if err != nil {
		return nil, u, err
	}
	sl := compose(p, o, n-1, c)
	sl.Intent = sc.Slides[n-1].Intent
	sc.Slides[n-1] = sl
	return sl, u, nil
}

func compose(p *profile.StyleProfile, o *Outline, i int, c content.Slide) *scene.Slide {
	content.Clean(&c)
	sl := layout.Compose(p, c, i+1)
	sl.Title, sl.Intent = c.Title, o.Slides[i].Intent
	return sl
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
