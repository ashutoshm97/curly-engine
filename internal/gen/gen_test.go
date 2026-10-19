package gen

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ashutoshm97/curly-engine/internal/llm"
	"github.com/ashutoshm97/curly-engine/internal/normalize"
	"github.com/ashutoshm97/curly-engine/internal/ooxml"
	"github.com/ashutoshm97/curly-engine/internal/profile"
	"github.com/ashutoshm97/curly-engine/internal/scene"
)

func acme(t *testing.T) *profile.StyleProfile {
	t.Helper()
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

func TestGenerateOffline(t *testing.T) {
	p := acme(t)
	var outlines, slides atomic.Int32
	sc, _, err := Generate(context.Background(), p, Offline{}, "quarterly review for a SaaS company", 6, 3, Hooks{
		Outline: func(o *Outline) { outlines.Add(1) },
		Slide:   func(s *scene.Slide, total int) { slides.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Slides) != 6 || outlines.Load() != 1 || slides.Load() != 6 {
		t.Fatalf("slides=%d outline hooks=%d slide hooks=%d", len(sc.Slides), outlines.Load(), slides.Load())
	}
	if p.Layout(sc.Slides[0].Layout).Kind != "title" {
		t.Errorf("first slide layout kind = %s", p.Layout(sc.Slides[0].Layout).Kind)
	}
	for i, s := range sc.Slides {
		if s == nil || s.Index != i+1 || s.Title == "" || len(s.Elements) == 0 {
			t.Errorf("slide %d incomplete: %+v", i+1, s)
		}
	}
}

func TestSanitize(t *testing.T) {
	p := acme(t)
	o := &Outline{Slides: []OutlineItem{{Layout: "nope", Title: "a"}, {Layout: "L1", Title: "b"}}}
	Sanitize(p, o, 4)
	if len(o.Slides) != 4 || o.Title == "" {
		t.Fatalf("outline = %+v", o)
	}
	for _, s := range o.Slides {
		if p.Layout(s.Layout) == nil {
			t.Errorf("invalid layout %q kept", s.Layout)
		}
	}
	Sanitize(p, o, 2)
	if len(o.Slides) != 2 {
		t.Errorf("not truncated: %d", len(o.Slides))
	}
}

// fakeClient answers plan and fill requests and records prompts.
type fakeClient struct{ prompts []string }

func (f *fakeClient) GenerateJSON(_ context.Context, r llm.Request) ([]byte, llm.Usage, error) {
	f.prompts = append(f.prompts, r.Prompt)
	if r.Model == "plan" {
		return []byte(`{"title":"Deck","slides":[{"layout":"L1","title":"Hello","intent":"open"},{"layout":"L2","title":"Points","intent":"list"}]}`), llm.Usage{InputTokens: 10, OutputTokens: 5}, nil
	}
	return []byte(`{"title":"Filled","bullets":["a","b"]}`), llm.Usage{InputTokens: 7, OutputTokens: 3}, nil
}

func TestLLMWriterNeverSendsStyles(t *testing.T) {
	p := acme(t)
	fc := &fakeClient{}
	w := &LLMWriter{Client: fc, PlanModel: "plan", FillModel: "fill"}
	sc, u, err := Generate(context.Background(), p, w, "topic", 2, 1, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	if u.InputTokens != 24 || u.OutputTokens != 11 {
		t.Errorf("usage = %+v", u)
	}
	if sc.Slides[1].Layout != "L2" || sc.Slides[1].Title != "Filled" {
		t.Errorf("slide 2 = %s %q", sc.Slides[1].Layout, sc.Slides[1].Title)
	}
	for _, pr := range fc.prompts {
		for _, c := range p.Tokens.Colors {
			if strings.Contains(strings.ToLower(pr), strings.ToLower(c.Value)) {
				t.Fatalf("prompt leaks color %s", c.Value)
			}
		}
		if f := p.Font("heading").Family; strings.Contains(pr, f) {
			t.Fatalf("prompt leaks font %s", f)
		}
	}

	before := len(fc.prompts)
	sl, _, err := Regenerate(context.Background(), p, w, sc, "topic", 2, "make it shorter")
	if err != nil {
		t.Fatal(err)
	}
	if len(fc.prompts) != before+1 || !strings.Contains(fc.prompts[before], "make it shorter") {
		t.Errorf("regenerate prompt = %q", fc.prompts[len(fc.prompts)-1])
	}
	if sc.Slides[1] != sl || sl.Index != 2 || sl.Layout != "L2" {
		t.Errorf("slide not replaced in place: %+v", sl)
	}
	if sc.Slides[1].Intent != "list" {
		t.Errorf("stored intent changed to %q", sc.Slides[1].Intent)
	}
}
