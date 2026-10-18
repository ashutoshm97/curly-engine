// Package layout deterministically places content into a profile layout:
// it resolves tokens, fits text to slot boxes and expands repeat regions.
package layout

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ashutoshm97/curly-engine/internal/content"
	"github.com/ashutoshm97/curly-engine/internal/profile"
	"github.com/ashutoshm97/curly-engine/internal/scene"
)

type composer struct {
	p    *profile.StyleProfile
	w, h float64
}

// Compose builds a scene slide for content c (1-based index n).
func Compose(p *profile.StyleProfile, c content.Slide, n int) *scene.Slide {
	l := p.Layout(c.Layout)
	if l == nil {
		l = &p.Layouts[0]
	}
	k := composer{p: p, w: p.WidthPt, h: p.HeightPt}
	s := &scene.Slide{Index: n, Layout: l.ID, Background: p.Color(l.Background), Notes: c.Notes}
	if s.Background == "" {
		s.Background = p.Color("lt1")
	}
	for _, d := range l.Decor {
		if d.Kind == "shape" {
			s.Elements = append(s.Elements, k.decor(d, n))
		}
	}
	for _, sl := range l.Slots {
		if bx := k.listBox(sl, l); bx != nil {
			s.Elements = append(s.Elements, bx)
		}
		if el := k.slot(sl, c); el != nil {
			s.Elements = append(s.Elements, el)
		}
	}
	for _, r := range l.Repeats {
		s.Elements = append(s.Elements, k.repeat(r, c.Items)...)
	}
	for _, d := range l.Decor {
		if d.Kind != "shape" {
			if el := k.decor(d, n); el != nil {
				s.Elements = append(s.Elements, el)
			}
		}
	}
	return s
}

func (k composer) abs(b profile.Box) (x, y, w, h float64) {
	return b.X * k.w, b.Y * k.h, b.W * k.w, b.H * k.h
}

func (k composer) text(spec *profile.TextSpec, minStep string, paras []string, bullet string, w, h float64, prep ...func(*scene.Text)) *scene.Text {
	if spec == nil {
		spec = &profile.TextSpec{Font: "body", Step: "body", Color: "dk1"}
	}
	f := k.p.Font(spec.Font)
	t := &scene.Text{Paragraphs: paras, Font: f.Family, Fallback: f.Fallback, Color: k.p.Color(spec.Color),
		Bold: spec.Bold, Italic: spec.Italic, Align: spec.Align, Bullet: bullet}
	steps := []profile.Step{k.p.Step(spec.Step)}
	for _, st := range k.p.StepsBelow(spec.Step) {
		steps = append(steps, st)
		if st.Name == minStep {
			break
		}
	}
	if minStep == "" {
		steps = steps[:min(len(steps), 3)]
	}
	for _, f := range prep {
		f(t)
	}
	Fit(t, w, h, steps)
	return t
}

func (k composer) slot(sl profile.Slot, c content.Slide) *scene.Element {
	x, y, w, h := k.abs(sl.Box)
	el := &scene.Element{Slot: sl.Name, X: x, Y: y, W: w, H: h}
	comp := k.p.Component(sl.Component)
	var spec *profile.TextSpec
	bullet := ""
	if comp != nil {
		spec = comp.Text
		el.Role = comp.Role
		if comp.Marker != nil && sl.Type == "list" {
			bullet = comp.Marker.Glyph
		}
	}
	var lvl *profile.BulletLevel
	if comp != nil && comp.Bullets != nil && len(comp.Bullets.Levels) > 0 && sl.Type == "list" {
		lvl = &comp.Bullets.Levels[0]
		bullet = lvl.Glyph
	}
	switch sl.Type {
	case "text", "list":
		paras := textFor(sl, c)
		if len(paras) == 0 {
			return nil
		}
		if sl.Type == "list" && bullet == "" && len(paras) > 1 {
			bullet = "•"
		}
		el.Kind = "text"
		el.Text = k.text(spec, sl.MinStep, paras, bullet, w, h, func(t *scene.Text) {
			if lvl == nil || t.Bullet == "" {
				return
			}
			t.BulletColor = k.p.Color(lvl.Color)
			t.IndentPt, t.HangPt, t.SpaceBefore = lvl.IndentPt, lvl.HangPt, lvl.SpaceBeforePt
		})
	case "image":
		el.Kind = "image"
		el.Image = &scene.Image{Placeholder: placeholder(c), Fill: k.p.Color(k.light()), TextColor: k.p.Color("dk1"), RadiusPt: sl.Radius}
		if comp != nil && comp.Frame != nil {
			fr := comp.Frame
			el.Image.RadiusPt = max(el.Image.RadiusPt, fr.Radius)
			el.Image.Line, el.Image.LineWidthPt, el.Image.Shadow = k.p.Color(fr.Line), fr.LineWidthPt, fr.Shadow
		}
	case "chart":
		if c.Chart == nil || len(c.Chart.Series) == 0 || len(c.Chart.Categories) == 0 {
			return nil
		}
		el.Kind = "chart"
		el.Chart = k.chart(comp, c.Chart)
	case "table":
		if c.Table == nil || len(c.Table.Header) == 0 {
			return nil
		}
		el.Kind = "table"
		el.Table = k.table(sl, c.Table, h)
	default:
		return nil
	}
	return el
}

func placeholder(c content.Slide) string {
	if c.ImagePrompt != "" {
		return c.ImagePrompt
	}
	return "Image"
}

// light returns a light neutral token for placeholders.
func (k composer) light() string {
	for _, n := range []string{"lt2", "c1"} {
		for _, t := range k.p.Tokens.Colors {
			if t.Name == n {
				return n
			}
		}
	}
	return "lt1"
}

func textFor(sl profile.Slot, c content.Slide) []string {
	name := strings.ToLower(sl.Name)
	switch {
	case strings.HasPrefix(name, "title"):
		return nonEmpty(c.Title)
	case strings.HasPrefix(name, "subtitle"):
		return nonEmpty(c.Subtitle)
	case sl.Type == "list":
		if len(c.Bullets) > 0 {
			return c.Bullets
		}
		return content.SplitList(c.Body)
	}
	if c.Body != "" {
		return []string{c.Body}
	}
	if len(c.Bullets) > 0 {
		return []string{strings.Join(c.Bullets, " ")}
	}
	return nonEmpty(c.Subtitle)
}

func nonEmpty(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return []string{s}
}

func (k composer) table(sl profile.Slot, t *content.Table, h float64) *scene.Table {
	ts := k.p.TableStyle(sl.TableStyle)
	if ts == nil {
		ts = &profile.TableStyle{Header: profile.TableHeader{Fill: "dk2", Text: "lt1", Bold: true, Step: "caption"},
			Body: profile.TableBody{Fill: "lt1", Text: "dk1", Step: "caption"}}
	}
	maxCols := sl.MaxCols
	if maxCols == 0 {
		maxCols = 6
	}
	maxRows := sl.MaxRows
	if maxRows == 0 {
		maxRows = 10
	}
	clip := func(r []string) []string { return r[:min(len(r), maxCols)] }
	out := &scene.Table{Header: clip(t.Header), Font: k.p.Font("body").Family,
		HeaderFill: k.p.Color(ts.Header.Fill), HeaderText: k.p.Color(ts.Header.Text), HeaderBold: ts.Header.Bold,
		BodyFill: k.p.Color(ts.Body.Fill), BodyText: k.p.Color(ts.Body.Text), Border: k.p.Color(ts.Border)}
	if ts.Body.BandRows {
		out.BandFill = k.p.Color(ts.Body.BandFill)
	}
	for _, r := range t.Rows[:min(len(t.Rows), maxRows-1)] {
		out.Rows = append(out.Rows, clip(r))
	}
	// Shrink the font until rows fit (row height ≈ 2.2 × size).
	size := k.p.Step(ts.Body.Step).Size
	for size > 9 && float64(len(out.Rows)+1)*size*2.2 > h {
		size--
	}
	out.SizePt = size
	return out
}

func (k composer) repeat(r profile.Repeat, items []content.Item) []*scene.Element {
	comp := k.p.Component(r.Component)
	if comp == nil || len(items) == 0 {
		return nil
	}
	n := min(max(len(items), r.Min), r.Max)
	if n > len(items) {
		n = len(items)
	}
	rx, ry, rw, rh := k.abs(r.Region)
	var out []*scene.Element
	for i := 0; i < n; i++ {
		var x, y, w, h float64
		if r.Direction == "column" {
			gap := r.Gap * k.h
			h = (rh - gap*float64(n-1)) / float64(n)
			x, y, w = rx, ry+float64(i)*(h+gap), rw
		} else {
			gap := r.Gap * k.w
			w = (rw - gap*float64(n-1)) / float64(n)
			x, y, h = rx+float64(i)*(w+gap), ry, rh
		}
		if comp.Shape != nil {
			out = append(out, &scene.Element{Kind: "shape", Role: "card", Slot: r.Name + "." + strconv.Itoa(i), X: x, Y: y, W: w, H: h,
				Shape: &scene.Shape{Geometry: comp.Shape.Kind, Fill: k.p.Color(comp.Shape.Fill), Line: k.p.Color(comp.Shape.Line),
					RadiusPt: comp.Shape.Radius, Shadow: comp.Shape.Shadow}})
		}
		vals := []string{items[i].Value, items[i].Label}
		for j, cs := range comp.Slots {
			var v string
			switch strings.ToLower(cs.Name) {
			case "value":
				v = items[i].Value
			case "label":
				v = items[i].Label
			default:
				if j < len(vals) {
					v = vals[j]
				}
			}
			if v == "" {
				continue
			}
			sx, sy, sw, sh := x+cs.Box.X*w, y+cs.Box.Y*h, cs.Box.W*w, cs.Box.H*h
			out = append(out, &scene.Element{Kind: "text", Role: cs.Name, Slot: r.Name + "." + strconv.Itoa(i) + "." + cs.Name,
				X: sx, Y: sy, W: sw, H: sh, Text: k.text(cs.Text, k.smallest(), []string{v}, "", sw, sh)})
		}
	}
	return out
}

func (k composer) decor(d profile.Decor, n int) *scene.Element {
	x, y, w, h := k.abs(d.Box)
	el := &scene.Element{Role: d.Role, X: x, Y: y, W: w, H: h}
	switch d.Kind {
	case "shape":
		el.Kind = "shape"
		el.Shape = &scene.Shape{Geometry: orRect(d.Geometry), Fill: k.p.Color(d.Fill), RadiusPt: d.Radius}
	case "image":
		a := k.p.Asset(d.Asset)
		if a == nil {
			return nil
		}
		el.Kind = "image"
		el.Image = &scene.Image{Ref: a.Ref, Mime: a.Mime}
		if alt := k.p.Asset(a.Alt); a.Alt != "" && alt != nil {
			el.Image.AltRef, el.Image.AltMime = alt.Ref, alt.Mime
		}
	case "text":
		txt := strings.ReplaceAll(d.Text, "{n}", strconv.Itoa(n))
		if txt == "" {
			return nil
		}
		el.Kind = "text"
		el.Text = k.text(d.TextSpec, "", []string{txt}, "", w, h)
	default:
		return nil
	}
	return el
}

func orRect(g string) string {
	if g == "" {
		return "rect"
	}
	return g
}

// smallest is the name of the smallest type-scale step.
func (k composer) smallest() string {
	name, size := "", 0.0
	for _, st := range k.p.Tokens.TypeScale.Steps {
		if name == "" || st.Size < size {
			name, size = st.Name, st.Size
		}
	}
	return name
}

// listBox draws a list component's panel unless the layout already has one there.
func (k composer) listBox(sl profile.Slot, l *profile.Layout) *scene.Element {
	comp := k.p.Component(sl.Component)
	if sl.Type != "list" || comp == nil || comp.Box == nil {
		return nil
	}
	for _, d := range l.Decor {
		if d.Kind == "shape" && d.Box.X <= sl.Box.X && d.Box.Y <= sl.Box.Y && d.Box.X+d.Box.W >= sl.Box.X+sl.Box.W && d.Box.Y+d.Box.H >= sl.Box.Y+sl.Box.H {
			return nil
		}
	}
	x, y, w, h := k.abs(sl.Box)
	pad := comp.Box.PadPt
	bx := comp.Box
	return &scene.Element{Kind: "shape", Role: "listBox", Slot: sl.Name + ".box", X: x - pad, Y: y - pad, W: w + 2*pad, H: h + 2*pad,
		Shape: &scene.Shape{Geometry: orRect(bx.Kind), Fill: k.p.Color(bx.Fill), Line: k.p.Color(bx.Line), RadiusPt: bx.Radius, Shadow: bx.Shadow}}
}

// chart merges generated chart data with the profile's chart style.
func (k composer) chart(comp *profile.Component, d *content.Chart) *scene.Chart {
	spec := &profile.ChartSpec{Type: "col", Grouping: "clustered", Axis: &profile.ChartAxis{Gridlines: true}}
	if comp != nil && comp.Chart != nil {
		spec = comp.Chart
	}
	out := &scene.Chart{Type: spec.Type, Grouping: spec.Grouping, Legend: spec.Legend, GapWidth: spec.GapWidth, HolePct: spec.HolePct,
		Font: k.p.Font("body").Family, FontSizePt: 10, TextColor: k.p.Color("dk1")}
	out.Categories = d.Categories[:min(len(d.Categories), 12)]
	for _, s := range d.Series[:min(len(d.Series), 6)] {
		out.Series = append(out.Series, scene.ChartSeries{Name: s.Name, Values: s.Values[:min(len(s.Values), len(out.Categories))]})
	}
	if (out.Type == "pie" || out.Type == "doughnut") && len(out.Series) > 1 {
		out.Series = out.Series[:1]
	}
	for _, s := range spec.Series {
		if c := k.p.Color(s.Color); c != "" {
			out.Colors = append(out.Colors, c)
		}
	}
	if len(out.Colors) < 2 && (out.Type == "pie" || out.Type == "doughnut" || len(out.Series) > len(out.Colors)) {
		for i := 1; i <= 6; i++ {
			if c := k.p.Color(fmt.Sprintf("accent%d", i)); c != "" {
				out.Colors = append(out.Colors, c)
			}
		}
	}
	if len(out.Colors) == 0 {
		out.Colors = []string{k.p.Color("dk2"), k.p.Color("dk1")}
	}
	if l := spec.Labels; l != nil {
		out.DataLabels, out.LabelColor, out.LabelSizePt, out.NumFmt = true, k.p.Color(l.Color), l.SizePt, l.NumFmt
	}
	if a := spec.Axis; a != nil {
		out.Gridlines, out.GridColor, out.AxisColor = a.Gridlines, k.p.Color(a.Grid), k.p.Color(a.Line)
		if a.SizePt > 0 {
			out.FontSizePt = a.SizePt
		}
	}
	return out
}
