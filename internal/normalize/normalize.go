// Package normalize turns a source-agnostic rawdeck.Deck into a StyleProfile:
// tokens, type scale, components, layouts (via clustering), table styles and
// placement patterns. Deterministic; no LLM involved.
package normalize

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/ashutoshm97/curly-engine/internal/profile"
	"github.com/ashutoshm97/curly-engine/internal/rawdeck"
)

type Options struct {
	ID   string
	Name string
	// ClusterThreshold is the max layout distance (0..1) to join a cluster.
	ClusterThreshold float64
}

type builder struct {
	d      *rawdeck.Deck
	opts   Options
	pal    *palette
	p      *profile.StyleProfile
	comps  map[string]string // signature -> component id
	assets map[string]string // image ref -> asset id
	steps  []profile.Step
}

// Build normalizes a deck into a profile. Asset refs listed in the profile
// refer to entries in d.Assets that the caller must persist.
func Build(d *rawdeck.Deck, opts Options) (*profile.StyleProfile, error) {
	if len(d.Slides) == 0 {
		return nil, fmt.Errorf("deck has no slides")
	}
	if opts.ClusterThreshold == 0 {
		opts.ClusterThreshold = 0.4
	}
	b := &builder{d: d, opts: opts, pal: newPalette(), comps: map[string]string{}, assets: map[string]string{}}
	b.p = &profile.StyleProfile{
		ID: opts.ID, Name: opts.Name, Version: 1, SchemaVersion: profile.SchemaVersion,
		Source: "sha256:" + d.SourceHash, SourceType: d.SourceType,
		Aspect: aspect(d.WidthPt, d.HeightPt), WidthPt: round(d.WidthPt, 2), HeightPt: round(d.HeightPt, 2),
	}
	if len(d.Theme.Colors) == 0 {
		d.Theme = inferTheme(d)
	}
	for _, c := range d.Theme.Colors {
		b.pal.add(c.Name, c.Hex, false)
	}
	b.classify()
	b.fonts()
	b.typeScale()
	b.spacing()
	b.tables()
	clusters := b.cluster()
	b.layouts(clusters)
	b.placements()
	b.p.Tokens.Colors = b.pal.tokens
	if err := b.p.Validate(); err != nil {
		return nil, err
	}
	return b.p, nil
}

func aspect(w, h float64) string {
	r := w / h
	switch {
	case math.Abs(r-16.0/9) < 0.02:
		return "16:9"
	case math.Abs(r-4.0/3) < 0.02:
		return "4:3"
	case math.Abs(r-16.0/10) < 0.02:
		return "16:10"
	}
	return strconv.FormatFloat(round(r, 3), 'f', -1, 64) + ":1"
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

func snap(b rawdeck.Box) profile.Box {
	s := func(v float64) float64 { return round(math.Round(v/0.005)*0.005, 3) }
	return profile.Box{X: s(b.X), Y: s(b.Y), W: s(b.W), H: s(b.H)}
}

func isContent(r rawdeck.Role) bool {
	switch r {
	case rawdeck.RoleTitle, rawdeck.RoleSubtitle, rawdeck.RoleBody, rawdeck.RoleImage,
		rawdeck.RoleTable, rawdeck.RoleChart, rawdeck.RoleCard:
		return true
	}
	return false
}

// classify assigns roles to elements that lack an authoritative placeholder
// role and groups filled shapes with the text they contain into cards.
func (b *builder) classify() {
	for _, s := range b.d.Slides {
		var kept []*rawdeck.Element
		consumed := map[*rawdeck.Element]bool{}
		hasTitle := false
		maxSize := 0.0
		for _, el := range s.Elements {
			if el.Role == rawdeck.RoleTitle {
				hasTitle = true
			}
			if el.Kind == rawdeck.KindText && !el.Inherited && el.Style.SizePt > maxSize {
				maxSize = el.Style.SizePt
			}
		}
		// Cards: filled shape containing ≥1 non-placeholder text.
		for _, sh := range s.Elements {
			if sh.Kind != rawdeck.KindShape || sh.Inherited || sh.Fill.IsZero() {
				continue
			}
			if a := sh.Box.Area(); a < 0.005 || a > 0.35 {
				continue
			}
			var inner []*rawdeck.Element
			for _, t := range s.Elements {
				if t.Kind != rawdeck.KindText || t.PlaceholderType != "" || consumed[t] || t == sh {
					continue
				}
				if t.Box.CX() > sh.Box.X && t.Box.CX() < sh.Box.Right() && t.Box.CY() > sh.Box.Y && t.Box.CY() < sh.Box.Bottom() {
					inner = append(inner, t)
				}
			}
			if len(inner) == 0 {
				continue
			}
			sort.Slice(inner, func(i, j int) bool { return inner[i].Box.Y < inner[j].Box.Y })
			for _, t := range inner {
				consumed[t] = true
			}
			consumed[sh] = true
			card := *sh
			card.Role = rawdeck.RoleCard
			card.Children = inner
			kept = append(kept, &card)
		}
		for _, el := range s.Elements {
			if consumed[el] {
				continue
			}
			if el.PlaceholderType == "" && el.Role != rawdeck.RoleTable && el.Role != rawdeck.RoleChart {
				switch el.Kind {
				case rawdeck.KindText:
					switch {
					case el.Box.Y > 0.84 && len(el.Text) < 60:
						if _, err := strconv.Atoi(strings.TrimSpace(el.Text)); err == nil {
							el.Role = rawdeck.RolePageNumber
						} else {
							el.Role = rawdeck.RoleFooter
						}
					case !hasTitle && !el.Inherited && el.Style.SizePt == maxSize && el.Style.SizePt >= 20 && el.Box.Y < 0.5:
						el.Role = rawdeck.RoleTitle
						hasTitle = true
					case el.Inherited:
						el.Role = rawdeck.RoleDecor
					default:
						el.Role = rawdeck.RoleBody
					}
				case rawdeck.KindImage:
					cx, cy := el.Box.CX(), el.Box.CY()
					if el.Box.Area() < 0.03 && (cx < 0.25 || cx > 0.75) && (cy < 0.2 || cy > 0.8) {
						el.Role = rawdeck.RoleLogo
					} else if el.Inherited || el.Box.Area() < 0.015 {
						el.Role = rawdeck.RoleDecor // repeated art or small icons, not content slots
					} else {
						el.Role = rawdeck.RoleImage
					}
				case rawdeck.KindShape:
					if el.Box.Area() > 0.85 {
						if s.Background.IsZero() {
							s.Background = el.Fill
						}
						continue
					}
					el.Role = rawdeck.RoleDecor
				}
			}
			kept = append(kept, el)
		}
		// Subtitle: a short single-paragraph body directly under the title on a
		// slide with no other body text (typical of title slides).
		var title *rawdeck.Element
		var bodies []*rawdeck.Element
		for _, el := range kept {
			switch el.Role {
			case rawdeck.RoleTitle:
				title = el
			case rawdeck.RoleBody:
				if !el.Inherited {
					bodies = append(bodies, el)
				}
			}
		}
		if title != nil && len(bodies) == 1 {
			bd := bodies[0]
			if bd.Style.Paragraphs <= 1 && len(bd.Text) < 120 && bd.Box.Y >= title.Box.Y && bd.Style.SizePt < title.Style.SizePt {
				hasOthers := false
				for _, el := range kept {
					if el.Role == rawdeck.RoleImage || el.Role == rawdeck.RoleTable || el.Role == rawdeck.RoleCard || el.Role == rawdeck.RoleChart {
						hasOthers = true
					}
				}
				if !hasOthers {
					bd.Role = rawdeck.RoleSubtitle
				}
			}
		}
		s.Elements = kept
	}
}

func (b *builder) fonts() {
	count := func(role rawdeck.Role) string {
		m := map[string]int{}
		for _, s := range b.d.Slides {
			for _, el := range s.Elements {
				if el.Role == role && el.Style.Font != "" {
					m[el.Style.Font] += len(el.Text) + 1
				}
			}
		}
		best, bn := "", 0
		for f, n := range m {
			if n > bn || (n == bn && f < best) {
				best, bn = f, n
			}
		}
		return best
	}
	heading, body := b.d.Theme.HeadingFont, b.d.Theme.BodyFont
	if heading == "" {
		heading = count(rawdeck.RoleTitle)
	}
	if body == "" {
		body = count(rawdeck.RoleBody)
	}
	if heading == "" {
		heading = "Arial"
	}
	if body == "" {
		body = heading
	}
	b.p.Tokens.Fonts = []profile.FontToken{
		{Role: "heading", Family: heading, Fallback: fallbackFont(heading)},
		{Role: "body", Family: body, Fallback: fallbackFont(body)},
	}
}

func fallbackFont(f string) string {
	l := strings.ToLower(f)
	switch {
	case strings.Contains(l, "mono") || strings.Contains(l, "courier") || strings.Contains(l, "code"):
		return "Courier New"
	case strings.Contains(l, "serif") && !strings.Contains(l, "sans"), strings.Contains(l, "georgia"),
		strings.Contains(l, "times"), strings.Contains(l, "garamond"), strings.Contains(l, "playfair"):
		return "Georgia"
	}
	return "Arial"
}

func mode(vals map[float64]float64, def float64) float64 {
	best, bw := def, 0.0
	for v, w := range vals {
		if w > bw || (w == bw && v > best) {
			best, bw = v, w
		}
	}
	return best
}

func (b *builder) typeScale() {
	title, sub, body, value, small := map[float64]float64{}, map[float64]float64{}, map[float64]float64{}, map[float64]float64{}, map[float64]float64{}
	maxTitle := 0.0
	for _, s := range b.d.Slides {
		for _, el := range s.Elements {
			sz := math.Round(el.Style.SizePt)
			switch el.Role {
			case rawdeck.RoleTitle:
				title[sz]++
				maxTitle = math.Max(maxTitle, sz)
			case rawdeck.RoleSubtitle:
				sub[sz]++
			case rawdeck.RoleBody:
				body[sz] += float64(len(el.Text) + 1)
			case rawdeck.RoleFooter, rawdeck.RolePageNumber:
				small[sz]++
			case rawdeck.RoleCard:
				for i, ch := range el.Children {
					if i == 0 {
						value[math.Round(ch.Style.SizePt)]++
					} else {
						small[math.Round(ch.Style.SizePt)]++
					}
				}
			}
			if el.Table != nil && el.Table.FontSizePt > 0 {
				small[math.Round(el.Table.FontSizePt)] += 0.5
			}
		}
	}
	bodySz := mode(body, 18)
	h1 := mode(title, math.Round(bodySz*2))
	h2 := mode(value, 0)
	if h2 == 0 {
		h2 = mode(sub, math.Round((h1+bodySz)/2))
	}
	caption := mode(small, math.Round(bodySz*0.7))
	if caption >= bodySz {
		caption = math.Round(bodySz * 0.7)
	}
	steps := []profile.Step{}
	if maxTitle > h1*1.1 {
		steps = append(steps, profile.Step{Name: "display", Size: maxTitle, LineHeight: 1.1})
	}
	steps = append(steps,
		profile.Step{Name: "h1", Size: h1, LineHeight: 1.15},
		profile.Step{Name: "h2", Size: h2, LineHeight: 1.2},
		profile.Step{Name: "body", Size: bodySz, LineHeight: 1.3},
		profile.Step{Name: "caption", Size: caption, LineHeight: 1.3},
	)
	b.steps = steps
	b.p.Tokens.TypeScale = profile.TypeScale{Unit: "pt", Steps: steps}
}

// stepFor maps a font size to the nearest step, constrained by role.
func (b *builder) stepFor(size float64, role rawdeck.Role) string {
	allowed := map[string]bool{}
	switch role {
	case rawdeck.RoleTitle:
		allowed = map[string]bool{"display": true, "h1": true, "h2": true}
	case rawdeck.RoleFooter, rawdeck.RolePageNumber:
		allowed = map[string]bool{"caption": true, "body": true}
	}
	best, bd := "body", math.MaxFloat64
	for _, s := range b.steps {
		if len(allowed) > 0 && !allowed[s.Name] {
			continue
		}
		if d := math.Abs(s.Size - size); d < bd {
			best, bd = s.Name, d
		}
	}
	return best
}

func (b *builder) spacing() {
	var lefts, rights, tops, bottoms, gaps []float64
	for _, s := range b.d.Slides {
		var content []*rawdeck.Element
		for _, el := range s.Elements {
			if isContent(el.Role) && !el.Inherited {
				content = append(content, el)
				lefts = append(lefts, el.Box.X)
				rights = append(rights, 1-el.Box.Right())
				tops = append(tops, el.Box.Y)
				bottoms = append(bottoms, 1-el.Box.Bottom())
			}
		}
		sort.Slice(content, func(i, j int) bool { return content[i].Box.X < content[j].Box.X })
		for i := 1; i < len(content); i++ {
			a, c := content[i-1], content[i]
			if math.Abs(a.Box.Y-c.Box.Y) < 0.05 {
				if g := c.Box.X - a.Box.Right(); g > 0.001 && g < 0.2 {
					gaps = append(gaps, g)
				}
			}
		}
	}
	pct := func(v []float64, q float64, def float64) float64 {
		if len(v) == 0 {
			return def
		}
		sort.Float64s(v)
		return round(math.Max(0, v[int(q*float64(len(v)-1))]), 3)
	}
	b.p.Tokens.Spacing = profile.Spacing{
		Unit: "frac", Grid: 12, Gutter: pct(gaps, 0.5, 0.02),
		Margin: fmt.Sprintf("%v %v %v %v", pct(tops, 0.1, 0.06), pct(rights, 0.1, 0.06), pct(bottoms, 0.1, 0.06), pct(lefts, 0.1, 0.06)),
	}
	radii := map[float64]float64{}
	for _, s := range b.d.Slides {
		for _, el := range s.Elements {
			if el.RadiusPx > 0 {
				radii[math.Round(el.RadiusPx)]++
			}
		}
	}
	if r := mode(radii, 0); r > 0 {
		b.p.Tokens.Radii = []profile.Radius{{Name: "md", Px: r}}
	}
}

func (b *builder) textSpec(el *rawdeck.Element) *profile.TextSpec {
	font := el.Style.FontRole
	if font == "" {
		font = "body"
		if el.Style.Font == b.p.Tokens.Fonts[0].Family && el.Role == rawdeck.RoleTitle {
			font = "heading"
		}
	}
	color := b.pal.ref(el.Style.Color)
	if color == "" {
		color = "dk1"
	}
	align := el.Style.Align
	if align == "" {
		align = "l"
	}
	return &profile.TextSpec{Font: font, Step: b.stepFor(el.Style.SizePt, el.Role), Color: color, Align: align, Bold: el.Style.Bold, Italic: el.Style.Italic}
}

// component registers (or reuses) a component for a text element.
func (b *builder) component(el *rawdeck.Element, list bool) string {
	ts := b.textSpec(el)
	typ := "text"
	var marker *profile.Marker
	if list {
		typ = "list"
		glyph := el.Style.Bullet
		if glyph == "" {
			glyph = "•"
		}
		marker = &profile.Marker{Glyph: glyph}
	}
	sig := fmt.Sprintf("%s|%s|%+v|%+v", typ, el.Role, *ts, marker)
	if id, ok := b.comps[sig]; ok {
		return id
	}
	base := "C." + string(el.Role)
	if el.Role == "" {
		base = "C.text"
	}
	id := base
	for i := 2; b.p.Component(id) != nil; i++ {
		id = fmt.Sprintf("%s%d", base, i)
	}
	b.comps[sig] = id
	b.p.Components = append(b.p.Components, profile.Component{ID: id, Type: typ, Role: string(el.Role), Text: ts, Marker: marker})
	return id
}

func (b *builder) cardComponent(card *rawdeck.Element) string {
	shape := &profile.ShapeSpec{Kind: card.Geometry, Fill: b.pal.ref(card.Fill), Line: b.pal.ref(card.Line), Radius: round(card.RadiusPx, 1), Shadow: card.Shadow}
	if shape.Kind == "" {
		shape.Kind = "rect"
	}
	var slots []profile.ComponentSlot
	for i, ch := range card.Children {
		name := "value"
		if i == 1 {
			name = "label"
		} else if i > 1 {
			name = fmt.Sprintf("text%d", i+1)
		}
		ts := b.textSpec(ch)
		if i == 0 && ts.Step == "body" {
			ts.Step = "h2"
		}
		rel := rawdeck.Box{
			X: (ch.Box.X - card.Box.X) / card.Box.W, Y: (ch.Box.Y - card.Box.Y) / card.Box.H,
			W: ch.Box.W / card.Box.W, H: ch.Box.H / card.Box.H,
		}
		slots = append(slots, profile.ComponentSlot{Name: name, Box: snap(rel), Text: ts})
	}
	sig := fmt.Sprintf("card|%+v|%d", *shape, len(slots))
	for _, s := range slots {
		sig += fmt.Sprintf("|%+v", *s.Text)
	}
	if id, ok := b.comps[sig]; ok {
		return id
	}
	id := "C.card"
	for i := 2; b.p.Component(id) != nil; i++ {
		id = fmt.Sprintf("C.card%d", i)
	}
	b.comps[sig] = id
	b.p.Components = append(b.p.Components, profile.Component{ID: id, Type: "group", Role: "card", Shape: shape, Slots: slots})
	return id
}

func (b *builder) tables() {
	seen := map[string]string{}
	for _, s := range b.d.Slides {
		for _, el := range s.Elements {
			t := el.Table
			if t == nil {
				continue
			}
			ts := profile.TableStyle{
				SourceStyleID: t.StyleID,
				Header:        profile.TableHeader{Fill: b.pal.ref(t.HeaderFill), Text: b.pal.ref(t.HeaderText), Bold: t.HeaderBold, Step: "caption"},
				Body:          profile.TableBody{Fill: b.pal.ref(t.BodyFill), BandRows: t.BandRow || !t.BandFill.IsZero(), BandFill: b.pal.ref(t.BandFill), Text: b.pal.ref(t.BodyText), Step: "caption"},
				Border:        b.pal.ref(t.BorderColor),
			}
			if t.FontSizePt > 0 {
				st := b.stepFor(t.FontSizePt, rawdeck.RoleBody)
				ts.Header.Step, ts.Body.Step = st, st
			}
			if ts.Header.Text == "" {
				ts.Header.Text = "lt1"
			}
			if ts.Body.Text == "" {
				ts.Body.Text = "dk1"
			}
			sig := fmt.Sprintf("%+v%+v", ts.Header, ts.Body)
			if id, ok := seen[sig]; ok {
				el.Table.StyleID = id
				continue
			}
			ts.ID = fmt.Sprintf("T%d", len(b.p.Tables)+1)
			seen[sig] = ts.ID
			el.Table.StyleID = ts.ID
			b.p.Tables = append(b.p.Tables, ts)
		}
	}
}

func (b *builder) asset(ref string) string {
	if ref == "" {
		return ""
	}
	if id, ok := b.assets[ref]; ok {
		return id
	}
	a, ok := b.d.Assets[ref]
	if !ok {
		return ""
	}
	id := fmt.Sprintf("A%d", len(b.p.Assets)+1)
	b.assets[ref] = id
	b.p.Assets = append(b.p.Assets, profile.Asset{ID: id, Ref: ref, Kind: "image", Mime: a.Mime})
	return id
}
