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

type cluster struct {
	members []*rawdeck.Slide
	medoid  *rawdeck.Slide
}

func contentOf(s *rawdeck.Slide) []*rawdeck.Element {
	var out []*rawdeck.Element
	for _, el := range s.Elements {
		if isContent(el.Role) && !el.Inherited {
			out = append(out, el)
		}
	}
	return out
}

func decorOf(s *rawdeck.Slide) []*rawdeck.Element {
	var out []*rawdeck.Element
	for _, el := range s.Elements {
		if !isContent(el.Role) || el.Inherited {
			out = append(out, el)
		}
	}
	return out
}

// match greedily pairs elements of the same role by IoU.
func match(a, b []*rawdeck.Element) (pairs [][2]*rawdeck.Element, score float64) {
	used := map[*rawdeck.Element]bool{}
	for _, x := range a {
		var best *rawdeck.Element
		bi := 0.0
		for _, y := range b {
			if used[y] || y.Role != x.Role {
				continue
			}
			if iou := x.Box.IoU(y.Box); iou > bi {
				best, bi = y, iou
			}
		}
		if best != nil && bi > 0.2 {
			used[best] = true
			pairs = append(pairs, [2]*rawdeck.Element{x, best})
			score += bi
		}
	}
	return pairs, score
}

func distance(a, b *rawdeck.Slide) float64 {
	ca, cb := contentOf(a), contentOf(b)
	n := max(len(ca), len(cb))
	if n == 0 {
		return 0
	}
	_, s := match(ca, cb)
	d := 1 - s/float64(n)
	// Cards are compared by count as well: a 3-card and 4-card row are the same layout.
	if hasRole(ca, rawdeck.RoleCard) && hasRole(cb, rawdeck.RoleCard) {
		d *= 0.5
	}
	return d
}

func hasRole(els []*rawdeck.Element, r rawdeck.Role) bool {
	for _, e := range els {
		if e.Role == r {
			return true
		}
	}
	return false
}

func (b *builder) cluster() []*cluster {
	var cs []*cluster
	for _, s := range b.d.Slides {
		var best *cluster
		bd := math.MaxFloat64
		for _, c := range cs {
			d := distance(s, c.members[0])
			if d < bd {
				best, bd = c, d
			}
		}
		if best != nil && bd <= b.opts.ClusterThreshold {
			best.members = append(best.members, s)
		} else {
			cs = append(cs, &cluster{members: []*rawdeck.Slide{s}})
		}
	}
	for _, c := range cs {
		bestSum := math.MaxFloat64
		for _, m := range c.members {
			sum := 0.0
			for _, o := range c.members {
				sum += distance(m, o)
			}
			if sum < bestSum {
				c.medoid, bestSum = m, sum
			}
		}
	}
	return cs
}

// capacity estimates how many characters fit in a box at a font size.
func capacity(box rawdeck.Box, wPt, hPt, sizePt, lineHeight float64) int {
	if sizePt <= 0 {
		return 0
	}
	charsPerLine := box.W * wPt / (sizePt * 0.5)
	lines := math.Floor(box.H * hPt / (sizePt * lineHeight))
	return int(math.Max(1, lines) * charsPerLine * 0.9)
}

func (b *builder) layouts(cs []*cluster) {
	total := float64(len(b.d.Slides))
	slideLayout := map[int]string{}
	for i, c := range cs {
		id := fmt.Sprintf("L%d", i+1)
		l := profile.Layout{ID: id, Freq: round(float64(len(c.members))/total, 3)}
		var idx []string
		bgCount := map[string]int{}
		for _, m := range c.members {
			idx = append(idx, strconv.Itoa(m.Index))
			slideLayout[m.Index] = id
			bgCount[b.pal.ref(m.Background)]++
		}
		l.SourceSlides = strings.Join(idx, ",")
		bestBg, bn := "", 0
		for k, n := range bgCount {
			if n > bn || (n == bn && k < bestBg) {
				bestBg, bn = k, n
			}
		}
		l.Background = bestBg

		content := contentOf(c.medoid)
		sort.SliceStable(content, func(i, j int) bool {
			if math.Abs(content[i].Box.Y-content[j].Box.Y) > 0.03 {
				return content[i].Box.Y < content[j].Box.Y
			}
			return content[i].Box.X < content[j].Box.X
		})
		// Average each medoid element's box across matched members.
		avg := map[*rawdeck.Element]rawdeck.Box{}
		for _, el := range content {
			avg[el] = el.Box
		}
		sums := map[*rawdeck.Element][]rawdeck.Box{}
		for _, m := range c.members {
			pairs, _ := match(content, contentOf(m))
			for _, p := range pairs {
				sums[p[0]] = append(sums[p[0]], p[1].Box)
			}
		}
		for el, boxes := range sums {
			var a rawdeck.Box
			for _, bx := range boxes {
				a.X += bx.X
				a.Y += bx.Y
				a.W += bx.W
				a.H += bx.H
			}
			n := float64(len(boxes))
			avg[el] = rawdeck.Box{X: a.X / n, Y: a.Y / n, W: a.W / n, H: a.H / n}
		}

		counts := map[string]int{}
		var cards []*rawdeck.Element
		maxCards := 0
		for _, m := range c.members {
			n := 0
			for _, el := range contentOf(m) {
				if el.Role == rawdeck.RoleCard {
					n++
				}
			}
			maxCards = max(maxCards, n)
		}
		for _, el := range content {
			if el.Role == rawdeck.RoleCard {
				cards = append(cards, el)
				continue
			}
			base := string(el.Role)
			counts[base]++
			name := base
			if counts[base] > 1 {
				name = fmt.Sprintf("%s%d", base, counts[base])
			}
			box := avg[el]
			slot := profile.Slot{Name: name, Box: snap(box)}
			switch el.Role {
			case rawdeck.RoleTitle, rawdeck.RoleSubtitle, rawdeck.RoleBody:
				list := el.Role == rawdeck.RoleBody && (el.Style.Paragraphs > 1 || el.Style.Bullet != "")
				slot.Type = "text"
				if list {
					slot.Type = "list"
				}
				slot.Component = b.component(el, list)
				st := b.p.Step(b.p.Component(slot.Component).Text.Step)
				slot.MaxChars = capacity(box, b.p.WidthPt, b.p.HeightPt, st.Size, st.LineHeight)
				if below := b.p.StepsBelow(st.Name); len(below) > 0 && el.Role == rawdeck.RoleBody {
					slot.MinStep = below[0].Name
				}
				slot.Example = truncate(el.Text, 80)
			case rawdeck.RoleImage:
				slot.Type = "image"
				slot.Radius = round(el.RadiusPx, 1)
			case rawdeck.RoleTable:
				slot.Type = "table"
				if el.Table != nil {
					slot.TableStyle = el.Table.StyleID
					slot.MaxCols = max(el.Table.Cols, 2) + 1
				}
				rowH := b.p.Step("caption").Size * 2.2
				slot.MaxRows = max(3, int(box.H*b.p.HeightPt/rowH))
			case rawdeck.RoleChart:
				slot.Type = "chart"
			}
			l.Slots = append(l.Slots, slot)
		}
		if len(cards) > 0 {
			l.Repeats = append(l.Repeats, b.repeat(cards, maxCards))
		}
		l.Decor = b.layoutDecor(c)
		l.Kind = layoutKind(l, c, len(b.d.Slides))
		l.Name = layoutName(l)
		l.Description = describe(l)
		b.p.Layouts = append(b.p.Layouts, l)
	}
	// Sequence + transitions.
	trans := map[[2]string]int{}
	var seq []string
	for i, s := range b.d.Slides {
		seq = append(seq, slideLayout[s.Index])
		if i > 0 {
			trans[[2]string{seq[i-1], seq[i]}]++
		}
	}
	for k, n := range trans {
		b.p.Patterns.Transitions = append(b.p.Patterns.Transitions, profile.Transition{From: k[0], To: k[1], Count: n})
	}
	sort.Slice(b.p.Patterns.Transitions, func(i, j int) bool {
		ti, tj := b.p.Patterns.Transitions[i], b.p.Patterns.Transitions[j]
		if ti.Count != tj.Count {
			return ti.Count > tj.Count
		}
		return ti.From+ti.To < tj.From+tj.To
	})
	b.p.Patterns.Sequence = sequence(seq)
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " / ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func (b *builder) repeat(cards []*rawdeck.Element, maxCards int) profile.Repeat {
	sort.Slice(cards, func(i, j int) bool { return cards[i].Box.X+cards[i].Box.Y < cards[j].Box.X+cards[j].Box.Y })
	region := cards[0].Box
	for _, c := range cards[1:] {
		x2, y2 := math.Max(region.Right(), c.Box.Right()), math.Max(region.Bottom(), c.Box.Bottom())
		region.X, region.Y = math.Min(region.X, c.Box.X), math.Min(region.Y, c.Box.Y)
		region.W, region.H = x2-region.X, y2-region.Y
	}
	dir := "row"
	gap := 0.02
	if len(cards) > 1 {
		if math.Abs(cards[0].Box.Y-cards[1].Box.Y) > math.Abs(cards[0].Box.X-cards[1].Box.X) {
			dir = "column"
			gap = cards[1].Box.Y - cards[0].Box.Bottom()
		} else {
			gap = cards[1].Box.X - cards[0].Box.Right()
		}
	}
	return profile.Repeat{
		Name: "items", Component: b.cardComponent(cards[0]), Min: min(2, len(cards)), Max: max(4, maxCards),
		Region: snap(region), Direction: dir, Gap: round(math.Max(0, gap), 3),
	}
}

// layoutDecor keeps non-content elements that recur in ≥50% of the cluster.
func (b *builder) layoutDecor(c *cluster) []profile.Decor {
	var out []profile.Decor
	for _, el := range decorOf(c.medoid) {
		hits := 0
		for _, m := range c.members {
			for _, o := range decorOf(m) {
				if o.Role == el.Role && o.Box.IoU(el.Box) > 0.5 {
					hits++
					break
				}
			}
		}
		if float64(hits)/float64(len(c.members)) < 0.5 {
			continue
		}
		d := profile.Decor{Role: string(el.Role), Box: snap(el.Box), Geometry: el.Geometry, Radius: round(el.RadiusPx, 1)}
		switch el.Kind {
		case rawdeck.KindImage:
			d.Kind = "image"
			d.Asset = b.asset(el.ImageRef)
		case rawdeck.KindText:
			d.Kind = "text"
			d.Text = el.Text
			if el.Role == rawdeck.RolePageNumber {
				d.Text = "{n}"
			}
			d.TextSpec = b.textSpec(el)
		default:
			d.Kind = "shape"
			d.Fill = b.pal.ref(el.Fill)
		}
		out = append(out, d)
	}
	return out
}

func layoutKind(l profile.Layout, c *cluster, nSlides int) string {
	if len(l.Repeats) > 0 {
		return "data"
	}
	roles := map[string]bool{}
	for _, s := range l.Slots {
		roles[s.Type+":"+strings.TrimRight(s.Name, "0123456789")] = true
		if s.Type == "table" || s.Type == "chart" {
			return "data"
		}
	}
	onlyTitles := true
	for k := range roles {
		if k != "text:title" && k != "text:subtitle" {
			onlyTitles = false
		}
	}
	if onlyTitles {
		for _, m := range c.members {
			if m.Index == 1 {
				return "title"
			}
		}
		for _, m := range c.members {
			if m.Index == nSlides {
				return "closing"
			}
		}
		if roles["text:subtitle"] {
			return "title"
		}
		return "section"
	}
	return "content"
}

func layoutName(l profile.Layout) string {
	var parts []string
	has := map[string]int{}
	for _, s := range l.Slots {
		has[s.Type]++
	}
	switch l.Kind {
	case "title":
		return "Title slide"
	case "closing":
		return "Closing slide"
	case "section":
		return "Section header"
	}
	parts = append(parts, "Title")
	var img, txt *profile.Slot
	for i := range l.Slots {
		s := &l.Slots[i]
		if s.Type == "image" && img == nil {
			img = s
		}
		if (s.Type == "list" || s.Type == "text") && s.Name != "title" && txt == nil {
			txt = s
		}
	}
	switch {
	case txt != nil && img != nil:
		if txt.Box.X < img.Box.X {
			parts = append(parts, "text left, image right")
		} else {
			parts = append(parts, "image left, text right")
		}
	case has["list"] > 1 || (has["list"]+has["text"] > 2):
		parts = append(parts, "two columns")
	case txt != nil:
		parts = append(parts, "body")
	case img != nil:
		parts = append(parts, "image")
	}
	if has["table"] > 0 {
		parts = append(parts, "table")
	}
	if has["chart"] > 0 {
		parts = append(parts, "chart")
	}
	for _, r := range l.Repeats {
		parts = append(parts, fmt.Sprintf("%d-%d cards", r.Min, r.Max))
	}
	return strings.Join(parts, " + ")
}

func describe(l profile.Layout) string {
	var b strings.Builder
	switch l.Kind {
	case "title":
		b.WriteString("Opening slide with the deck title and a short subtitle.")
	case "closing":
		b.WriteString("Closing slide (thank you / contact / call to action).")
	case "section":
		b.WriteString("Section divider with a single heading.")
	case "data":
		b.WriteString("Data slide for numbers: ")
		var parts []string
		for _, s := range l.Slots {
			if s.Type == "table" {
				parts = append(parts, fmt.Sprintf("a table (up to %d rows x %d cols)", s.MaxRows, s.MaxCols))
			}
			if s.Type == "chart" {
				parts = append(parts, "a chart")
			}
		}
		for _, r := range l.Repeats {
			parts = append(parts, fmt.Sprintf("%d-%d metric cards (value + label)", r.Min, r.Max))
		}
		b.WriteString(strings.Join(parts, ", ") + ".")
	default:
		b.WriteString("Content slide: " + strings.ToLower(l.Name) + ".")
	}
	return b.String()
}

func sequence(seq []string) string {
	if len(seq) == 0 {
		return ""
	}
	first, last := seq[0], seq[len(seq)-1]
	mid := []string{}
	seen := map[string]bool{}
	end := len(seq)
	if len(seq) > 1 {
		end = len(seq) - 1
	}
	for _, s := range seq[1:end] {
		if !seen[s] {
			seen[s] = true
			mid = append(mid, s)
		}
	}
	out := first
	if len(mid) > 0 {
		out += " → (" + strings.Join(mid, " | ") + ")*"
	}
	if len(seq) > 1 {
		out += " → " + last
	}
	return out
}

// placements finds deck-wide recurring elements (logo, page number, …).
func (b *builder) placements() {
	type cand struct {
		el   *rawdeck.Element
		hits int
	}
	var cands []*cand
	for _, s := range b.d.Slides {
		seen := map[*cand]bool{}
		for _, el := range s.Elements {
			switch el.Role {
			case rawdeck.RoleLogo, rawdeck.RolePageNumber, rawdeck.RoleFooter:
			default:
				continue
			}
			var found *cand
			for _, c := range cands {
				if c.el.Role == el.Role && c.el.Box.IoU(el.Box) > 0.5 {
					found = c
					break
				}
			}
			if found == nil {
				found = &cand{el: el}
				cands = append(cands, found)
			}
			if !seen[found] {
				seen[found] = true
				found.hits++
			}
		}
	}
	n := float64(len(b.d.Slides))
	for _, c := range cands {
		sup := float64(c.hits) / n
		if sup < 0.5 {
			continue
		}
		p := profile.Placement{Element: string(c.el.Role), Anchor: anchor(c.el.Box), Box: snap(c.el.Box), Support: round(sup, 2)}
		if c.el.Kind == rawdeck.KindImage {
			p.Asset = b.asset(c.el.ImageRef)
		}
		b.p.Patterns.Placements = append(b.p.Patterns.Placements, p)
	}
}

func anchor(bx rawdeck.Box) string {
	v, h := "m", "c"
	if bx.CY() < 0.33 {
		v = "t"
	} else if bx.CY() > 0.66 {
		v = "b"
	}
	if bx.CX() < 0.33 {
		h = "l"
	} else if bx.CX() > 0.66 {
		h = "r"
	}
	if v == "m" && h == "c" {
		return "center"
	}
	return v + h
}
