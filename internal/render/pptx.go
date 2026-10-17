package render

import (
	"archive/zip"
	"bytes"
	"fmt"
	"html"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	"github.com/ashutoshm97/curly-engine/internal/profile"
	"github.com/ashutoshm97/curly-engine/internal/scene"
)

const (
	nsA   = `xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"`
	nsR   = `xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	nsP   = `xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"`
	nsAll = nsA + " " + nsR + " " + nsP
	relNS = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/"
	hdr   = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
)

// Blobs loads asset bytes by ref.
type Blobs func(ref string) ([]byte, error)

func emu(pt float64) int64 { return int64(pt*12700 + 0.5) }

func esc(s string) string { return html.EscapeString(s) }

func hex6(c string) string { return strings.ToUpper(strings.TrimPrefix(c, "#")) }

// PPTX writes an editable PowerPoint file: theme colors/fonts come from the
// profile, every element becomes a native shape, picture or table.
func PPTX(d *scene.Deck, p *profile.StyleProfile, blobs Blobs) ([]byte, error) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	put := func(name, body string) error {
		w, err := z.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(body))
		return err
	}
	media := map[string]string{} // ref -> part name
	exts := map[string]bool{}
	var slideRels []string
	for i, s := range d.Slides {
		rels := []string{fmt.Sprintf(`<Relationship Id="rId1" Type="%sslideLayout" Target="../slideLayouts/slideLayout1.xml"/>`, relNS)}
		body, err := slideXML(s, blobs, func(ref string, data []byte) string {
			name, ok := media[ref]
			if !ok {
				ext := "png"
				if _, f, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
					ext = f
				}
				exts[ext] = true
				name = fmt.Sprintf("image%d.%s", len(media)+1, ext)
				media[ref] = name
				if err := put("ppt/media/"+name, string(data)); err != nil {
					return ""
				}
			}
			id := fmt.Sprintf("rId%d", len(rels)+1)
			rels = append(rels, fmt.Sprintf(`<Relationship Id="%s" Type="%simage" Target="../media/%s"/>`, id, relNS, name))
			return id
		})
		if err != nil {
			return nil, err
		}
		if err := put(fmt.Sprintf("ppt/slides/slide%d.xml", i+1), body); err != nil {
			return nil, err
		}
		if err := put(fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", i+1), relsXML(rels)); err != nil {
			return nil, err
		}
		slideRels = append(slideRels, fmt.Sprintf(`<Relationship Id="rId%d" Type="%sslide" Target="slides/slide%d.xml"/>`, i+3, relNS, i+1))
	}

	var ct strings.Builder
	ct.WriteString(hdr + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	ct.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/>`)
	for ext := range exts {
		mime := "image/" + ext
		fmt.Fprintf(&ct, `<Default Extension="%s" ContentType="%s"/>`, ext, mime)
	}
	ct.WriteString(`<Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/>`)
	ct.WriteString(`<Override PartName="/ppt/slideMasters/slideMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"/>`)
	ct.WriteString(`<Override PartName="/ppt/slideLayouts/slideLayout1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"/>`)
	ct.WriteString(`<Override PartName="/ppt/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/>`)
	for i := range d.Slides {
		fmt.Fprintf(&ct, `<Override PartName="/ppt/slides/slide%d.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>`, i+1)
	}
	ct.WriteString(`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>`)
	ct.WriteString(`<Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/></Types>`)

	var ids strings.Builder
	for i := range d.Slides {
		fmt.Fprintf(&ids, `<p:sldId id="%d" r:id="rId%d"/>`, 256+i, i+3)
	}
	parts := []struct{ name, body string }{
		{"[Content_Types].xml", ct.String()},
		{"_rels/.rels", relsXML([]string{
			fmt.Sprintf(`<Relationship Id="rId1" Type="%sofficeDocument" Target="ppt/presentation.xml"/>`, relNS),
			`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>`,
			fmt.Sprintf(`<Relationship Id="rId3" Type="%sextended-properties" Target="docProps/app.xml"/>`, relNS),
		})},
		{"docProps/core.xml", hdr + `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>` + esc(d.Title) + `</dc:title><dc:creator>curly-engine</dc:creator></cp:coreProperties>`},
		{"docProps/app.xml", hdr + fmt.Sprintf(`<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties"><Application>curly-engine</Application><Slides>%d</Slides></Properties>`, len(d.Slides))},
		{"ppt/presentation.xml", hdr + fmt.Sprintf(`<p:presentation %s saveSubsetFonts="1"><p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst><p:sldIdLst>%s</p:sldIdLst><p:sldSz cx="%d" cy="%d"/><p:notesSz cx="6858000" cy="9144000"/></p:presentation>`,
			nsAll, ids.String(), emu(d.WidthPt), emu(d.HeightPt))},
		{"ppt/_rels/presentation.xml.rels", relsXML(append([]string{
			fmt.Sprintf(`<Relationship Id="rId1" Type="%sslideMaster" Target="slideMasters/slideMaster1.xml"/>`, relNS),
			fmt.Sprintf(`<Relationship Id="rId2" Type="%stheme" Target="theme/theme1.xml"/>`, relNS),
		}, slideRels...))},
		{"ppt/slideMasters/slideMaster1.xml", hdr + `<p:sldMaster ` + nsAll + `><p:cSld><p:bg><p:bgRef idx="1001"><a:schemeClr val="bg1"/></p:bgRef></p:bg><p:spTree>` + grpHdr + `</p:spTree></p:cSld>` +
			`<p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2" accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" hlink="hlink" folHlink="folHlink"/>` +
			`<p:sldLayoutIdLst><p:sldLayoutId id="2147483649" r:id="rId1"/></p:sldLayoutIdLst><p:txStyles><p:titleStyle/><p:bodyStyle/><p:otherStyle/></p:txStyles></p:sldMaster>`},
		{"ppt/slideMasters/_rels/slideMaster1.xml.rels", relsXML([]string{
			fmt.Sprintf(`<Relationship Id="rId1" Type="%sslideLayout" Target="../slideLayouts/slideLayout1.xml"/>`, relNS),
			fmt.Sprintf(`<Relationship Id="rId2" Type="%stheme" Target="../theme/theme1.xml"/>`, relNS),
		})},
		{"ppt/slideLayouts/slideLayout1.xml", hdr + `<p:sldLayout ` + nsAll + ` type="blank" preserve="1"><p:cSld name="Blank"><p:spTree>` + grpHdr + `</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`},
		{"ppt/slideLayouts/_rels/slideLayout1.xml.rels", relsXML([]string{
			fmt.Sprintf(`<Relationship Id="rId1" Type="%sslideMaster" Target="../slideMasters/slideMaster1.xml"/>`, relNS),
		})},
		{"ppt/theme/theme1.xml", themeXML(p)},
	}
	for _, pt := range parts {
		if err := put(pt.name, pt.body); err != nil {
			return nil, err
		}
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

const grpHdr = `<p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>`

func relsXML(rels []string) string {
	return hdr + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + strings.Join(rels, "") + `</Relationships>`
}

func themeXML(p *profile.StyleProfile) string {
	defaults := map[string]string{"dk1": "000000", "lt1": "FFFFFF", "dk2": "44546A", "lt2": "E7E6E6", "accent1": "4472C4", "accent2": "ED7D31",
		"accent3": "A5A5A5", "accent4": "FFC000", "accent5": "5B9BD5", "accent6": "70AD47", "hlink": "0563C1", "folHlink": "954F72"}
	var c strings.Builder
	for _, n := range []string{"dk1", "lt1", "dk2", "lt2", "accent1", "accent2", "accent3", "accent4", "accent5", "accent6", "hlink", "folHlink"} {
		v := defaults[n]
		for _, t := range p.Tokens.Colors {
			if t.Name == n {
				v = hex6(t.Value)
			}
		}
		fmt.Fprintf(&c, `<a:%s><a:srgbClr val="%s"/></a:%s>`, n, v, n)
	}
	fill := `<a:solidFill><a:schemeClr val="phClr"/></a:solidFill>`
	ln := `<a:ln w="9525"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:ln>`
	eff := `<a:effectStyle><a:effectLst/></a:effectStyle>`
	return hdr + `<a:theme ` + nsA + ` name="` + esc(p.Name) + `"><a:themeElements><a:clrScheme name="` + esc(p.Name) + `">` + c.String() + `</a:clrScheme>` +
		`<a:fontScheme name="` + esc(p.Name) + `"><a:majorFont><a:latin typeface="` + esc(p.Font("heading").Family) + `"/><a:ea typeface=""/><a:cs typeface=""/></a:majorFont>` +
		`<a:minorFont><a:latin typeface="` + esc(p.Font("body").Family) + `"/><a:ea typeface=""/><a:cs typeface=""/></a:minorFont></a:fontScheme>` +
		`<a:fmtScheme name="Office"><a:fillStyleLst>` + strings.Repeat(fill, 3) + `</a:fillStyleLst><a:lnStyleLst>` + strings.Repeat(ln, 3) + `</a:lnStyleLst>` +
		`<a:effectStyleLst>` + strings.Repeat(eff, 3) + `</a:effectStyleLst><a:bgFillStyleLst>` + strings.Repeat(fill, 3) + `</a:bgFillStyleLst></a:fmtScheme>` +
		`</a:themeElements><a:objectDefaults/><a:extraClrSchemeLst/></a:theme>`
}

func xfrm(e *scene.Element) string {
	return fmt.Sprintf(`<a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm>`, emu(e.X), emu(e.Y), emu(e.W), emu(e.H))
}

func solid(c string) string {
	if c == "" {
		return `<a:noFill/>`
	}
	return fmt.Sprintf(`<a:solidFill><a:srgbClr val="%s"/></a:solidFill>`, hex6(c))
}

func geom(g string, radius, w, h float64) string {
	switch g {
	case "roundRect":
		adj := 16667
		if radius > 0 && min(w, h) > 0 {
			adj = int(min(50000, radius/min(w, h)*100000))
		}
		return fmt.Sprintf(`<a:prstGeom prst="roundRect"><a:avLst><a:gd name="adj" fmla="val %d"/></a:avLst></a:prstGeom>`, adj)
	case "ellipse":
		return `<a:prstGeom prst="ellipse"><a:avLst/></a:prstGeom>`
	}
	return `<a:prstGeom prst="rect"><a:avLst/></a:prstGeom>`
}

func slideXML(s *scene.Slide, blobs Blobs, embed func(ref string, data []byte) string) (string, error) {
	var b strings.Builder
	b.WriteString(hdr + `<p:sld ` + nsAll + `><p:cSld><p:bg><p:bgPr>` + solid(s.Background) + `<a:effectLst/></p:bgPr></p:bg><p:spTree>` + grpHdr)
	id := 2
	for _, e := range s.Elements {
		name := esc(e.Role)
		if e.Slot != "" {
			name = esc(e.Slot)
		}
		switch e.Kind {
		case "shape":
			sh := e.Shape
			effect := ""
			if sh.Shadow {
				effect = `<a:effectLst><a:outerShdw blurRad="76200" dist="25400" dir="5400000" algn="t" rotWithShape="0"><a:srgbClr val="000000"><a:alpha val="18000"/></a:srgbClr></a:outerShdw></a:effectLst>`
			}
			line := `<a:ln><a:noFill/></a:ln>`
			if sh.Line != "" {
				line = `<a:ln w="9525">` + solid(sh.Line) + `</a:ln>`
			}
			fmt.Fprintf(&b, `<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr>%s%s%s%s%s</p:spPr></p:sp>`,
				id, name, xfrm(e), geom(sh.Geometry, sh.RadiusPt, e.W, e.H), solid(sh.Fill), line, effect)
		case "text":
			fmt.Fprintf(&b, `<p:sp><p:nvSpPr><p:cNvPr id="%d" name="%s"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr><p:spPr>%s<a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/></p:spPr>%s</p:sp>`,
				id, name, xfrm(e), txBody(e.Text))
		case "image":
			im := e.Image
			var data []byte
			if im.Ref != "" && blobs != nil {
				data, _ = blobs(im.Ref)
			}
			if data == nil {
				ph := &scene.Text{Paragraphs: []string{im.Placeholder}, Font: "Arial", SizePt: 11, LineHeight: 1.2, Color: im.TextColor, Align: "ctr", InsetX: 7.2, InsetY: 3.6}
				fmt.Fprintf(&b, `<p:sp><p:nvSpPr><p:cNvPr id="%d" name="Image placeholder"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr>%s%s%s%s</p:spPr>%s</p:sp>`,
					id, xfrm(e), geom("roundRect", im.RadiusPt, e.W, e.H), solid(im.Fill), lnXML(im.Line, im.LineWidthPt), strings.Replace(txBody(ph), `anchor="t"`, `anchor="ctr"`, 1))
				break
			}
			rid := embed(im.Ref, data)
			box := *e
			if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil && cfg.Width > 0 && cfg.Height > 0 {
				// Contain the picture inside the slot, centred.
				ar := float64(cfg.Width) / float64(cfg.Height)
				if e.W/e.H > ar {
					box.W = e.H * ar
					box.X = e.X + (e.W-box.W)/2
				} else {
					box.H = e.W / ar
					box.Y = e.Y + (e.H-box.H)/2
				}
			}
			fmt.Fprintf(&b, `<p:pic><p:nvPicPr><p:cNvPr id="%d" name="%s"/><p:cNvPicPr><a:picLocks noChangeAspect="1"/></p:cNvPicPr><p:nvPr/></p:nvPicPr><p:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></p:blipFill><p:spPr>%s<a:prstGeom prst="rect"><a:avLst/></a:prstGeom>%s</p:spPr></p:pic>`,
				id, name, rid, xfrm(&box), picLine(im))
		case "table":
			b.WriteString(tableXML(e, id, name))
		}
		id++
	}
	b.WriteString(`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`)
	return b.String(), nil
}

func algn(a string) string {
	switch a {
	case "ctr", "r", "just":
		return a
	}
	return "l"
}

func runProps(font string, size float64, bold, italic bool, color string) string {
	attrs := fmt.Sprintf(`lang="en-US" sz="%d" dirty="0"`, int(size*100+0.5))
	if bold {
		attrs += ` b="1"`
	}
	if italic {
		attrs += ` i="1"`
	}
	return fmt.Sprintf(`<a:rPr %s>%s<a:latin typeface="%s"/></a:rPr>`, attrs, solid(color), esc(font))
}

func txBody(t *scene.Text) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<p:txBody><a:bodyPr wrap="square" lIns="%d" tIns="%d" rIns="%d" bIns="%d" anchor="t"><a:noAutofit/></a:bodyPr><a:lstStyle/>`,
		emu(t.InsetX), emu(t.InsetY), emu(t.InsetX), emu(t.InsetY))
	lh := int(t.LineHeight * 100000)
	if lh <= 0 {
		lh = 100000
	}
	for _, para := range t.Paragraphs {
		ppr := fmt.Sprintf(`algn="%s"`, algn(t.Align))
		inner := fmt.Sprintf(`<a:lnSpc><a:spcPct val="%d"/></a:lnSpc>`, lh)
		if t.Bullet != "" {
			ind, hang := emu(t.SizePt*1.1), emu(t.SizePt*1.1)
			if t.IndentPt > 0 {
				ind, hang = emu(t.IndentPt), emu(t.IndentPt)
				if t.HangPt > 0 {
					hang = emu(t.HangPt)
				}
			}
			ppr += fmt.Sprintf(` marL="%d" indent="-%d"`, ind, hang)
			bef := int(t.SizePt * t.LineHeight * 35)
			if t.SpaceBefore > 0 {
				bef = int(t.SpaceBefore * 100)
			}
			inner += `<a:spcBef><a:spcPts val="` + fmt.Sprint(bef) + `"/></a:spcBef>`
			if t.BulletColor != "" {
				inner += `<a:buClr><a:srgbClr val="` + hex6(t.BulletColor) + `"/></a:buClr>`
			}
			inner += `<a:buFont typeface="Arial"/><a:buChar char="` + esc(t.Bullet) + `"/>`
		} else {
			inner += `<a:buNone/>`
		}
		fmt.Fprintf(&b, `<a:p><a:pPr %s>%s</a:pPr><a:r>%s<a:t>%s</a:t></a:r></a:p>`, ppr, inner, runProps(t.Font, t.SizePt, t.Bold, t.Italic, t.Color), esc(para))
	}
	if len(t.Paragraphs) == 0 {
		b.WriteString(`<a:p/>`)
	}
	b.WriteString(`</p:txBody>`)
	return b.String()
}

func tableXML(e *scene.Element, id int, name string) string {
	t := e.Table
	cols := len(t.Header)
	rows := append([][]string{t.Header}, t.Rows...)
	rh := min(e.H/float64(len(rows)), t.SizePt*2.2)
	var b strings.Builder
	fmt.Fprintf(&b, `<p:graphicFrame><p:nvGraphicFramePr><p:cNvPr id="%d" name="%s"/><p:cNvGraphicFramePr><a:graphicFrameLocks noGrp="1"/></p:cNvGraphicFramePr><p:nvPr/></p:nvGraphicFramePr>`, id, name)
	fmt.Fprintf(&b, `<p:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></p:xfrm>`, emu(e.X), emu(e.Y), emu(e.W), emu(rh*float64(len(rows))))
	b.WriteString(`<a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/table"><a:tbl><a:tblPr firstRow="1" bandRow="1"/><a:tblGrid>`)
	cw := emu(e.W / float64(cols))
	for range cols {
		fmt.Fprintf(&b, `<a:gridCol w="%d"/>`, cw)
	}
	b.WriteString(`</a:tblGrid>`)
	for r, row := range rows {
		fmt.Fprintf(&b, `<a:tr h="%d">`, emu(rh))
		fill, color, bold := t.BodyFill, t.BodyText, false
		if r == 0 {
			fill, color, bold = t.HeaderFill, t.HeaderText, t.HeaderBold
		} else if t.BandFill != "" && r%2 == 1 {
			fill = t.BandFill
		}
		for c := 0; c < cols; c++ {
			v := ""
			if c < len(row) {
				v = row[c]
			}
			border := ""
			if t.Border != "" {
				border = `<a:lnB w="9525">` + solid(t.Border) + `</a:lnB>`
			}
			fmt.Fprintf(&b, `<a:tc><a:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r>%s<a:t>%s</a:t></a:r></a:p></a:txBody><a:tcPr marL="91440" marR="91440" marT="45720" marB="45720" anchor="ctr">%s%s</a:tcPr></a:tc>`,
				runProps(t.Font, t.SizePt, bold, false, color), esc(v), border, solid(fill))
		}
		b.WriteString(`</a:tr>`)
	}
	b.WriteString(`</a:tbl></a:graphicData></a:graphic></p:graphicFrame>`)
	return b.String()
}

func lnXML(color string, w float64) string {
	if color == "" {
		return `<a:ln><a:noFill/></a:ln>`
	}
	if w <= 0 {
		w = 0.75
	}
	return fmt.Sprintf(`<a:ln w="%d">%s</a:ln>`, emu(w), solid(color))
}

func picLine(im *scene.Image) string {
	if im.Line == "" {
		return ""
	}
	return lnXML(im.Line, im.LineWidthPt)
}
