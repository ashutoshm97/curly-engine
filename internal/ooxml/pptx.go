// Package ooxml extracts a rawdeck.Deck from a .pptx/.potx package without
// any external process: themes, masters, layouts, slides, tables and media.
package ooxml

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/ashutoshm97/curly-engine/internal/blob"
	"github.com/ashutoshm97/curly-engine/internal/rawdeck"
	"github.com/ashutoshm97/curly-engine/internal/xmldom"
)

const emuPerPt = 12700.0

// Limits guard against hostile archives (zip bombs, absurd decks).
type Limits struct {
	MaxEntries      int
	MaxUncompressed int64
	MaxEntryRatio   float64
	MaxSlides       int
}

var DefaultLimits = Limits{MaxEntries: 10000, MaxUncompressed: 1 << 30, MaxEntryRatio: 250, MaxSlides: 500}

type rel struct{ Type, Target string }

type pkg struct {
	files map[string]*zip.File
	xml   map[string]*xmldom.Node
	rels  map[string]map[string]rel
	lim   Limits
	read  int64
}

func openPkg(data []byte, lim Limits) (*pkg, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("not a valid pptx (zip): %w", err)
	}
	if len(zr.File) > lim.MaxEntries {
		return nil, fmt.Errorf("too many zip entries: %d", len(zr.File))
	}
	p := &pkg{files: map[string]*zip.File{}, xml: map[string]*xmldom.Node{}, rels: map[string]map[string]rel{}, lim: lim}
	var total uint64
	for _, f := range zr.File {
		total += f.UncompressedSize64
		if f.CompressedSize64 > 0 && float64(f.UncompressedSize64)/float64(f.CompressedSize64) > lim.MaxEntryRatio && f.UncompressedSize64 > 10<<20 {
			return nil, fmt.Errorf("suspicious compression ratio in %s", f.Name)
		}
		p.files[strings.TrimPrefix(f.Name, "/")] = f
	}
	if int64(total) > lim.MaxUncompressed {
		return nil, errors.New("archive too large when uncompressed")
	}
	if _, ok := p.files["ppt/presentation.xml"]; !ok {
		return nil, errors.New("not a presentation: ppt/presentation.xml missing")
	}
	return p, nil
}

func (p *pkg) bytes(name string) ([]byte, error) {
	f, ok := p.files[name]
	if !ok {
		return nil, fmt.Errorf("missing part %s", name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, p.lim.MaxUncompressed-p.read+1))
	if err != nil {
		return nil, err
	}
	p.read += int64(len(b))
	if p.read > p.lim.MaxUncompressed {
		return nil, errors.New("archive exceeds uncompressed limit")
	}
	return b, nil
}

func (p *pkg) node(name string) (*xmldom.Node, error) {
	if n, ok := p.xml[name]; ok {
		return n, nil
	}
	b, err := p.bytes(name)
	if err != nil {
		return nil, err
	}
	n, err := xmldom.Parse(b)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}
	p.xml[name] = n
	return n, nil
}

func (p *pkg) relsOf(part string) map[string]rel {
	if r, ok := p.rels[part]; ok {
		return r
	}
	out := map[string]rel{}
	relPath := path.Join(path.Dir(part), "_rels", path.Base(part)+".rels")
	if n, err := p.node(relPath); err == nil {
		for _, r := range n.All("Relationship") {
			if r.Attr("TargetMode") == "External" {
				continue
			}
			t := r.Attr("Target")
			if strings.HasPrefix(t, "/") {
				t = strings.TrimPrefix(t, "/")
			} else {
				t = path.Clean(path.Join(path.Dir(part), t))
			}
			typ := r.Attr("Type")
			out[r.Attr("Id")] = rel{Type: typ[strings.LastIndex(typ, "/")+1:], Target: t}
		}
	}
	p.rels[part] = out
	return out
}

func (p *pkg) relOfType(part, typ string) string {
	for _, r := range p.relsOf(part) {
		if r.Type == typ {
			return r.Target
		}
	}
	return ""
}

type placeholder struct {
	typ, idx string
	box      *emuBox
	node     *xmldom.Node
}

type emuBox struct{ x, y, cx, cy float64 }

type master struct {
	part         string
	node         *xmldom.Node
	colors       *colorCtx
	placeholders []placeholder
	bg           rawdeck.Color
	decor        []*rawdeck.Element
}

type layout struct {
	part         string
	node         *xmldom.Node
	master       *master
	name, typ    string
	placeholders []placeholder
	bg           rawdeck.Color
	decor        []*rawdeck.Element
	elements     []*rawdeck.Element
}

type extractor struct {
	p           *pkg
	deck        *rawdeck.Deck
	sw, sh      float64
	masters     map[string]*master
	layouts     map[string]*layout
	tableStyles map[string]*xmldom.Node
}

// Extract parses a PPTX package into a rawdeck.Deck.
func Extract(data []byte, lim Limits) (*rawdeck.Deck, error) {
	p, err := openPkg(data, lim)
	if err != nil {
		return nil, err
	}
	pres, err := p.node("ppt/presentation.xml")
	if err != nil {
		return nil, err
	}
	sz := pres.Child("sldSz")
	cx, _ := sz.Int("cx")
	cy, _ := sz.Int("cy")
	if cx == 0 || cy == 0 {
		cx, cy = 12192000, 6858000
	}
	e := &extractor{
		p:           p,
		deck:        &rawdeck.Deck{SourceType: "pptx", SourceHash: blob.Hash(data), WidthPt: float64(cx) / emuPerPt, HeightPt: float64(cy) / emuPerPt},
		sw:          float64(cx),
		sh:          float64(cy),
		masters:     map[string]*master{},
		layouts:     map[string]*layout{},
		tableStyles: map[string]*xmldom.Node{},
	}
	if ts, err := p.node("ppt/tableStyles.xml"); err == nil {
		for _, s := range ts.All("tblStyle") {
			e.tableStyles[s.Attr("styleId")] = s
		}
	}
	presRels := p.relsOf("ppt/presentation.xml")
	var slideParts []string
	for _, id := range pres.Path("sldIdLst").All("sldId") {
		if r, ok := presRels[id.Attr("r:id")]; ok {
			slideParts = append(slideParts, r.Target)
		}
	}
	if len(slideParts) > lim.MaxSlides {
		return nil, fmt.Errorf("too many slides: %d", len(slideParts))
	}
	// Theme comes from the first master.
	for _, id := range pres.Path("sldMasterIdLst").All("sldMasterId") {
		if r, ok := presRels[id.Attr("r:id")]; ok {
			if _, err := e.master(r.Target); err != nil {
				return nil, err
			}
			break
		}
	}
	usedLayouts := []string{}
	seen := map[string]bool{}
	for i, part := range slideParts {
		s, err := e.slide(i+1, part)
		if err != nil {
			return nil, fmt.Errorf("slide %d: %w", i+1, err)
		}
		e.deck.Slides = append(e.deck.Slides, s)
		if s.LayoutRef != "" && !seen[s.LayoutRef] {
			seen[s.LayoutRef] = true
			usedLayouts = append(usedLayouts, s.LayoutRef)
		}
	}
	for _, lp := range usedLayouts {
		l := e.layouts[lp]
		e.deck.Layouts = append(e.deck.Layouts, rawdeck.DeclaredLayout{
			ID: path.Base(lp), Name: l.name, Type: l.typ, Background: l.bg, Elements: l.elements,
		})
	}
	return e.deck, nil
}

func (e *extractor) master(part string) (*master, error) {
	if m, ok := e.masters[part]; ok {
		return m, nil
	}
	n, err := e.p.node(part)
	if err != nil {
		return nil, err
	}
	m := &master{part: part, node: n}
	theme := rawdeck.Theme{}
	if tp := e.p.relOfType(part, "theme"); tp != "" {
		if tn, err := e.p.node(tp); err == nil {
			theme = parseTheme(tn)
		}
	}
	if len(e.deck.Theme.Colors) == 0 {
		e.deck.Theme = theme
	}
	cm := map[string]string{}
	if c := n.Child("clrMap"); c != nil {
		for k, v := range c.Attrs {
			cm[k] = v
		}
	}
	m.colors = &colorCtx{theme: theme, clrMap: cm}
	m.placeholders = collectPlaceholders(n)
	m.bg = e.background(n, m.colors)
	e.masters[part] = m
	ctx := &shapeCtx{e: e, colors: m.colors, part: part, inherited: true}
	for _, el := range ctx.tree(n.Path("cSld/spTree"), identity) {
		if el.PlaceholderType == "" {
			m.decor = append(m.decor, el)
		}
	}
	return m, nil
}

func (e *extractor) layout(part string) (*layout, error) {
	if l, ok := e.layouts[part]; ok {
		return l, nil
	}
	n, err := e.p.node(part)
	if err != nil {
		return nil, err
	}
	mp := e.p.relOfType(part, "slideMaster")
	m, err := e.master(mp)
	if err != nil {
		return nil, err
	}
	l := &layout{part: part, node: n, master: m, typ: n.Attr("type"), name: n.Path("cSld").Attr("name")}
	l.placeholders = collectPlaceholders(n)
	l.bg = e.background(n, m.colors)
	if l.bg.IsZero() {
		l.bg = m.bg
	}
	e.layouts[part] = l
	ctx := &shapeCtx{e: e, colors: m.colors, part: part, layout: l, master: m, keepEmpty: true}
	for _, el := range ctx.tree(n.Path("cSld/spTree"), identity) {
		if el.PlaceholderType == "" {
			el.Inherited = true
			l.decor = append(l.decor, el)
		}
		l.elements = append(l.elements, el)
	}
	if show, ok := n.Bool("showMasterSp"); !ok || show {
		l.elements = append(l.elements, m.decor...)
	}
	return l, nil
}

func (e *extractor) slide(idx int, part string) (*rawdeck.Slide, error) {
	n, err := e.p.node(part)
	if err != nil {
		return nil, err
	}
	s := &rawdeck.Slide{Index: idx}
	var l *layout
	if lp := e.p.relOfType(part, "slideLayout"); lp != "" {
		l, err = e.layout(lp)
		if err != nil {
			return nil, err
		}
		s.LayoutRef = lp
	}
	var colors *colorCtx
	var m *master
	if l != nil {
		m = l.master
		colors = m.colors
	} else {
		colors = &colorCtx{theme: e.deck.Theme}
	}
	if co := n.Child("clrMapOvr"); co != nil {
		if oc := co.Child("overrideClrMapping"); oc != nil {
			cm := map[string]string{}
			for k, v := range oc.Attrs {
				cm[k] = v
			}
			colors = &colorCtx{theme: colors.theme, clrMap: cm}
		}
	}
	s.Background = e.background(n, colors)
	if s.Background.IsZero() && l != nil {
		s.Background = l.bg
	}
	ctx := &shapeCtx{e: e, colors: colors, part: part, layout: l, master: m}
	s.Elements = ctx.tree(n.Path("cSld/spTree"), identity)
	if l != nil {
		if show, ok := n.Bool("showMasterSp"); !ok || show {
			s.Elements = append(s.Elements, l.decor...)
			if show, ok := l.node.Bool("showMasterSp"); !ok || show {
				s.Elements = append(s.Elements, m.decor...)
			}
		}
	}
	return s, nil
}

func (e *extractor) background(n *xmldom.Node, c *colorCtx) rawdeck.Color {
	bg := n.Path("cSld/bg")
	if bg == nil {
		return rawdeck.Color{}
	}
	if pr := bg.Child("bgPr"); pr != nil {
		if sf := pr.Child("solidFill"); sf != nil {
			return c.resolve(sf)
		}
		if gf := pr.Child("gradFill"); gf != nil {
			if gs := gf.Path("gsLst/gs"); gs != nil {
				return c.resolve(gs)
			}
		}
	}
	if ref := bg.Child("bgRef"); ref != nil {
		return c.resolve(ref)
	}
	return rawdeck.Color{}
}

func parseTheme(n *xmldom.Node) rawdeck.Theme {
	t := rawdeck.Theme{}
	te := n.Child("themeElements")
	order := []string{"dk1", "lt1", "dk2", "lt2", "accent1", "accent2", "accent3", "accent4", "accent5", "accent6", "hlink", "folHlink"}
	cs := te.Child("clrScheme")
	c := &colorCtx{}
	for _, name := range order {
		if cn := cs.Child(name); cn != nil {
			col := c.resolve(cn)
			if col.Hex != "" {
				t.Colors = append(t.Colors, rawdeck.NamedColor{Name: name, Hex: col.Hex})
			}
		}
	}
	fs := te.Child("fontScheme")
	t.HeadingFont = fs.Path("majorFont/latin").Attr("typeface")
	t.BodyFont = fs.Path("minorFont/latin").Attr("typeface")
	return t
}

func collectPlaceholders(n *xmldom.Node) []placeholder {
	var out []placeholder
	for _, sp := range n.Path("cSld/spTree").Find("sp") {
		ph := sp.Path("nvSpPr/nvPr/ph")
		if ph == nil {
			continue
		}
		p := placeholder{typ: phType(ph), idx: ph.Attr("idx"), node: sp}
		if b := readXfrm(sp.Path("spPr/xfrm")); b != nil {
			p.box = b
		}
		out = append(out, p)
	}
	return out
}

func phType(ph *xmldom.Node) string {
	t := ph.Attr("type")
	if t == "" {
		return "body"
	}
	return t
}

func readXfrm(x *xmldom.Node) *emuBox {
	if x == nil {
		return nil
	}
	off, ext := x.Child("off"), x.Child("ext")
	if off == nil || ext == nil {
		return nil
	}
	b := &emuBox{}
	if v, ok := off.Int("x"); ok {
		b.x = float64(v)
	}
	if v, ok := off.Int("y"); ok {
		b.y = float64(v)
	}
	if v, ok := ext.Int("cx"); ok {
		b.cx = float64(v)
	}
	if v, ok := ext.Int("cy"); ok {
		b.cy = float64(v)
	}
	return b
}

func parsePct(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
