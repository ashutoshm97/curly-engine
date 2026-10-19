package gen

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/ashutoshm97/curly-engine/internal/content"
	"github.com/ashutoshm97/curly-engine/internal/llm"
	"github.com/ashutoshm97/curly-engine/internal/profile"
)

// Offline is a deterministic Writer used when no Gemini key is configured.
type Offline struct{}

var sections = []string{"Context", "The problem", "Our approach", "Key results", "How it works", "Roadmap", "Risks and mitigations", "Team", "Next steps"}

func topic(prompt string) string {
	t := strings.TrimSpace(strings.Split(prompt, "\n")[0])
	if r := []rune(t); len(r) > 60 {
		t = string(r[:60])
		if i := strings.LastIndex(t, " "); i > 20 {
			t = t[:i]
		}
	}
	if t == "" {
		return "Untitled deck"
	}
	r := []rune(t)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func byKind(p *profile.StyleProfile, kinds ...string) []profile.Layout {
	var out []profile.Layout
	for _, k := range kinds {
		for _, l := range p.Layouts {
			if l.Kind == k {
				out = append(out, l)
			}
		}
	}
	return out
}

func (Offline) Plan(_ context.Context, p *profile.StyleProfile, prompt string, n int) (*Outline, llm.Usage, error) {
	o := &Outline{Title: topic(prompt)}
	first := append(byKind(p, "title"), p.Layouts[0])[0]
	body := byKind(p, "content", "data", "section")
	if len(body) == 0 {
		body = p.Layouts
	}
	last := append(byKind(p, "closing", "title"), first)[0]
	for i := 0; i < n; i++ {
		switch {
		case i == 0:
			o.Slides = append(o.Slides, OutlineItem{Layout: first.ID, Title: o.Title, Intent: "opening"})
		case i == n-1 && n > 2:
			o.Slides = append(o.Slides, OutlineItem{Layout: last.ID, Title: "Thank you", Intent: "closing"})
		default:
			l := body[(i-1)%len(body)]
			o.Slides = append(o.Slides, OutlineItem{Layout: l.ID, Title: sections[(i-1)%len(sections)], Intent: l.Kind})
		}
	}
	return o, llm.Usage{}, nil
}

func (Offline) Fill(_ context.Context, p *profile.StyleProfile, o *Outline, i int, prompt string) (content.Slide, llm.Usage, error) {
	it := o.Slides[i]
	s := content.Slide{Layout: it.Layout, Title: it.Title}
	t := strings.ToLower(o.Title)
	switch {
	case i == 0:
		s.Subtitle = "Overview and recommendations"
	case it.Intent == "closing":
		s.Subtitle = "Questions and discussion"
	}
	l := p.Layout(it.Layout)
	if l == nil {
		return s, llm.Usage{}, nil
	}
	for _, sl := range l.Slots {
		switch sl.Type {
		case "list":
			s.Bullets = []string{
				fmt.Sprintf("Why %s matters now", t),
				"Three priorities for the next quarter",
				"Measurable outcomes and owners",
				"Dependencies and open questions",
			}
		case "text":
			if !strings.HasPrefix(sl.Name, "title") && !strings.HasPrefix(sl.Name, "subtitle") {
				s.Body = fmt.Sprintf("A short summary of %s for this section.", t)
			}
		case "table":
			s.Table = &content.Table{Header: []string{"Area", "Today", "Target"}, Rows: [][]string{
				{"Adoption", "35%", "60%"}, {"Cycle time", "12 days", "5 days"}, {"Cost per unit", "$4.10", "$2.80"}, {"NPS", "31", "45"}}}
		case "image":
			s.ImagePrompt = "Photo illustrating " + t
		}
	}
	if len(l.Repeats) > 0 {
		s.Items = []content.Item{{Value: "38%", Label: "Growth year over year"}, {Value: "2.4x", Label: "Faster delivery"}, {Value: "120k", Label: "Active users"}}
	}
	return s, llm.Usage{}, nil
}
