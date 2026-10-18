package render

import (
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"

	"github.com/ashutoshm97/curly-engine/internal/scene"
)

func isRound(t string) bool { return t == "pie" || t == "doughnut" }

func chartColor(c *scene.Chart, i int) string {
	if len(c.Colors) == 0 {
		return "#4472C4"
	}
	return c.Colors[i%len(c.Colors)]
}

// fmtNum formats v using the decimals, currency and percent of an Excel number format.
func fmtNum(v float64, format string) string {
	dec := 0
	if i := strings.Index(format, "."); i >= 0 {
		for _, r := range format[i+1:] {
			if r != '0' {
				break
			}
			dec++
		}
	} else if (format == "" || format == "General") && v != math.Trunc(v) {
		dec = 1
	}
	s := strconv.FormatFloat(math.Abs(v), 'f', dec, 64)
	if strings.Contains(format, "$") {
		s = "$" + s
	}
	if strings.Contains(format, "%") {
		s += "%"
	}
	if v < 0 {
		s = "-" + s
	}
	return s
}

func niceStep(span float64) float64 {
	if span <= 0 {
		return 1
	}
	raw := span / 4
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if raw <= m*mag {
			return m * mag
		}
	}
	return 10 * mag
}

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func chartSVG(b *strings.Builder, e *scene.Element) {
	c := e.Chart
	if c == nil || len(c.Series) == 0 || len(c.Categories) == 0 {
		return
	}
	fs := c.FontSizePt
	if fs <= 0 {
		fs = 10
	}
	fmt.Fprintf(b, `<g font-family="%s" font-size="%s" fill="%s">`, attr(c.Font), f(fs), attr(c.TextColor))
	x, y, w, h := e.X, e.Y, e.W, e.H
	if c.Legend != "" {
		h -= fs * 2
		chartLegend(b, c, x, y+h+fs*1.3, fs)
	}
	if isRound(c.Type) {
		pieSVG(b, c, x, y, w, h, fs)
	} else {
		axesSVG(b, c, x, y, w, h, fs)
	}
	b.WriteString(`</g>`)
}

func chartLegend(b *strings.Builder, c *scene.Chart, x, y, fs float64) {
	var names []string
	if isRound(c.Type) {
		names = c.Categories
	} else {
		for _, s := range c.Series {
			names = append(names, s.Name)
		}
	}
	for i, n := range names {
		fmt.Fprintf(b, `<rect x="%s" y="%s" width="%s" height="%s" fill="%s"/>`, f(x), f(y-fs*0.75), f(fs*0.8), f(fs*0.8), attr(chartColor(c, i)))
		fmt.Fprintf(b, `<text x="%s" y="%s">%s</text>`, f(x+fs*1.1), f(y), html.EscapeString(n))
		x += fs*2 + float64(len([]rune(n)))*fs*0.55
	}
}

func pieSVG(b *strings.Builder, c *scene.Chart, x, y, w, h, fs float64) {
	vals := c.Series[0].Values
	total := 0.0
	for _, v := range vals {
		total += math.Max(v, 0)
	}
	if total <= 0 {
		return
	}
	cx, cy := x+w/2, y+h/2
	r := math.Min(w, h) / 2 * 0.92
	inner := 0.0
	if c.Type == "doughnut" {
		hole := c.HolePct
		if hole <= 0 {
			hole = 50
		}
		inner = r * float64(hole) / 100
	}
	pt := func(rad, a float64) (float64, float64) { return cx + rad*math.Cos(a), cy + rad*math.Sin(a) }
	a := -math.Pi / 2
	for i, v := range vals {
		if v <= 0 {
			continue
		}
		da := v / total * 2 * math.Pi
		col := chartColor(c, i)
		if da >= 2*math.Pi-1e-6 {
			fmt.Fprintf(b, `<circle cx="%s" cy="%s" r="%s" fill="%s"/>`, f(cx), f(cy), f(r), attr(col))
		} else {
			large := 0
			if da > math.Pi {
				large = 1
			}
			x1, y1 := pt(r, a)
			x2, y2 := pt(r, a+da)
			d := fmt.Sprintf("M%s %s A%s %s 0 %d 1 %s %s", f(x1), f(y1), f(r), f(r), large, f(x2), f(y2))
			if inner > 0 {
				x3, y3 := pt(inner, a+da)
				x4, y4 := pt(inner, a)
				d += fmt.Sprintf(" L%s %s A%s %s 0 %d 0 %s %s Z", f(x3), f(y3), f(inner), f(inner), large, f(x4), f(y4))
			} else {
				d += fmt.Sprintf(" L%s %s Z", f(cx), f(cy))
			}
			fmt.Fprintf(b, `<path d="%s" fill="%s" stroke="#FFFFFF" stroke-width="1"/>`, d, attr(col))
		}
		if c.DataLabels {
			lx, ly := pt((r+inner)/2+r*0.08*boolF(inner == 0), a+da/2)
			fmt.Fprintf(b, `<text x="%s" y="%s" text-anchor="middle" fill="%s" font-size="%s">%s</text>`, f(lx), f(ly+fs*0.35), attr(orStr(c.LabelColor, "#FFFFFF")), f(labelSize(c, fs)), html.EscapeString(fmtNum(v, c.NumFmt)))
		}
		a += da
	}
}

func boolF(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func labelSize(c *scene.Chart, fs float64) float64 {
	if c.LabelSizePt > 0 {
		return c.LabelSizePt
	}
	return fs
}

func axesSVG(b *strings.Builder, c *scene.Chart, x, y, w, h, fs float64) {
	horiz := c.Type == "bar"
	stacked := c.Grouping == "stacked" || c.Grouping == "percentStacked"
	pct := c.Grouping == "percentStacked"
	nc, ns := len(c.Categories), len(c.Series)
	val := func(si, ci int) float64 {
		vs := c.Series[si].Values
		if ci >= len(vs) {
			return 0
		}
		v := vs[ci]
		if pct {
			tot := 0.0
			for _, s := range c.Series {
				if ci < len(s.Values) {
					tot += math.Abs(s.Values[ci])
				}
			}
			if tot > 0 {
				v = v / tot * 100
			}
		}
		return v
	}
	lo, hi := 0.0, 0.0
	for ci := 0; ci < nc; ci++ {
		pos, neg := 0.0, 0.0
		for si := 0; si < ns; si++ {
			v := val(si, ci)
			if stacked {
				if v >= 0 {
					pos += v
				} else {
					neg += v
				}
			} else {
				pos, neg = math.Max(pos, v), math.Min(neg, v)
			}
		}
		hi, lo = math.Max(hi, pos), math.Min(lo, neg)
	}
	step := niceStep(hi - lo)
	lo, hi = math.Floor(lo/step)*step, math.Ceil(hi/step)*step
	if hi <= lo {
		hi = lo + step
	}
	numFmt := c.NumFmt
	if pct {
		numFmt = "0%"
	}
	left, bottom := fs*3.6, fs*1.8
	if horiz {
		left = fs * 0.6
		for _, cat := range c.Categories {
			left = math.Max(left, float64(len([]rune(cat)))*fs*0.55+fs*0.6)
		}
	}
	px, py, pw, ph := x+left, y+fs*0.6, w-left-fs*0.6, h-bottom-fs*0.6
	if pw <= 0 || ph <= 0 {
		return
	}
	// pos maps a value onto the value axis (y for columns, x for bars).
	pos := func(v float64) float64 {
		t := (v - lo) / (hi - lo)
		if horiz {
			return px + t*pw
		}
		return py + ph - t*ph
	}
	grid := orStr(c.GridColor, "#D9D9D9")
	for v := lo; v <= hi+step/1000; v += step {
		p := pos(v)
		if horiz {
			if c.Gridlines {
				fmt.Fprintf(b, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="0.5"/>`, f(p), f(py), f(p), f(py+ph), attr(grid))
			}
			fmt.Fprintf(b, `<text x="%s" y="%s" text-anchor="middle">%s</text>`, f(p), f(py+ph+fs*1.3), html.EscapeString(fmtNum(v, numFmt)))
		} else {
			if c.Gridlines {
				fmt.Fprintf(b, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="0.5"/>`, f(px), f(p), f(px+pw), f(p), attr(grid))
			}
			fmt.Fprintf(b, `<text x="%s" y="%s" text-anchor="end">%s</text>`, f(px-fs*0.4), f(p+fs*0.35), html.EscapeString(fmtNum(v, numFmt)))
		}
	}
	zero := pos(math.Max(lo, 0))
	axis := orStr(c.AxisColor, "#808080")
	band := ph
	if !horiz {
		band = pw
		fmt.Fprintf(b, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="1"/>`, f(px), f(zero), f(px+pw), f(zero), attr(axis))
	} else {
		fmt.Fprintf(b, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="1"/>`, f(zero), f(py), f(zero), f(py+ph), attr(axis))
	}
	gw := band / float64(nc)
	for ci, cat := range c.Categories {
		mid := gw * (float64(ci) + 0.5)
		if horiz {
			fmt.Fprintf(b, `<text x="%s" y="%s" text-anchor="end">%s</text>`, f(px-fs*0.4), f(py+mid+fs*0.35), html.EscapeString(cat))
		} else {
			fmt.Fprintf(b, `<text x="%s" y="%s" text-anchor="middle">%s</text>`, f(px+mid), f(py+ph+fs*1.3), html.EscapeString(cat))
		}
	}
	label := func(cx, cy, v float64) {
		if c.DataLabels {
			fmt.Fprintf(b, `<text x="%s" y="%s" text-anchor="middle" font-size="%s" fill="%s">%s</text>`, f(cx), f(cy+labelSize(c, fs)*0.35), f(labelSize(c, fs)), attr(orStr(c.LabelColor, c.TextColor)), html.EscapeString(fmtNum(v, c.NumFmt)))
		}
	}
	if c.Type == "line" || c.Type == "area" {
		for si := 0; si < ns; si++ {
			var pts []string
			for ci := 0; ci < nc; ci++ {
				v := val(si, ci)
				if stacked {
					for sj := 0; sj < si; sj++ {
						v += val(sj, ci)
					}
				}
				pts = append(pts, f(px+gw*(float64(ci)+0.5))+","+f(pos(v)))
			}
			col := chartColor(c, si)
			if c.Type == "area" {
				fmt.Fprintf(b, `<polygon points="%s %s,%s %s,%s" fill="%s" opacity="0.85"/>`, strings.Join(pts, " "), f(px+gw*(float64(nc)-0.5)), f(zero), f(px+gw*0.5), f(zero), attr(col))
			} else {
				fmt.Fprintf(b, `<polyline points="%s" fill="none" stroke="%s" stroke-width="2.25"/>`, strings.Join(pts, " "), attr(col))
			}
		}
		return
	}
	gap := float64(c.GapWidth) / 100
	if c.GapWidth == 0 {
		gap = 1.5
	}
	rect := func(at, thick, from, to float64, col string) {
		a, z := math.Min(pos(from), pos(to)), math.Max(pos(from), pos(to))
		if horiz {
			fmt.Fprintf(b, `<rect x="%s" y="%s" width="%s" height="%s" fill="%s"/>`, f(a), f(py+at), f(z-a), f(thick), attr(col))
		} else {
			fmt.Fprintf(b, `<rect x="%s" y="%s" width="%s" height="%s" fill="%s"/>`, f(px+at), f(a), f(thick), f(z-a), attr(col))
		}
	}
	center := func(at, thick, from, to float64) (float64, float64) {
		m := (pos(from) + pos(to)) / 2
		if horiz {
			return m, py + at + thick/2
		}
		return px + at + thick/2, m
	}
	for ci := 0; ci < nc; ci++ {
		if stacked {
			thick := gw / (1 + gap)
			at := gw*float64(ci) + (gw-thick)/2
			up, down := 0.0, 0.0
			for si := 0; si < ns; si++ {
				v := val(si, ci)
				from := up
				if v < 0 {
					from = down
				}
				rect(at, thick, from, from+v, chartColor(c, si))
				lx, ly := center(at, thick, from, from+v)
				label(lx, ly, c.Series[si].Values[min(ci, len(c.Series[si].Values)-1)])
				if v < 0 {
					down += v
				} else {
					up += v
				}
			}
			continue
		}
		thick := gw / (float64(ns) + gap)
		for si := 0; si < ns; si++ {
			at := gw*float64(ci) + thick*gap/2 + thick*float64(si)
			v := val(si, ci)
			rect(at, thick, 0, v, chartColor(c, si))
			if c.DataLabels {
				lx, ly := center(at, thick, v, v)
				if horiz {
					lx += fs
				} else {
					ly -= fs * 0.7
				}
				label(lx, ly, v)
			}
		}
	}
}

// chartXML writes a native DrawingML chart part with literal (embedded) data.
func chartXML(c *scene.Chart) string {
	var b strings.Builder
	b.WriteString(hdr + `<c:chartSpace xmlns:c="http://schemas.openxmlformats.org/drawingml/2006/chart" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><c:roundedCorners val="0"/><c:chart><c:autoTitleDeleted val="1"/><c:plotArea><c:layout/>`)
	grouping := c.Grouping
	fill := func(col string) string {
		return `<a:solidFill><a:srgbClr val="` + hex6(col) + `"/></a:solidFill>`
	}
	txPr := func(size float64, col string) string {
		s := fmt.Sprintf(`<c:txPr><a:bodyPr/><a:lstStyle/><a:p><a:pPr><a:defRPr sz="%d">`, int(size*100))
		if col != "" {
			s += fill(col)
		}
		return s + `</a:defRPr></a:pPr><a:endParaRPr lang="en-US"/></a:p></c:txPr>`
	}
	fs := c.FontSizePt
	if fs <= 0 {
		fs = 10
	}
	dLbls := func() string {
		if !c.DataLabels {
			return ""
		}
		s := `<c:dLbls>`
		if c.NumFmt != "" {
			s += `<c:numFmt formatCode="` + esc(c.NumFmt) + `" sourceLinked="0"/>`
		}
		return s + `<c:spPr><a:noFill/><a:ln><a:noFill/></a:ln></c:spPr>` + txPr(labelSize(c, fs), c.LabelColor) +
			`<c:showLegendKey val="0"/><c:showVal val="1"/><c:showCatName val="0"/><c:showSerName val="0"/><c:showPercent val="0"/><c:showBubbleSize val="0"/></c:dLbls>`
	}
	cats := func() string {
		s := fmt.Sprintf(`<c:cat><c:strLit><c:ptCount val="%d"/>`, len(c.Categories))
		for i, cat := range c.Categories {
			s += fmt.Sprintf(`<c:pt idx="%d"><c:v>%s</c:v></c:pt>`, i, esc(cat))
		}
		return s + `</c:strLit></c:cat>`
	}
	vals := func(vs []float64) string {
		s := fmt.Sprintf(`<c:val><c:numLit><c:formatCode>General</c:formatCode><c:ptCount val="%d"/>`, len(c.Categories))
		for i, v := range vs {
			s += fmt.Sprintf(`<c:pt idx="%d"><c:v>%s</c:v></c:pt>`, i, strconv.FormatFloat(v, 'f', -1, 64))
		}
		return s + `</c:numLit></c:val>`
	}
	ser := func(i int, s scene.ChartSeries) string {
		out := fmt.Sprintf(`<c:ser><c:idx val="%d"/><c:order val="%d"/><c:tx><c:strRef><c:f>Sheet1!$%c$1</c:f><c:strCache><c:ptCount val="1"/><c:pt idx="0"><c:v>%s</c:v></c:pt></c:strCache></c:strRef></c:tx>`, i, i, 'B'+i, esc(s.Name))
		switch {
		case c.Type == "line":
			out += `<c:spPr><a:ln w="28575" cap="rnd">` + fill(chartColor(c, i)) + `</a:ln></c:spPr><c:marker><c:symbol val="none"/></c:marker>`
		case isRound(c.Type):
			for j := range c.Categories {
				out += fmt.Sprintf(`<c:dPt><c:idx val="%d"/><c:bubble3D val="0"/><c:spPr>%s<a:ln><a:solidFill><a:srgbClr val="FFFFFF"/></a:solidFill></a:ln></c:spPr></c:dPt>`, j, fill(chartColor(c, j)))
			}
		default:
			out += `<c:spPr>` + fill(chartColor(c, i)) + `</c:spPr>`
			if c.Type != "area" {
				out += `<c:invertIfNegative val="0"/>`
			}
		}
		out += dLbls() + cats() + vals(s.Values)
		if c.Type == "line" {
			out += `<c:smooth val="0"/>`
		}
		return out + `</c:ser>`
	}
	var series strings.Builder
	for i, s := range c.Series {
		series.WriteString(ser(i, s))
	}
	axIDs := `<c:axId val="111"/><c:axId val="222"/>`
	switch c.Type {
	case "pie":
		b.WriteString(`<c:pieChart><c:varyColors val="1"/>` + series.String() + `<c:firstSliceAng val="0"/></c:pieChart>`)
	case "doughnut":
		hole := c.HolePct
		if hole <= 0 {
			hole = 50
		}
		fmt.Fprintf(&b, `<c:doughnutChart><c:varyColors val="1"/>%s<c:firstSliceAng val="0"/><c:holeSize val="%d"/></c:doughnutChart>`, series.String(), hole)
	case "line", "area":
		if grouping != "stacked" && grouping != "percentStacked" {
			grouping = "standard"
		}
		tag := "lineChart"
		if c.Type == "area" {
			tag = "areaChart"
		}
		fmt.Fprintf(&b, `<c:%s><c:grouping val="%s"/><c:varyColors val="0"/>%s`, tag, grouping, series.String())
		if c.Type == "line" {
			b.WriteString(`<c:marker val="1"/>`)
		}
		fmt.Fprintf(&b, `%s</c:%s>`, axIDs, tag)
	default:
		dir := "col"
		if c.Type == "bar" {
			dir = "bar"
		}
		if grouping != "stacked" && grouping != "percentStacked" {
			grouping = "clustered"
		}
		gap := c.GapWidth
		if gap == 0 {
			gap = 150
		}
		fmt.Fprintf(&b, `<c:barChart><c:barDir val="%s"/><c:grouping val="%s"/><c:varyColors val="0"/>%s<c:gapWidth val="%d"/>`, dir, grouping, series.String(), gap)
		if grouping != "clustered" {
			b.WriteString(`<c:overlap val="100"/>`)
		}
		b.WriteString(axIDs + `</c:barChart>`)
	}
	if !isRound(c.Type) {
		catPos, valPos := "b", "l"
		if c.Type == "bar" {
			catPos, valPos = "l", "b"
		}
		axisLn := `<a:ln w="9525">` + fill(orStr(c.AxisColor, "#808080")) + `</a:ln>`
		fmt.Fprintf(&b, `<c:catAx><c:axId val="111"/><c:scaling><c:orientation val="minMax"/></c:scaling><c:delete val="0"/><c:axPos val="%s"/><c:numFmt formatCode="General" sourceLinked="0"/><c:majorTickMark val="none"/><c:minorTickMark val="none"/><c:tickLblPos val="low"/><c:spPr>%s</c:spPr>%s<c:crossAx val="222"/><c:crosses val="autoZero"/><c:auto val="1"/><c:lblAlgn val="ctr"/><c:lblOffset val="100"/><c:noMultiLvlLbl val="0"/></c:catAx>`,
			catPos, axisLn, txPr(fs, c.TextColor))
		fmt.Fprintf(&b, `<c:valAx><c:axId val="222"/><c:scaling><c:orientation val="minMax"/></c:scaling><c:delete val="0"/><c:axPos val="%s"/>`, valPos)
		if c.Gridlines {
			b.WriteString(`<c:majorGridlines><c:spPr><a:ln w="6350">` + fill(orStr(c.GridColor, "#D9D9D9")) + `</a:ln></c:spPr></c:majorGridlines>`)
		}
		valFmt := orStr(c.NumFmt, "General")
		if grouping == "percentStacked" {
			valFmt = "0%"
		}
		fmt.Fprintf(&b, `<c:numFmt formatCode="%s" sourceLinked="0"/><c:majorTickMark val="none"/><c:minorTickMark val="none"/><c:tickLblPos val="nextTo"/><c:spPr><a:ln><a:noFill/></a:ln></c:spPr>%s<c:crossAx val="111"/><c:crosses val="autoZero"/><c:crossBetween val="between"/></c:valAx>`,
			esc(valFmt), txPr(fs, c.TextColor))
	}
	b.WriteString(`</c:plotArea>`)
	if c.Legend != "" {
		fmt.Fprintf(&b, `<c:legend><c:legendPos val="%s"/><c:overlay val="0"/>%s</c:legend>`, esc(c.Legend), txPr(fs, c.TextColor))
	}
	fmt.Fprintf(&b, `<c:plotVisOnly val="1"/><c:dispBlanksAs val="gap"/></c:chart><c:txPr><a:bodyPr/><a:lstStyle/><a:p><a:pPr><a:defRPr sz="%d"><a:latin typeface="%s"/></a:defRPr></a:pPr><a:endParaRPr lang="en-US"/></a:p></c:txPr></c:chartSpace>`,
		int(fs*100), esc(c.Font))
	return b.String()
}
