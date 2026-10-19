// Package pdfx extracts style evidence from text-layer PDFs (exported decks)
// by interpreting page content streams: glyph runs with font, size and color,
// filled and stroked paths, and image placements. It uses no OCR or vision,
// so scanned or image-only PDFs are rejected.
package pdfx

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/ledongthuc/pdf"

	"github.com/ashutoshm97/curly-engine/internal/blob"
	"github.com/ashutoshm97/curly-engine/internal/rawdeck"
)

type Limits struct {
	MaxPages        int
	MaxImagePixels  int
	MaxElements     int
	MaxFormDepth    int
	MinShapeAreaPct float64
}

var DefaultLimits = Limits{MaxPages: 300, MaxImagePixels: 25_000_000, MaxElements: 400, MaxFormDepth: 8, MinShapeAreaPct: 0.15}

// ErrNoText means the PDF has no extractable text layer.
var ErrNoText = errors.New("pdf has no text layer (scanned or image-only PDFs are not supported)")

// Extract converts a PDF into a raw deck, one slide per page.
func Extract(data []byte, lim Limits) (d *rawdeck.Deck, err error) {
	defer func() {
		if r := recover(); r != nil {
			d, err = nil, fmt.Errorf("parse pdf: %v", r)
		}
	}()
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("parse pdf: %w", err)
	}
	n := r.NumPage()
	if n == 0 {
		return nil, errors.New("pdf has no pages")
	}
	if n > lim.MaxPages {
		return nil, fmt.Errorf("pdf has %d pages (limit %d)", n, lim.MaxPages)
	}
	ox, oy, w, h := pageBox(r.Page(1))
	d = &rawdeck.Deck{SourceType: "pdf", SourceHash: blob.Hash(data), WidthPt: round(w), HeightPt: round(h)}
	texts := 0
	for i := 1; i <= n; i++ {
		pg := r.Page(i)
		x := &interp{lim: lim, deck: d, fonts: map[string]*fontInfo{}, g: gstate{ctm: ident, th: 1, fillAlpha: 1, fill: "#000000", stroke: "#000000"}}
		x.ox, x.oy, x.w, x.h = ox, oy, w, h
		if px, py, pw, ph := pageBox(pg); pw > 0 && ph > 0 {
			x.ox, x.oy, x.w, x.h = px, py, pw, ph
		}
		x.runPage(pg)
		s := x.slide(i)
		for _, el := range s.Elements {
			if el.Kind == rawdeck.KindText {
				texts++
			}
		}
		d.Slides = append(d.Slides, s)
	}
	if texts == 0 {
		return nil, ErrNoText
	}
	markRepeated(d)
	markTitles(d)
	return d, nil
}

func pageBox(p pdf.Page) (ox, oy, w, h float64) {
	var box pdf.Value
	for _, key := range []string{"CropBox", "MediaBox"} {
		for v := p.V; !v.IsNull(); v = v.Key("Parent") {
			if b := v.Key(key); b.Kind() == pdf.Array && b.Len() == 4 {
				box = b
				break
			}
		}
		if !box.IsNull() {
			break
		}
	}
	if box.IsNull() {
		return 0, 0, 612, 792
	}
	x0, y0, x1, y1 := box.Index(0).Float64(), box.Index(1).Float64(), box.Index(2).Float64(), box.Index(3).Float64()
	return math.Min(x0, x1), math.Min(y0, y1), math.Abs(x1 - x0), math.Abs(y1 - y0)
}

// mat is a PDF affine matrix [a b c d e f]; points transform as row vectors.
type mat [6]float64

var ident = mat{1, 0, 0, 1, 0, 0}

func (m mat) mul(n mat) mat {
	return mat{
		m[0]*n[0] + m[1]*n[2], m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2], m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4], m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

func (m mat) apply(x, y float64) (float64, float64) {
	return x*m[0] + y*m[2] + m[4], x*m[1] + y*m[3] + m[5]
}

func matOf(args []pdf.Value) (mat, bool) {
	if len(args) != 6 {
		return ident, false
	}
	var m mat
	for i := range 6 {
		m[i] = args[i].Float64()
	}
	return m, true
}

type gstate struct {
	ctm          mat
	fill, stroke string
	fillAlpha    float64
	font         *fontInfo
	tfs, tc, tw  float64
	th, tl, rise float64
	mode         int
	tm, tlm      mat
	softClip     bool
}

type pathState struct {
	pts                 [][2]float64
	rects, curves, segs int
	maxH                float64
	clip                bool
}

type interp struct {
	lim         Limits
	deck        *rawdeck.Deck
	fonts       map[string]*fontInfo
	ox, oy      float64
	w, h        float64
	g           gstate
	stack       []gstate
	path        pathState
	runs        []run
	marks       [][4]float64 // small filled dots/squares in device space: candidate list bullets
	items       []*rawdeck.Element
	depth       int
	unsupported int
}

func (x *interp) runPage(p pdf.Page) {
	defer func() {
		if r := recover(); r != nil {
			x.unsupported++ // keep whatever was gathered before the bad operator
		}
	}()
	x.run(p.Resources(), p.V.Key("Contents"))
}

func (x *interp) run(res, strm pdf.Value) {
	if strm.Kind() == pdf.Array {
		for i := 0; i < strm.Len(); i++ {
			x.run(res, strm.Index(i))
		}
		return
	}
	pdf.Interpret(strm, func(stk *pdf.Stack, op string) {
		n := stk.Len()
		args := make([]pdf.Value, n)
		for i := n - 1; i >= 0; i-- {
			args[i] = stk.Pop()
		}
		num := func(i int) float64 {
			if i < len(args) {
				return args[i].Float64()
			}
			return 0
		}
		g := &x.g
		switch op {
		case "q":
			x.stack = append(x.stack, x.g)
		case "Q":
			if k := len(x.stack); k > 0 {
				x.g, x.stack = x.stack[k-1], x.stack[:k-1]
			}
		case "cm":
			if m, ok := matOf(args); ok {
				g.ctm = m.mul(g.ctm)
			}
		case "gs":
			if len(args) == 1 {
				gsd := res.Key("ExtGState").Key(args[0].Name())
				if ca := gsd.Key("ca"); ca.Kind() == pdf.Integer || ca.Kind() == pdf.Real {
					g.fillAlpha = ca.Float64()
				}
				if gsd.Key("SMask").Kind() == pdf.Dict {
					g.softClip = true // soft-masked paint: shadows, glows, gradient strips
				}
			}
		case "g", "rg", "k", "sc", "scn":
			g.fill = colorOf(args)
		case "G", "RG", "K", "SC", "SCN":
			g.stroke = colorOf(args)
		case "cs":
			g.fill = "#000000"
		case "CS":
			g.stroke = "#000000"
		case "m":
			x.addPt(num(0), num(1))
		case "l":
			if k := len(x.path.pts); k > 0 {
				p := x.path.pts[k-1]
				x.addPt(num(0), num(1))
				q := x.path.pts[k]
				if math.Abs(q[1]-p[1]) < 0.5 {
					x.path.maxH = math.Max(x.path.maxH, math.Abs(q[0]-p[0]))
				}
			} else {
				x.addPt(num(0), num(1))
			}
			x.path.segs++
		case "c":
			x.addPt(num(0), num(1))
			x.addPt(num(2), num(3))
			x.addPt(num(4), num(5))
			x.path.curves++
		case "v", "y":
			x.addPt(num(0), num(1))
			x.addPt(num(2), num(3))
			x.path.curves++
		case "re":
			a, b, w, h := num(0), num(1), num(2), num(3)
			x.addPt(a, b)
			x.addPt(a+w, b)
			x.addPt(a+w, b+h)
			x.addPt(a, b+h)
			x.path.rects++
		case "f", "F", "f*":
			x.paint(true, false)
		case "B", "B*", "b", "b*":
			x.paint(true, true)
		case "S", "s":
			x.paint(false, true)
		case "W", "W*":
			x.path.clip = true
		case "n":
			if x.path.clip && (x.path.curves > 0 || x.path.rects == 0 && len(x.path.pts) > 5) {
				g.softClip = true // shapes drawn inside a free-form clip are gradient strips or icon fills
			}
			x.path = pathState{}
		case "BT":
			g.tm, g.tlm = ident, ident
		case "Tf":
			if len(args) == 2 {
				g.font = x.font(res, args[0].Name())
				g.tfs = num(1)
			}
		case "Tc":
			g.tc = num(0)
		case "Tw":
			g.tw = num(0)
		case "Tz":
			g.th = num(0) / 100
		case "TL":
			g.tl = num(0)
		case "Ts":
			g.rise = num(0)
		case "Tr":
			g.mode = int(num(0))
		case "TD", "Td":
			if op == "TD" {
				g.tl = -num(1)
			}
			g.tlm = mat{1, 0, 0, 1, num(0), num(1)}.mul(g.tlm)
			g.tm = g.tlm
		case "Tm":
			if m, ok := matOf(args); ok {
				g.tm, g.tlm = m, m
			}
		case "T*":
			x.nextLine()
		case "Tj":
			if len(args) == 1 {
				x.show(args[0].RawString())
			}
		case "'":
			x.nextLine()
			if len(args) == 1 {
				x.show(args[0].RawString())
			}
		case "\"":
			if len(args) == 3 {
				g.tw, g.tc = num(0), num(1)
				x.nextLine()
				x.show(args[2].RawString())
			}
		case "TJ":
			if len(args) == 1 {
				a := args[0]
				for i := 0; i < a.Len(); i++ {
					v := a.Index(i)
					if v.Kind() == pdf.String {
						x.show(v.RawString())
					} else {
						g.tm = mat{1, 0, 0, 1, -v.Float64() / 1000 * g.tfs * g.th, 0}.mul(g.tm)
					}
				}
			}
		case "Do":
			if len(args) == 1 {
				x.do(res, args[0].Name())
			}
		}
	})
}

func (x *interp) nextLine() {
	x.g.tlm = mat{1, 0, 0, 1, 0, -x.g.tl}.mul(x.g.tlm)
	x.g.tm = x.g.tlm
}

func (x *interp) addPt(px, py float64) {
	dx, dy := x.g.ctm.apply(px, py)
	x.path.pts = append(x.path.pts, [2]float64{dx, dy})
}

// box converts device-space extents (y up) into a slide-fraction box (y down).
func (x *interp) box(x0, y0, x1, y1 float64) rawdeck.Box {
	return clip(rawdeck.Box{
		X: (x0 - x.ox) / x.w, Y: (x.h - (y1 - x.oy)) / x.h,
		W: (x1 - x0) / x.w, H: (y1 - y0) / x.h,
	})
}

func (x *interp) paint(fill, stroke bool) {
	p := x.path
	x.path = pathState{}
	if len(p.pts) < 2 || len(x.items) >= x.lim.MaxElements || x.g.softClip {
		return
	}
	fillHex, lineHex := "", ""
	if fill && x.g.fillAlpha >= 0.15 {
		fillHex = x.g.fill
	}
	if stroke {
		lineHex = x.g.stroke
	}
	if fillHex == "" && lineHex == "" {
		return
	}
	x0, y0, x1, y1 := bounds(p.pts)
	b := x.box(x0, y0, x1, y1)
	if b.W <= 0 && b.H <= 0 {
		return
	}
	if fillHex != "" && b.Area()*100 < x.lim.MinShapeAreaPct {
		if w, h := x1-x0, y1-y0; w > 1 && w < 12 && h > 1 && h < 12 && math.Abs(w-h) < 0.3*w {
			x.marks = append(x.marks, [4]float64{x0, y0, x1, y1})
		}
		return
	}
	if fillHex == "" && b.W < 0.05 && b.H < 0.05 {
		return
	}
	geom := ""
	switch {
	case p.curves == 0 && (p.rects > 0 || len(p.pts) <= 5):
		geom = "rect"
	case p.curves == 4 && p.segs == 0 && p.rects == 0:
		geom = "ellipse"
	case p.curves == 4 && p.segs >= 3 && p.rects == 0:
		geom = "roundRect"
	default:
		return // free-form paths (arrows, wedges, icons) have no shape equivalent
	}
	el := &rawdeck.Element{Kind: rawdeck.KindShape, Box: b, Geometry: geom}
	if fillHex != "" {
		el.Fill = rawdeck.Color{Hex: fillHex}
	}
	if lineHex != "" {
		el.Line = rawdeck.Color{Hex: lineHex}
	}
	if geom == "roundRect" {
		if p.maxH > 0 && p.maxH < x1-x0 {
			el.RadiusPx = round((x1 - x0 - p.maxH) / 2)
		} else {
			el.RadiusPx = 8
		}
	}
	for _, o := range x.items {
		if o.Kind == rawdeck.KindShape && o.Fill == el.Fill && o.Line == el.Line && iou(o.Box, el.Box) > 0.9 {
			return
		}
	}
	x.items = append(x.items, el)
}

func iou(a, b rawdeck.Box) float64 {
	w := math.Min(a.Right(), b.Right()) - math.Max(a.X, b.X)
	h := math.Min(a.Bottom(), b.Bottom()) - math.Max(a.Y, b.Y)
	if w <= 0 || h <= 0 {
		return 0
	}
	in := w * h
	return in / (a.Area() + b.Area() - in)
}

func bounds(pts [][2]float64) (x0, y0, x1, y1 float64) {
	x0, y0, x1, y1 = math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range pts {
		x0, x1 = math.Min(x0, p[0]), math.Max(x1, p[0])
		y0, y1 = math.Min(y0, p[1]), math.Max(y1, p[1])
	}
	return
}

func (x *interp) do(res pdf.Value, name string) {
	xo := res.Key("XObject").Key(name)
	switch xo.Key("Subtype").Name() {
	case "Image":
		if len(x.items) >= x.lim.MaxElements {
			return
		}
		var pts [][2]float64
		for _, c := range [][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}} {
			dx, dy := x.g.ctm.apply(c[0], c[1])
			pts = append(pts, [2]float64{dx, dy})
		}
		x0, y0, x1, y1 := bounds(pts)
		el := &rawdeck.Element{Kind: rawdeck.KindImage, Box: x.box(x0, y0, x1, y1)}
		if el.Box.Area() < 0.0005 {
			return
		}
		if ref, data, ok := decodeImage(xo, x.lim.MaxImagePixels); ok {
			x.deck.AddAsset(ref, "image/png", data)
			el.ImageRef = ref
		}
		x.items = append(x.items, el)
	case "Form":
		if x.depth >= x.lim.MaxFormDepth {
			return
		}
		saved, savedStack := x.g, x.stack
		if m, ok := matOf(arrayValues(xo.Key("Matrix"))); ok {
			x.g.ctm = m.mul(x.g.ctm)
		}
		fres := xo.Key("Resources")
		if fres.IsNull() {
			fres = res
		}
		if x.depth > 0 && xo.Key("Group").Key("S").Name() == "Transparency" {
			x.g.softClip = true // nested transparency groups are export effects (shadows, gradients)
		}
		x.depth++
		x.run(fres, xo)
		x.depth--
		x.g, x.stack = saved, savedStack
	}
}

func arrayValues(v pdf.Value) []pdf.Value {
	if v.Kind() != pdf.Array {
		return nil
	}
	out := make([]pdf.Value, v.Len())
	for i := range out {
		out[i] = v.Index(i)
	}
	return out
}

func colorOf(args []pdf.Value) string {
	var c []float64
	for _, a := range args {
		switch a.Kind() {
		case pdf.Integer, pdf.Real:
			c = append(c, a.Float64())
		default:
			return "" // pattern or named colour: no solid equivalent
		}
	}
	switch len(c) {
	case 1:
		return hex(c[0], c[0], c[0])
	case 3:
		return hex(c[0], c[1], c[2])
	case 4:
		k := c[3]
		return hex((1-c[0])*(1-k), (1-c[1])*(1-k), (1-c[2])*(1-k))
	}
	return ""
}

func hex(r, g, b float64) string {
	cl := func(v float64) int { return int(math.Round(math.Max(0, math.Min(1, v)) * 255)) }
	return fmt.Sprintf("#%02X%02X%02X", cl(r), cl(g), cl(b))
}

func round(v float64) float64 { return math.Round(v*10) / 10 }

func clip(b rawdeck.Box) rawdeck.Box {
	x2, y2 := math.Min(1, b.Right()), math.Min(1, b.Bottom())
	b.X, b.Y = math.Max(0, b.X), math.Max(0, b.Y)
	b.W, b.H = math.Max(0, x2-b.X), math.Max(0, y2-b.Y)
	return b
}

// markRepeated flags elements that recur unchanged on many pages (logos,
// footers, frames) as Inherited, standing in for PPTX masters.
func markRepeated(d *rawdeck.Deck) {
	n := len(d.Slides)
	if n < 3 {
		return
	}
	sig := func(el *rawdeck.Element) string {
		b := el.Box
		return fmt.Sprintf("%s|%.2f|%.2f|%.2f|%.2f|%s|%s|%s", el.Kind, b.X, b.Y, b.W, b.H, strings.TrimSpace(el.Text), el.Fill.Hex, el.ImageRef)
	}
	count := map[string]int{}
	for _, s := range d.Slides {
		seen := map[string]bool{}
		for _, el := range s.Elements {
			k := sig(el)
			if !seen[k] {
				seen[k] = true
				count[k]++
			}
		}
	}
	for _, s := range d.Slides {
		for _, el := range s.Elements {
			if el.Box.Area() < 0.85 && count[sig(el)]*2 >= n {
				el.Inherited = true
			}
		}
	}
}

// markTitles finds the text style that sits at the same top-of-slide spot on
// many pages (the de-facto title placeholder) and marks those elements as
// titles, so large numbers or headlines elsewhere are not mistaken for them.
func markTitles(d *rawdeck.Deck) {
	type cluster struct {
		ref    *rawdeck.Element
		slides map[int]bool
	}
	same := func(a, b *rawdeck.Element) bool {
		return a.Style.Font == b.Style.Font && a.Style.Bold == b.Style.Bold && a.Style.Color == b.Style.Color &&
			math.Abs(a.Style.SizePt-b.Style.SizePt) < 1 && math.Abs(a.Box.X-b.Box.X) < 0.015 && math.Abs(a.Box.Y-b.Box.Y) < 0.015
	}
	cand := func(el *rawdeck.Element) bool {
		return el.Kind == rawdeck.KindText && !el.Inherited && el.Box.Y < 0.2 && el.Style.SizePt >= 18 && el.Style.Paragraphs <= 2
	}
	var cs []*cluster
	for i, s := range d.Slides {
		for _, el := range s.Elements {
			if !cand(el) {
				continue
			}
			var c *cluster
			for _, o := range cs {
				if same(o.ref, el) {
					c = o
					break
				}
			}
			if c == nil {
				c = &cluster{ref: el, slides: map[int]bool{}}
				cs = append(cs, c)
			}
			c.slides[i] = true
		}
	}
	var best *cluster
	for _, c := range cs {
		if best == nil || len(c.slides) > len(best.slides) ||
			len(c.slides) == len(best.slides) && c.ref.Box.Y < best.ref.Box.Y {
			best = c
		}
	}
	if best == nil || len(best.slides) < 3 && len(best.slides)*4 < len(d.Slides) {
		return
	}
	for _, s := range d.Slides {
		for _, el := range s.Elements {
			if cand(el) && same(best.ref, el) {
				el.Role, el.PlaceholderType = rawdeck.RoleTitle, "title"
				break
			}
		}
	}
}
