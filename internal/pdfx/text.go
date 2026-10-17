package pdfx

import (
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/ashutoshm97/curly-engine/internal/rawdeck"
)

// run is one shown string: baseline start/end in device space (y up).
type run struct {
	x0, x1, y, size float64
	font            *fontInfo
	color, text     string
}

type line struct {
	runs      []run
	x0, x1, y float64
	size      float64
	textX0    float64
	bullet    string
	text      string
	font      *fontInfo
	color     string
}

var bulletGlyphs = map[string]string{
	"•": "•", "·": "•", "●": "•", "◦": "◦", "▪": "▪", "■": "▪", "–": "–", "-": "–",
	"✓": "✓", "✔": "✓", "➢": "➢", "►": "►", "▶": "►", ">": "➢",
	"\uf0fc": "✓", "\uf0a7": "▪", "\uf0d8": "➢", "\uf0b7": "•", "\uf076": "❖",
}

var symbolBullets = map[string]string{"ü": "✓", "§": "▪", "Ø": "➢", "l": "•", "n": "▪", "v": "❖", "Ÿ": "•", "q": "□"}

func bulletOf(r run) (string, bool) {
	t := strings.TrimSpace(r.text)
	if utf8.RuneCountInString(t) != 1 {
		return "", false
	}
	if r.font != nil && r.font.symbol {
		if b, ok := symbolBullets[t]; ok {
			return b, true
		}
		return "•", true
	}
	b, ok := bulletGlyphs[t]
	return b, ok
}

func buildLines(runs []run) []*line {
	sort.SliceStable(runs, func(i, j int) bool {
		if math.Abs(runs[i].y-runs[j].y) > 0.5 {
			return runs[i].y > runs[j].y
		}
		return runs[i].x0 < runs[j].x0
	})
	var lines []*line
	for _, r := range runs {
		var dst *line
		for k := len(lines) - 1; k >= 0; k-- {
			l := lines[k]
			if l.y-r.y > 2*r.size {
				break
			}
			ref := math.Min(l.size, r.size)
			if math.Abs(l.y-r.y) < 0.35*ref && r.size < l.size*1.6 && l.size < r.size*1.6 &&
				r.x0 >= l.x1-0.5*ref && r.x0-l.x1 <= 1.2*math.Max(l.size, r.size) {
				dst = l
				break
			}
		}
		if dst == nil {
			dst = &line{x0: r.x0, x1: r.x1, y: r.y, size: r.size}
			lines = append(lines, dst)
		}
		dst.runs = append(dst.runs, r)
		dst.x1 = math.Max(dst.x1, r.x1)
	}
	out := lines[:0]
	for _, l := range lines {
		if l.finish() {
			out = append(out, l)
		}
	}
	return out
}

// finish joins runs into text and picks the line's dominant style.
func (l *line) finish() bool {
	rs := l.runs
	l.textX0 = l.x0
	if len(rs) > 1 {
		if b, ok := bulletOf(rs[0]); ok {
			l.bullet, l.textX0, rs = b, rs[1].x0, rs[1:]
		}
	}
	var sb strings.Builder
	prev := rs[0].x0
	weight := map[*fontInfo]int{}
	colors := map[string]int{}
	sizes := map[float64]int{}
	for i, r := range rs {
		t := r.text
		s := sb.String()
		if i > 0 && r.x0-prev > 0.2*r.size && !strings.HasSuffix(s, " ") && !strings.HasPrefix(t, " ") {
			sb.WriteByte(' ')
		}
		sb.WriteString(t)
		prev = math.Max(prev, r.x1)
		n := utf8.RuneCountInString(strings.TrimSpace(t))
		weight[r.font] += n
		colors[r.color] += n
		sizes[math.Round(r.size*2)/2] += n
	}
	l.text = strings.Join(strings.Fields(sb.String()), " ")
	if l.bullet == "" {
		for g, b := range bulletGlyphs {
			if strings.HasPrefix(l.text, g+" ") && g != "-" && g != ">" {
				l.bullet, l.text = b, strings.TrimSpace(strings.TrimPrefix(l.text, g))
				break
			}
		}
	}
	if l.text == "" {
		return false
	}
	l.font, l.color, l.size = argmax(weight), argmax(colors), argmax(sizes)
	return true
}

func argmax[K comparable](m map[K]int) K {
	var best K
	n := -1
	for k, v := range m {
		if v > n {
			best, n = k, v
		}
	}
	return best
}

type block struct{ lines []*line }

func buildBlocks(lines []*line) []*block {
	var blocks []*block
	for _, l := range lines {
		var dst *block
		for k := len(blocks) - 1; k >= 0; k-- {
			last := blocks[k].lines[len(blocks[k].lines)-1]
			big := math.Max(last.size, l.size)
			gap := last.y - l.y
			if gap <= 0.3*big || gap > 1.75*big || math.Abs(last.size-l.size) > 0.12*big || last.color != l.color {
				continue
			}
			if l.bullet == "" && last.font != nil && l.font != nil && last.font.bold != l.font.bold {
				continue
			}
			tol := 0.6 * l.size
			left := math.Abs(l.x0-last.x0) < tol || math.Abs(l.textX0-last.textX0) < tol
			ov := math.Min(l.x1, last.x1) - math.Max(l.x0, last.x0)
			centred := math.Abs((l.x0+l.x1)/2-(last.x0+last.x1)/2) < tol && ov > 0.5*math.Min(l.x1-l.x0, last.x1-last.x0)
			if left || centred {
				dst = blocks[k]
				break
			}
		}
		if dst == nil {
			dst = &block{}
			blocks = append(blocks, dst)
		}
		dst.lines = append(dst.lines, l)
	}
	return blocks
}

func (x *interp) slide(idx int) *rawdeck.Slide {
	s := &rawdeck.Slide{Index: idx}
	s.Elements = append(s.Elements, x.items...)
	labels := map[*rawdeck.Element]int{}
	lines := buildLines(x.runs)
	x.drawnBullets(lines)
	for _, b := range buildBlocks(lines) {
		if len(s.Elements) >= x.lim.MaxElements {
			break
		}
		el := x.textElement(b)
		if img := x.chartUnder(el); img != nil {
			labels[img]++ // label baked onto a chart picture: part of the chart, not a text slot
			continue
		}
		s.Elements = append(s.Elements, el)
	}
	for img, n := range labels {
		if n >= 2 {
			img.Role = rawdeck.RoleChart
		}
	}
	return s
}

// drawnBullets attaches vector-drawn list markers (as browsers export them)
// to the line they precede.
func (x *interp) drawnBullets(lines []*line) {
	for _, l := range lines {
		if l.bullet != "" {
			continue
		}
		for _, m := range x.marks {
			cy, w := (m[1]+m[3])/2, m[2]-m[0]
			if w < 0.6*l.size && m[2] <= l.x0 && l.x0-m[2] < 2*l.size && cy > l.y && cy < l.y+0.7*l.size {
				l.bullet, l.textX0, l.x0 = "•", l.x0, m[0]
				break
			}
		}
	}
}

// chartUnder returns the large picture a short text label sits on, if any.
func (x *interp) chartUnder(t *rawdeck.Element) *rawdeck.Element {
	if t.Style.Paragraphs > 1 || len(strings.Fields(t.Text)) > 4 {
		return nil
	}
	for _, it := range x.items {
		if it.Kind != rawdeck.KindImage || it.Box.Area() < 0.1 || it.Box.Area() > 0.85 {
			continue
		}
		if t.Box.X >= it.Box.X && t.Box.Right() <= it.Box.Right() && t.Box.Y >= it.Box.Y && t.Box.Bottom() <= it.Box.Bottom() {
			return it
		}
	}
	return nil
}

func (x *interp) textElement(b *block) *rawdeck.Element {
	first, last := b.lines[0], b.lines[len(b.lines)-1]
	x0, x1 := math.Inf(1), math.Inf(-1)
	var paras []string
	bullet := ""
	fonts := map[*fontInfo]int{}
	for i, l := range b.lines {
		x0, x1 = math.Min(x0, l.x0), math.Max(x1, l.x1)
		fonts[l.font] += len(l.text)
		newPara := i == 0 || l.bullet != "" || b.lines[i-1].y-l.y > 1.45*l.size
		if l.bullet != "" && bullet == "" {
			bullet = l.bullet
		}
		if newPara {
			paras = append(paras, l.text)
		} else {
			paras[len(paras)-1] += " " + l.text
		}
	}
	f := argmax(fonts)
	el := &rawdeck.Element{
		Kind: rawdeck.KindText,
		Box:  x.box(x0, last.y-0.22*last.size, x1, first.y+0.8*first.size),
		Text: strings.Join(paras, "\n"),
		Style: rawdeck.TextStyle{SizePt: round(first.size), Color: rawdeck.Color{Hex: first.color},
			Bullet: bullet, Paragraphs: len(paras)},
	}
	if f != nil {
		el.Style.Font, el.Style.Bold, el.Style.Italic = f.family, f.bold, f.italic
	}
	el.Style.Align = x.align(b, el.Box)
	return el
}

func (x *interp) align(b *block, bx rawdeck.Box) string {
	if len(b.lines) > 1 {
		spread := func(v func(*line) float64) float64 {
			lo, hi := math.Inf(1), math.Inf(-1)
			for _, l := range b.lines {
				lo, hi = math.Min(lo, v(l)), math.Max(hi, v(l))
			}
			return hi - lo
		}
		tol := 0.5 * b.lines[0].size
		switch {
		case spread(func(l *line) float64 { return l.textX0 }) <= tol:
			return "l"
		case spread(func(l *line) float64 { return (l.x0 + l.x1) / 2 }) <= tol:
			return "ctr"
		case spread(func(l *line) float64 { return l.x1 }) <= tol:
			return "r"
		}
		return "l"
	}
	// Single line: centred inside the smallest filled shape that contains it.
	var host *rawdeck.Element
	for _, it := range x.items {
		if it.Kind != rawdeck.KindShape || it.Fill.IsZero() || it.Box.Area() > 0.5 {
			continue
		}
		if bx.CX() > it.Box.X && bx.CX() < it.Box.Right() && bx.CY() > it.Box.Y && bx.CY() < it.Box.Bottom() &&
			(host == nil || it.Box.Area() < host.Box.Area()) {
			host = it
		}
	}
	if host != nil && math.Abs(bx.CX()-host.Box.CX()) < 0.04*host.Box.W && bx.X-host.Box.X > 0.12*host.Box.W {
		return "ctr"
	}
	return "l"
}
