// Package enrich runs one LLM pass over a freshly extracted profile to name
// layouts, flag noise layouts, and add components/layouts the source lacked
// (table, chart, quote, agenda, timeline, dark and card-count variants).
// The LLM only picks names and design-token references; Go builds the
// geometry, validates every token and marks additions origin="llm".
package enrich

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/ashutoshm97/curly-engine/internal/llm"
	"github.com/ashutoshm97/curly-engine/internal/profile"
)

type Enricher struct {
	Client llm.Client
	Model  string
}

type layoutHint struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Hide         bool   `json:"hide"`
	HideReason   string `json:"hideReason"`
	SimilarTo    string `json:"similarTo"`
	DarkVariant  bool   `json:"darkVariant"`
	CardVariants []int  `json:"cardVariants"`
	DrawnTable   bool   `json:"drawnTable"`
}

type answer struct {
	Layouts    []layoutHint `json:"layouts"`
	Components []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"components"`
	Table struct {
		HeaderFill, HeaderText, BodyFill, BandFill, BodyText, Border string
	} `json:"table"`
	ChartColors []string `json:"chartColors"`
	ChartType   string   `json:"chartType"`
	Quote       struct {
		Color, Step string
		Italic      bool
	} `json:"quote"`
	Accent     string `json:"accent"`
	AccentText string `json:"accentText"`
}

func str() map[string]any { return map[string]any{"type": "STRING"} }

var schema = map[string]any{
	"type": "OBJECT",
	"properties": map[string]any{
		"layouts": map[string]any{"type": "ARRAY", "items": map[string]any{"type": "OBJECT", "properties": map[string]any{
			"id": str(), "name": str(), "description": str(), "hide": map[string]any{"type": "BOOLEAN"}, "hideReason": str(), "similarTo": str(),
			"darkVariant": map[string]any{"type": "BOOLEAN"}, "cardVariants": map[string]any{"type": "ARRAY", "items": map[string]any{"type": "INTEGER"}},
			"drawnTable": map[string]any{"type": "BOOLEAN"}}, "required": []string{"id", "name"}}},
		"components":  map[string]any{"type": "ARRAY", "items": map[string]any{"type": "OBJECT", "properties": map[string]any{"id": str(), "name": str()}, "required": []string{"id", "name"}}},
		"table":       map[string]any{"type": "OBJECT", "properties": map[string]any{"HeaderFill": str(), "HeaderText": str(), "BodyFill": str(), "BandFill": str(), "BodyText": str(), "Border": str()}},
		"chartColors": map[string]any{"type": "ARRAY", "items": str()},
		"chartType":   map[string]any{"type": "STRING", "enum": []string{"col", "bar", "line", "pie", "doughnut"}},
		"quote":       map[string]any{"type": "OBJECT", "properties": map[string]any{"Color": str(), "Step": str(), "Italic": map[string]any{"type": "BOOLEAN"}}},
		"accent":      str(),
		"accentText":  str(),
	},
	"required": []string{"layouts"},
}

const system = "You are a presentation design-system editor. You only reference the design tokens you are given (color names, type-scale step names); never invent hex values, fonts or positions."

func summary(p *profile.StyleProfile) string {
	var b strings.Builder
	b.WriteString("COLOR TOKENS: ")
	for _, c := range p.Tokens.Colors {
		fmt.Fprintf(&b, "%s=%s ", c.Name, c.Value)
	}
	b.WriteString("\nTYPE STEPS: ")
	for _, s := range p.Tokens.TypeScale.Steps {
		fmt.Fprintf(&b, "%s=%gpt ", s.Name, s.Size)
	}
	b.WriteString("\nCOMPONENTS:\n")
	for _, c := range p.Components {
		fmt.Fprintf(&b, "%s type=%s role=%s\n", c.ID, c.Type, c.Role)
	}
	b.WriteString("LAYOUTS:\n")
	for _, l := range p.Layouts {
		fmt.Fprintf(&b, "%s kind=%s name=%q background=%s source slides=%s:", l.ID, l.Kind, l.Name, l.Background, l.SourceSlides)
		for _, s := range l.Slots {
			fmt.Fprintf(&b, " %s(%s %q)", s.Name, s.Type, truncate(s.Example, 40))
		}
		for _, r := range l.Repeats {
			fmt.Fprintf(&b, " cards(%d-%d)", r.Min, r.Max)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

const task = `
Tasks:
1. For every layout give a short human name (≤40 chars, e.g. "KPI cards ×3", "Agenda", "Section divider") and a one-sentence description of when to use it.
2. Set hide=true (with hideReason) for layouts that are noise for new decks: footnote/disclaimer/legal pages, appendix source lists. Never hide the first title layout.
3. If two layouts are near-duplicates, set similarTo on the later one to the id of the earlier one.
4. Set darkVariant=true on at most two content layouts that would look good on a dark background. For layouts with cards, list other useful card counts in cardVariants (2-5).
5. Set drawnTable=true on layouts whose grid of boxes is really a table.
6. Name every component (≤30 chars).
7. Pick tokens for components the deck may lack: table (HeaderFill, HeaderText, BodyFill, BandFill, BodyText, Border), chartColors (3-6 color tokens) and chartType, quote (Color, Step, Italic), accent (a fill token for timeline markers/agenda bullets) and accentText (text color token readable on accent).`

func (e *Enricher) Enrich(ctx context.Context, p *profile.StyleProfile) (int, error) {
	raw, _, err := e.Client.GenerateJSON(ctx, llm.Request{Model: e.Model, System: system, Prompt: summary(p) + task, Schema: schema, Temperature: 0.2})
	if err != nil {
		return 0, err
	}
	var a answer
	if err := json.Unmarshal(raw, &a); err != nil {
		return 0, fmt.Errorf("enrich: %w", err)
	}
	x, err := p.MarshalIndent()
	if err != nil {
		return 0, err
	}
	cp, err := profile.Parse(x)
	if err != nil {
		return 0, err
	}
	n := apply(cp, &a)
	if err := cp.Validate(); err != nil {
		return 0, fmt.Errorf("enrich: invalid result: %w", err)
	}
	*p = *cp
	return n, nil
}

type tokens struct {
	colors, steps map[string]bool
}

func (t tokens) color(v, def string) string {
	if t.colors[v] {
		return v
	}
	return def
}

func (t tokens) step(v, def string) string {
	if t.steps[v] {
		return v
	}
	return def
}

func newID(p *profile.StyleProfile, base string) string {
	id := base
	for i := 2; p.Component(id) != nil; i++ {
		id = fmt.Sprintf("%s%d", base, i)
	}
	return id
}

func layoutID(p *profile.StyleProfile) string {
	for i := len(p.Layouts) + 1; ; i++ {
		if id := fmt.Sprintf("L%d", i); p.Layout(id) == nil {
			return id
		}
	}
}

func apply(p *profile.StyleProfile, a *answer) int {
	t := tokens{colors: map[string]bool{}, steps: map[string]bool{}}
	for _, c := range p.Tokens.Colors {
		t.colors[c.Name] = true
	}
	for _, s := range p.Tokens.TypeScale.Steps {
		t.steps[s.Name] = true
	}
	changes := 0
	visible := 0
	for _, l := range p.Layouts {
		if l.Kind == "content" || l.Kind == "data" {
			visible++
		}
	}
	for _, h := range a.Layouts {
		l := p.Layout(h.ID)
		if l == nil {
			continue
		}
		if h.Name != "" {
			l.Name, changes = truncate(h.Name, 60), changes+1
		}
		if h.Description != "" {
			l.Description = truncate(h.Description, 200)
		}
		if h.DrawnTable {
			l.Description = strings.TrimSpace(l.Description + " The source draws a table with boxes.")
		}
		if h.Hide && l.ID != p.Layouts[0].ID && visible > 2 {
			l.Hidden, l.HiddenReason, changes = true, truncate(h.HideReason, 120), changes+1
			if l.Kind == "content" || l.Kind == "data" {
				visible--
			}
		}
		if h.SimilarTo != "" && h.SimilarTo != l.ID && p.Layout(h.SimilarTo) != nil {
			l.SimilarTo = h.SimilarTo
		}
	}
	for _, c := range a.Components {
		if comp := p.Component(c.ID); comp != nil && c.Name != "" {
			comp.Name = truncate(c.Name, 40)
		}
	}
	// Base geometry: the first visible content layout with a title slot.
	var title *profile.Slot
	region := profile.Box{X: 0.06, Y: 0.25, W: 0.88, H: 0.65}
	for _, l := range p.Layouts {
		if l.Hidden || l.Kind != "content" {
			continue
		}
		for i := range l.Slots {
			if l.Slots[i].Name == "title" {
				s := l.Slots[i]
				title = &s
				if top := s.Box.Y + s.Box.H + 0.04; top < 0.5 {
					region = profile.Box{X: s.Box.X, Y: top, W: max(s.Box.W, 0.8), H: 0.92 - top}
				}
			}
		}
		if title != nil {
			break
		}
	}
	addLayout := func(name, kind string, slots []profile.Slot, reps []profile.Repeat) {
		l := profile.Layout{ID: layoutID(p), Name: name, Kind: kind, Origin: "llm", Background: "lt1", Description: name + " (AI-created from this deck's tokens).", Repeats: reps}
		if title != nil {
			l.Slots = append(l.Slots, *title)
		}
		l.Slots = append(l.Slots, slots...)
		p.Layouts = append(p.Layouts, l)
		changes++
	}
	hasType := func(typ string) bool {
		return slices.ContainsFunc(p.Components, func(c profile.Component) bool { return c.Type == typ })
	}
	hasLayout := func(word string) bool {
		return slices.ContainsFunc(p.Layouts, func(l profile.Layout) bool { return strings.Contains(strings.ToLower(l.Name), word) })
	}
	accent := t.color(a.Accent, t.color("accent1", "dk1"))
	accentText := t.color(a.AccentText, "lt1")
	if !hasType("table") {
		tb := a.Table
		c := profile.Component{ID: newID(p, "C.table"), Type: "table", Role: "table", Name: "Data table", Origin: "llm",
			Header: &profile.TableHeader{Fill: t.color(tb.HeaderFill, accent), Text: t.color(tb.HeaderText, accentText), Bold: true, Step: "caption"},
			Body:   &profile.TableBody{Fill: t.color(tb.BodyFill, "lt1"), BandRows: true, BandFill: t.color(tb.BandFill, "lt1"), Text: t.color(tb.BodyText, "dk1"), Step: "caption"},
			Cell:   &profile.TableCell{Border: t.color(tb.Border, "")}}
		c.Header.Step, c.Body.Step = t.step("caption", c.Header.Step), t.step("caption", c.Body.Step)
		p.Components = append(p.Components, c)
		addLayout("Title + table", "data", []profile.Slot{{Name: "table", Type: "table", Component: c.ID, Box: region, MaxRows: 8, MaxCols: 5}}, nil)
	}
	if !hasType("chart") {
		spec := &profile.ChartSpec{Type: "col", Grouping: "clustered", Legend: "b", Axis: &profile.ChartAxis{Gridlines: true}}
		if slices.Contains([]string{"col", "bar", "line", "pie", "doughnut"}, a.ChartType) {
			spec.Type = a.ChartType
		}
		for _, c := range a.ChartColors {
			if t.colors[c] && len(spec.Series) < 6 {
				spec.Series = append(spec.Series, profile.SeriesStyle{Color: c})
			}
		}
		if len(spec.Series) == 0 {
			spec.Series = []profile.SeriesStyle{{Color: accent}}
		}
		c := profile.Component{ID: newID(p, "C.chart"), Type: "chart", Role: "chart", Name: "Chart", Origin: "llm", Chart: spec}
		p.Components = append(p.Components, c)
		addLayout("Title + chart", "data", []profile.Slot{{Name: "chart", Type: "chart", Component: c.ID, Box: region}}, nil)
	}
	if !hasLayout("quote") {
		q := a.Quote
		c := profile.Component{ID: newID(p, "C.quote"), Type: "text", Role: "body", Name: "Pull quote", Origin: "llm",
			Text: &profile.TextSpec{Font: "heading", Step: t.step(q.Step, t.step("h2", "body")), Color: t.color(q.Color, "dk1"), Align: "ctr", Italic: q.Italic}}
		attr := profile.Component{ID: newID(p, "C.attribution"), Type: "text", Role: "subtitle", Name: "Quote attribution", Origin: "llm",
			Text: &profile.TextSpec{Font: "body", Step: t.step("caption", "body"), Color: t.color("dk2", "dk1"), Align: "ctr"}}
		p.Components = append(p.Components, c, attr)
		saved := title
		title = nil
		addLayout("Quote", "content", []profile.Slot{
			{Name: "body", Type: "text", Component: c.ID, Box: profile.Box{X: 0.12, Y: 0.28, W: 0.76, H: 0.32}, MaxChars: 180},
			{Name: "subtitle", Type: "text", Component: attr.ID, Box: profile.Box{X: 0.12, Y: 0.64, W: 0.76, H: 0.08}, MaxChars: 60}}, nil)
		title = saved
	}
	if !hasLayout("agenda") {
		c := profile.Component{ID: newID(p, "C.agenda"), Type: "list", Role: "body", Name: "Agenda list", Origin: "llm",
			Text:    &profile.TextSpec{Font: "body", Step: t.step("h2", "body"), Color: "dk1", Align: "l"},
			Marker:  &profile.Marker{Glyph: "■", Color: accent},
			Bullets: &profile.Bullets{Levels: []profile.BulletLevel{{N: 1, Glyph: "■", Color: accent, SpaceBeforePt: 10}}}}
		p.Components = append(p.Components, c)
		addLayout("Agenda", "content", []profile.Slot{{Name: "body", Type: "list", Component: c.ID, Box: region, MaxChars: 300}}, nil)
	}
	if !hasLayout("timeline") {
		c := profile.Component{ID: newID(p, "C.milestone"), Type: "group", Role: "card", Name: "Timeline milestone", Origin: "llm",
			Shape: &profile.ShapeSpec{Kind: "roundRect", Fill: accent, Radius: 6},
			Slots: []profile.ComponentSlot{
				{Name: "value", Box: profile.Box{X: 0.08, Y: 0.12, W: 0.84, H: 0.35}, Text: &profile.TextSpec{Font: "heading", Step: t.step("h2", "body"), Color: accentText, Align: "l", Bold: true}},
				{Name: "label", Box: profile.Box{X: 0.08, Y: 0.52, W: 0.84, H: 0.4}, Text: &profile.TextSpec{Font: "body", Step: t.step("caption", "body"), Color: accentText, Align: "l"}}}}
		p.Components = append(p.Components, c)
		addLayout("Timeline", "content", nil, []profile.Repeat{{Name: "items", Component: c.ID, Min: 3, Max: 5, Direction: "row", Gap: 0.02,
			Region: profile.Box{X: region.X, Y: region.Y + region.H*0.25, W: region.W, H: region.H * 0.4}}})
	}
	variants := 0
	for _, h := range a.Layouts {
		src := p.Layout(h.ID)
		if src == nil || src.Hidden || src.Origin == "llm" {
			continue
		}
		if h.DarkVariant && variants < 3 && t.colors["dk1"] {
			v := dark(p, *src)
			v.ID = layoutID(p)
			p.Layouts = append(p.Layouts, v)
			variants++
			changes++
		}
		for _, n := range h.CardVariants {
			src = p.Layout(h.ID)
			if variants >= 3 || n < 2 || n > 5 || len(src.Repeats) == 0 || (n >= src.Repeats[0].Min && n <= src.Repeats[0].Max) {
				continue
			}
			v := *src
			v.ID, v.Name, v.Origin, v.VariantOf = layoutID(p), fmt.Sprintf("%s ×%d", src.Name, n), "llm", src.ID
			v.Repeats = slices.Clone(src.Repeats)
			v.Repeats[0].Min, v.Repeats[0].Max = n, n
			p.Layouts = append(p.Layouts, v)
			variants++
			changes++
		}
	}
	return changes
}

// dark copies a layout onto a dk1 background with light-text component twins.
func dark(p *profile.StyleProfile, src profile.Layout) profile.Layout {
	v := src
	v.Name, v.Background, v.Origin, v.VariantOf, v.SimilarTo = src.Name+" (dark)", "dk1", "llm", src.ID, ""
	v.Slots = slices.Clone(src.Slots)
	v.Decor = nil
	for i, s := range v.Slots {
		c := p.Component(s.Component)
		if c == nil || c.Text == nil || !strings.HasPrefix(c.Text.Color, "dk") {
			continue
		}
		twin := *c
		ts := *c.Text
		ts.Color = "lt1"
		twin.Text, twin.ID, twin.Origin, twin.Name = &ts, newID(p, c.ID+".dark"), "llm", strings.TrimSpace(c.Name+" (dark)")
		p.Components = append(p.Components, twin)
		v.Slots[i].Component = twin.ID
	}
	return v
}
