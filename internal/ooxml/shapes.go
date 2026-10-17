package ooxml

import (
	"mime"
	"path"
	"strings"

	"github.com/ashutoshm97/curly-engine/internal/blob"
	"github.com/ashutoshm97/curly-engine/internal/rawdeck"
	"github.com/ashutoshm97/curly-engine/internal/xmldom"
)

type transform func(emuBox) emuBox

func identity(b emuBox) emuBox { return b }

type shapeCtx struct {
	e         *extractor
	colors    *colorCtx
	part      string
	layout    *layout
	master    *master
	inherited bool
	keepEmpty bool
	group     int
}

func (c *shapeCtx) tree(n *xmldom.Node, tf transform) []*rawdeck.Element {
	if n == nil {
		return nil
	}
	var out []*rawdeck.Element
	for _, ch := range n.Children {
		switch ch.Name {
		case "sp", "cxnSp":
			if el := c.shape(ch, tf); el != nil {
				out = append(out, el)
			}
		case "pic":
			if el := c.picture(ch, tf); el != nil {
				out = append(out, el)
			}
		case "graphicFrame":
			if el := c.frame(ch, tf); el != nil {
				out = append(out, el)
			}
		case "grpSp":
			out = append(out, c.groupShape(ch, tf)...)
		case "AlternateContent":
			if fb := ch.Child("Fallback"); fb != nil {
				out = append(out, c.tree(fb, tf)...)
			}
		}
	}
	return out
}

func (c *shapeCtx) groupShape(n *xmldom.Node, parent transform) []*rawdeck.Element {
	x := n.Path("grpSpPr/xfrm")
	off, ext := readXfrm(x), (*emuBox)(nil)
	if x != nil {
		if co, ce := x.Child("chOff"), x.Child("chExt"); co != nil && ce != nil {
			ext = &emuBox{}
			if v, ok := co.Int("x"); ok {
				ext.x = float64(v)
			}
			if v, ok := co.Int("y"); ok {
				ext.y = float64(v)
			}
			if v, ok := ce.Int("cx"); ok {
				ext.cx = float64(v)
			}
			if v, ok := ce.Int("cy"); ok {
				ext.cy = float64(v)
			}
		}
	}
	tf := parent
	if off != nil && ext != nil && ext.cx > 0 && ext.cy > 0 {
		sx, sy := off.cx/ext.cx, off.cy/ext.cy
		o, ch := *off, *ext
		tf = func(b emuBox) emuBox {
			return parent(emuBox{x: o.x + (b.x-ch.x)*sx, y: o.y + (b.y-ch.y)*sy, cx: b.cx * sx, cy: b.cy * sy})
		}
	}
	c.group++
	gid := c.group
	els := c.tree(n, tf)
	for _, el := range els {
		if el.GroupID == 0 {
			el.GroupID = gid
		}
	}
	return els
}

func (c *shapeCtx) norm(b emuBox, tf transform) rawdeck.Box {
	t := tf(b)
	return rawdeck.Box{X: t.x / c.e.sw, Y: t.y / c.e.sh, W: t.cx / c.e.sw, H: t.cy / c.e.sh}
}

func normalizePh(t string) string {
	switch t {
	case "ctrTitle":
		return "title"
	case "subTitle", "obj":
		return "body"
	}
	return t
}

// inheritedPh finds the layout and master placeholders a shape inherits from.
func (c *shapeCtx) inheritedPh(typ, idx string) (lay, mas *placeholder) {
	if c.layout != nil {
		for i := range c.layout.placeholders {
			p := &c.layout.placeholders[i]
			if idx != "" && p.idx == idx {
				lay = p
				break
			}
		}
		if lay == nil {
			for i := range c.layout.placeholders {
				p := &c.layout.placeholders[i]
				if p.typ == typ || (idx == "" && normalizePh(p.typ) == normalizePh(typ)) {
					lay = p
					break
				}
			}
		}
	}
	m := c.master
	if m != nil {
		want := normalizePh(typ)
		if lay != nil {
			want = normalizePh(lay.typ)
		}
		for i := range m.placeholders {
			p := &m.placeholders[i]
			if normalizePh(p.typ) == want {
				mas = p
				break
			}
		}
	}
	return
}

func roleForPh(t string) rawdeck.Role {
	switch t {
	case "title", "ctrTitle":
		return rawdeck.RoleTitle
	case "subTitle":
		return rawdeck.RoleSubtitle
	case "pic", "clipArt":
		return rawdeck.RoleImage
	case "tbl":
		return rawdeck.RoleTable
	case "chart":
		return rawdeck.RoleChart
	case "ftr", "dt":
		return rawdeck.RoleFooter
	case "sldNum":
		return rawdeck.RolePageNumber
	}
	return rawdeck.RoleBody
}

func (c *shapeCtx) shape(n *xmldom.Node, tf transform) *rawdeck.Element {
	el := &rawdeck.Element{Kind: rawdeck.KindShape, Inherited: c.inherited}
	var lay, mas *placeholder
	if ph := n.Path("nvSpPr/nvPr/ph"); ph != nil {
		el.PlaceholderType = phType(ph)
		el.Role = roleForPh(el.PlaceholderType)
		lay, mas = c.inheritedPh(el.PlaceholderType, ph.Attr("idx"))
	}
	spPr := n.Child("spPr")
	box := readXfrm(spPr.Child("xfrm"))
	if box == nil && lay != nil {
		box = lay.box
		if box == nil && mas != nil {
			box = mas.box
		}
	}
	if box == nil && mas != nil {
		box = mas.box
	}
	if box == nil {
		return nil
	}
	el.Box = c.norm(*box, tf)
	el.Geometry = spPr.Path("prstGeom").Attr("prst")
	if el.Geometry == "roundRect" {
		el.RadiusPx = 8
		if gd := spPr.Path("prstGeom/avLst/gd"); gd != nil {
			if f := strings.TrimPrefix(gd.Attr("fmla"), "val "); f != "" {
				el.RadiusPx = parsePct(f) / 50000 * min(box.cx, box.cy) / 2 / emuPerPt
			}
		}
	}
	switch {
	case spPr.Child("solidFill") != nil:
		el.Fill = c.colors.resolve(spPr.Child("solidFill"))
	case spPr.Child("gradFill") != nil:
		el.Fill = c.colors.resolve(spPr.Path("gradFill/gsLst/gs"))
	case spPr.Child("noFill") != nil:
	default:
		if ref := n.Path("style/fillRef"); ref != nil && ref.Attr("idx") != "0" {
			el.Fill = c.colors.resolve(ref)
		}
	}
	if ln := spPr.Child("ln"); ln != nil && ln.Child("noFill") == nil {
		el.Line = c.colors.resolve(ln.Child("solidFill"))
	}
	el.Shadow = spPr.Path("effectLst/outerShdw") != nil
	if tb := n.Child("txBody"); tb != nil {
		text, style := c.text(tb, el.PlaceholderType, lay, mas)
		if text != "" {
			el.Kind = rawdeck.KindText
			el.Text = text
			el.Style = style
		} else if el.PlaceholderType != "" {
			el.Style = style
		}
	}
	if el.Kind == rawdeck.KindShape && el.PlaceholderType != "" {
		if !c.keepEmpty {
			return nil
		}
		el.Kind = rawdeck.KindText
		switch el.Role {
		case rawdeck.RoleImage:
			el.Kind = rawdeck.KindImage
		case rawdeck.RoleTable:
			el.Kind = rawdeck.KindTable
		case rawdeck.RoleChart:
			el.Kind = rawdeck.KindChart
		}
	}
	if el.Kind == rawdeck.KindShape && el.Fill.IsZero() && el.Line.IsZero() {
		return nil
	}
	return el
}

// text extracts the plain text plus the effective style of the first run,
// walking shape → layout placeholder → master placeholder → master txStyles.
func (c *shapeCtx) text(tb *xmldom.Node, phTyp string, lay, mas *placeholder) (string, rawdeck.TextStyle) {
	var paras []string
	st := rawdeck.TextStyle{}
	var firstRPr, firstPPr *xmldom.Node
	maxLvl := 0
	for _, p := range tb.All("p") {
		var sb strings.Builder
		for _, ch := range p.Children {
			switch ch.Name {
			case "r", "fld":
				t := ch.Child("t")
				if t != nil {
					sb.WriteString(t.Text)
				}
				if firstRPr == nil && t != nil && strings.TrimSpace(t.Text) != "" {
					firstRPr = ch.Child("rPr")
					firstPPr = p.Child("pPr")
				}
			case "br":
				sb.WriteString("\n")
			}
		}
		if s := strings.TrimSpace(sb.String()); s != "" {
			paras = append(paras, s)
			if lv, ok := p.Child("pPr").Int("lvl"); ok && int(lv) > maxLvl {
				maxLvl = int(lv)
			}
		}
	}
	st.Paragraphs = len(paras)
	st.MaxLevel = maxLvl

	// Candidate lvl1 paragraph-property nodes, most specific first.
	var chain []*xmldom.Node
	chain = append(chain, firstPPr, tb.Path("lstStyle/lvl1pPr"))
	if lay != nil {
		chain = append(chain, lay.node.Path("txBody/lstStyle/lvl1pPr"))
	}
	if mas != nil {
		chain = append(chain, mas.node.Path("txBody/lstStyle/lvl1pPr"))
	}
	if c.master != nil {
		ts := c.master.node.Child("txStyles")
		switch normalizePh(phTyp) {
		case "title":
			chain = append(chain, ts.Path("titleStyle/lvl1pPr"))
		case "body":
			chain = append(chain, ts.Path("bodyStyle/lvl1pPr"))
		default:
			chain = append(chain, ts.Path("otherStyle/lvl1pPr"))
		}
	}
	rprs := []*xmldom.Node{firstRPr}
	for _, p := range chain {
		if p != nil {
			rprs = append(rprs, p.Child("defRPr"))
		}
	}
	for _, r := range rprs {
		if r == nil {
			continue
		}
		if st.SizePt == 0 {
			if v, ok := r.Int("sz"); ok {
				st.SizePt = float64(v) / 100
			}
		}
		if b, ok := r.Bool("b"); ok && !st.Bold && st.Color.IsZero() {
			st.Bold = b
		}
		if i, ok := r.Bool("i"); ok && !st.Italic {
			st.Italic = i
		}
		if st.Color.IsZero() {
			if sf := r.Child("solidFill"); sf != nil {
				st.Color = c.colors.resolve(sf)
			}
		}
		if st.Font == "" {
			if tf := r.Child("latin").Attr("typeface"); tf != "" {
				st.Font = tf
			}
		}
	}
	for _, p := range chain {
		if p == nil {
			continue
		}
		if st.Align == "" {
			st.Align = p.Attr("algn")
		}
		if st.Bullet == "" {
			if p.Child("buNone") != nil {
				st.Bullet = "none"
			} else if bc := p.Child("buChar"); bc != nil {
				st.Bullet = bc.Attr("char")
			} else if p.Child("buAutoNum") != nil {
				st.Bullet = "1."
			}
		}
		if bc := p.Child("buClr"); bc != nil && st.BulletColor.IsZero() {
			st.BulletColor = c.colors.resolve(bc)
		}
		if v, ok := p.Int("marL"); ok && st.IndentPt == 0 {
			st.IndentPt = float64(v) / 12700
		}
		if v, ok := p.Int("indent"); ok && v < 0 && st.HangPt == 0 {
			st.HangPt = float64(-v) / 12700
		}
		if v, ok := p.Path("spcBef/spcPts").Int("val"); ok && st.SpaceBeforePt == 0 {
			st.SpaceBeforePt = float64(v) / 100
		}
		if v, ok := p.Path("lnSpc/spcPct").Int("val"); ok && st.LineHeight == 0 {
			st.LineHeight = float64(v) / 100000
		}
	}
	if st.Bullet == "none" {
		st.Bullet = ""
	}
	theme := c.colors.theme
	switch {
	case st.Font == "+mj-lt" || (st.Font == "" && normalizePh(phTyp) == "title"):
		st.FontRole, st.Font = "heading", theme.HeadingFont
	case st.Font == "+mn-lt" || st.Font == "":
		st.FontRole, st.Font = "body", theme.BodyFont
	case st.Font == theme.HeadingFont:
		st.FontRole = "heading"
	case st.Font == theme.BodyFont:
		st.FontRole = "body"
	}
	if st.SizePt == 0 {
		st.SizePt = 18
	}
	if st.Color.IsZero() {
		st.Color = c.colors.resolve(&xmldom.Node{Children: []*xmldom.Node{{Name: "schemeClr", Attrs: map[string]string{"val": "tx1"}}}})
	}
	return strings.Join(paras, "\n"), st
}

func (c *shapeCtx) picture(n *xmldom.Node, tf transform) *rawdeck.Element {
	box := readXfrm(n.Path("spPr/xfrm"))
	if box == nil {
		if ph := n.Path("nvPicPr/nvPr/ph"); ph != nil {
			lay, mas := c.inheritedPh(phType(ph), ph.Attr("idx"))
			if lay != nil && lay.box != nil {
				box = lay.box
			} else if mas != nil {
				box = mas.box
			}
		}
	}
	if box == nil {
		return nil
	}
	el := &rawdeck.Element{Kind: rawdeck.KindImage, Role: rawdeck.RoleImage, Box: c.norm(*box, tf), Inherited: c.inherited}
	if g := n.Path("spPr/prstGeom").Attr("prst"); g != "" {
		el.Geometry = g
		if g == "roundRect" {
			el.RadiusPx = 8
		}
	}
	el.Shadow = n.Path("spPr/effectLst/outerShdw") != nil
	if ln := n.Path("spPr/ln"); ln != nil && ln.Child("solidFill") != nil {
		el.Line = c.colors.resolve(ln.Child("solidFill"))
		el.LineWidthPt = 0.75
		if w, ok := ln.Int("w"); ok {
			el.LineWidthPt = float64(w) / 12700
		}
	}
	blip := n.Path("blipFill/blip")
	el.ImageRef = c.embedAsset(blip.Attr("r:embed"))
	if ext := blip.Child("extLst"); ext != nil {
		for _, e := range ext.Children {
			if sb := e.Child("svgBlip"); sb != nil {
				el.AltImageRef = c.embedAsset(sb.Attr("r:embed"))
			}
		}
	}
	return el
}

// embedAsset stores the part behind relationship emb as a deck asset.
func (c *shapeCtx) embedAsset(emb string) string {
	if emb == "" {
		return ""
	}
	r, ok := c.e.p.relsOf(c.part)[emb]
	if !ok {
		return ""
	}
	data, err := c.e.p.bytes(r.Target)
	if err != nil {
		return ""
	}
	ref := blob.Hash(data)
	mt := mime.TypeByExtension(path.Ext(r.Target))
	if mt == "" {
		mt = "application/octet-stream"
	}
	c.e.deck.AddAsset(ref, mt, data)
	return ref
}

func (c *shapeCtx) frame(n *xmldom.Node, tf transform) *rawdeck.Element {
	box := readXfrm(n.Child("xfrm"))
	if box == nil {
		return nil
	}
	gd := n.Path("graphic/graphicData")
	uri := gd.Attr("uri")
	el := &rawdeck.Element{Box: c.norm(*box, tf), Inherited: c.inherited}
	switch {
	case strings.HasSuffix(uri, "/table"):
		el.Kind, el.Role = rawdeck.KindTable, rawdeck.RoleTable
		el.Table = c.table(gd.Child("tbl"))
	case strings.HasSuffix(uri, "/chart"):
		el.Kind, el.Role = rawdeck.KindChart, rawdeck.RoleChart
	default:
		return nil
	}
	return el
}

// builtinTableStyles approximates PowerPoint's built-in styles that are not
// serialized into tableStyles.xml. Keyed by GUID → accent token.
var builtinTableStyles = map[string]string{
	"{5C22544A-7EE6-4342-B048-85BDC9FD1C3A}": "accent1", // Medium Style 2 - Accent 1
	"{21E4AEA4-8DFA-4A89-87EB-49C32662AFE8}": "accent2",
	"{F5AB1C69-6EDB-4FF4-983F-18BD219EF322}": "accent3",
	"{00A15C55-8517-42AA-B614-E9B94910E393}": "accent4",
	"{7DF18680-E054-41AD-8BC1-D1AEF772440D}": "accent5",
	"{93296810-A885-4BE3-A3E7-6D5BEEA58F35}": "accent6",
}

func (c *shapeCtx) table(tbl *xmldom.Node) *rawdeck.Table {
	if tbl == nil {
		return nil
	}
	t := &rawdeck.Table{}
	pr := tbl.Child("tblPr")
	t.FirstRow, _ = pr.Bool("firstRow")
	t.BandRow, _ = pr.Bool("bandRow")
	if id := pr.Child("tableStyleId"); id != nil {
		t.StyleID = strings.TrimSpace(id.Text)
	}
	t.Cols = len(tbl.Path("tblGrid").All("gridCol"))
	rows := tbl.All("tr")
	t.Rows = len(rows)
	cellFill := func(r, col int) rawdeck.Color {
		if r >= len(rows) {
			return rawdeck.Color{}
		}
		tcs := rows[r].All("tc")
		if col >= len(tcs) {
			return rawdeck.Color{}
		}
		return c.colors.resolve(tcs[col].Path("tcPr/solidFill"))
	}
	runStyle := func(r int) (rawdeck.Color, bool, float64) {
		if r >= len(rows) {
			return rawdeck.Color{}, false, 0
		}
		for _, rp := range rows[r].Find("rPr") {
			col := c.colors.resolve(rp.Child("solidFill"))
			b, _ := rp.Bool("b")
			sz, _ := rp.Int("sz")
			return col, b, float64(sz) / 100
		}
		return rawdeck.Color{}, false, 0
	}
	for i, tr := range rows {
		if i >= 30 {
			break
		}
		var row []string
		for _, tc := range tr.All("tc") {
			var parts []string
			for _, tt := range tc.Find("t") {
				parts = append(parts, tt.Text)
			}
			row = append(row, strings.TrimSpace(strings.Join(parts, "")))
		}
		t.Cells = append(t.Cells, row)
	}
	// Style definition from tableStyles.xml, then built-ins.
	if def, ok := c.e.tableStyles[t.StyleID]; ok {
		t.BodyFill = c.colors.resolve(def.Path("wholeTbl/tcStyle/fill/solidFill"))
		t.BodyText = c.colors.resolve(def.Path("wholeTbl/tcTxStyle"))
		t.HeaderFill = c.colors.resolve(def.Path("firstRow/tcStyle/fill/solidFill"))
		t.HeaderText = c.colors.resolve(def.Path("firstRow/tcTxStyle"))
		t.HeaderBold = def.Path("firstRow/tcTxStyle").Attr("b") == "on"
		t.BandFill = c.colors.resolve(def.Path("band1H/tcStyle/fill/solidFill"))
		t.BorderColor = c.colors.resolve(def.Path("wholeTbl/tcStyle/tcBdr/insideH/ln/solidFill"))
	} else if acc, ok := builtinTableStyles[t.StyleID]; ok {
		mk := func(xml string) rawdeck.Color {
			n, _ := xmldom.Parse([]byte(`<f xmlns:a="a">` + xml + `</f>`))
			return c.colors.resolve(n)
		}
		t.HeaderFill = mk(`<a:schemeClr val="` + acc + `"/>`)
		t.HeaderText = mk(`<a:schemeClr val="lt1"/>`)
		t.HeaderBold = true
		t.BodyFill = mk(`<a:schemeClr val="` + acc + `"><a:tint val="20000"/></a:schemeClr>`)
		t.BandFill = mk(`<a:schemeClr val="` + acc + `"><a:tint val="40000"/></a:schemeClr>`)
		t.BodyText = mk(`<a:schemeClr val="dk1"/>`)
		t.BorderColor = mk(`<a:schemeClr val="lt1"/>`)
	}
	// Explicit per-cell formatting wins.
	if f := cellFill(0, 0); !f.IsZero() {
		t.HeaderFill = f
	}
	if f := cellFill(1, 0); !f.IsZero() {
		t.BodyFill = f
		if f2 := cellFill(2, 0); !f2.IsZero() && f2 != f {
			t.BandFill = f
			t.BodyFill = f2
		}
	}
	if col, b, sz := runStyle(0); !col.IsZero() || b {
		if !col.IsZero() {
			t.HeaderText = col
		}
		t.HeaderBold = t.HeaderBold || b
		if sz > 0 {
			t.FontSizePt = sz
		}
	}
	if col, _, sz := runStyle(1); !col.IsZero() || sz > 0 {
		if !col.IsZero() {
			t.BodyText = col
		}
		if sz > 0 {
			t.FontSizePt = sz
		}
	}
	return t
}
