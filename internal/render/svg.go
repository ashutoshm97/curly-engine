// Package render turns scene slides into SVG previews and editable PPTX files.
package render

import (
	"fmt"
	"html"
	"strings"

	"github.com/ashutoshm97/curly-engine/internal/scene"
)

// AssetURL maps a blob ref to an href used in SVG output.
type AssetURL func(ref string) string

// SVG renders one slide; width/height are the slide size in points.
func SVG(s *scene.Slide, width, height float64, asset AssetURL) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 %s %s" width="%s" height="%s">`,
		f(width), f(height), f(width), f(height))
	b.WriteString(`<defs><filter id="sh" x="-10%" y="-10%" width="130%" height="140%"><feDropShadow dx="0" dy="2" stdDeviation="3" flood-opacity="0.18"/></filter></defs>`)
	fmt.Fprintf(&b, `<rect width="100%%" height="100%%" fill="%s"/>`, attr(s.Background))
	for i, e := range s.Elements {
		switch e.Kind {
		case "shape":
			shape(&b, e)
		case "image":
			imageEl(&b, e, asset, i)
		case "text":
			text(&b, e, e.Text)
		case "table":
			table(&b, e)
		}
	}
	b.WriteString(`</svg>`)
	return b.String()
}

func f(v float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
}

func attr(s string) string { return html.EscapeString(s) }

func shape(b *strings.Builder, e *scene.Element) {
	s := e.Shape
	fill := s.Fill
	if fill == "" {
		fill = "none"
	}
	extra := ""
	if s.Shadow {
		extra = ` filter="url(#sh)"`
	}
	if s.Line != "" {
		extra += fmt.Sprintf(` stroke="%s"`, attr(s.Line))
	}
	if s.Geometry == "ellipse" {
		fmt.Fprintf(b, `<ellipse cx="%s" cy="%s" rx="%s" ry="%s" fill="%s"%s/>`, f(e.X+e.W/2), f(e.Y+e.H/2), f(e.W/2), f(e.H/2), attr(fill), extra)
		return
	}
	r := 0.0
	if s.Geometry == "roundRect" {
		r = s.RadiusPt
		if r == 0 {
			r = min(e.W, e.H) * 0.08
		}
	}
	fmt.Fprintf(b, `<rect x="%s" y="%s" width="%s" height="%s" rx="%s" fill="%s"%s/>`, f(e.X), f(e.Y), f(e.W), f(e.H), f(r), attr(fill), extra)
}

func imageEl(b *strings.Builder, e *scene.Element, asset AssetURL, i int) {
	im := e.Image
	if im.Ref != "" && asset != nil {
		fmt.Fprintf(b, `<image x="%s" y="%s" width="%s" height="%s" href="%s" preserveAspectRatio="xMidYMid meet"/>`, f(e.X), f(e.Y), f(e.W), f(e.H), attr(asset(im.Ref)))
		return
	}
	fmt.Fprintf(b, `<rect x="%s" y="%s" width="%s" height="%s" rx="%s" fill="%s"/>`, f(e.X), f(e.Y), f(e.W), f(e.H), f(im.RadiusPt), attr(im.Fill))
	cx, cy := e.X+e.W/2, e.Y+e.H/2
	fmt.Fprintf(b, `<path d="M%s %s l18 -22 l12 14 l8 -9 l16 17 z" fill="%s" opacity="0.25"/>`, f(cx-27), f(cy+6), attr(im.TextColor))
	label := im.Placeholder
	if len([]rune(label)) > 60 {
		label = string([]rune(label)[:57]) + "…"
	}
	fmt.Fprintf(b, `<text x="%s" y="%s" text-anchor="middle" font-family="sans-serif" font-size="11" fill="%s" opacity="0.6">%s</text>`, f(cx), f(cy+28), attr(im.TextColor), html.EscapeString(label))
}

func family(t *scene.Text) string {
	fam := fmt.Sprintf("'%s'", t.Font)
	if t.Fallback != "" {
		fam += fmt.Sprintf(", '%s'", t.Fallback)
	}
	return fam + ", sans-serif"
}

func text(b *strings.Builder, e *scene.Element, t *scene.Text) {
	anchor, dx := "start", 0.0
	switch t.Align {
	case "ctr":
		anchor, dx = "middle", e.W/2
	case "r":
		anchor, dx = "end", e.W-t.InsetX
	}
	weight := "400"
	if t.Bold {
		weight = "700"
	}
	style := ""
	if t.Italic {
		style = ` font-style="italic"`
	}
	fmt.Fprintf(b, `<g font-family="%s" font-size="%s" font-weight="%s" fill="%s"%s>`, attr(family(t)), f(t.SizePt), weight, attr(t.Color), style)
	for _, l := range t.Lines {
		x := e.X + l.X
		if anchor != "start" {
			x = e.X + dx
		}
		if l.Bullet {
			bc := t.BulletColor
			if bc == "" {
				bc = t.Color
			}
			fmt.Fprintf(b, `<text x="%s" y="%s" fill="%s">%s</text>`, f(e.X+t.InsetX), f(e.Y+l.Baseline), attr(bc), html.EscapeString(t.Bullet))
		}
		fmt.Fprintf(b, `<text x="%s" y="%s" text-anchor="%s">%s</text>`, f(x), f(e.Y+l.Baseline), anchor, html.EscapeString(l.Text))
	}
	b.WriteString(`</g>`)
}

func table(b *strings.Builder, e *scene.Element) {
	t := e.Table
	cols := len(t.Header)
	rows := len(t.Rows) + 1
	rh := min(e.H/float64(rows), t.SizePt*2.2)
	cw := e.W / float64(cols)
	fmt.Fprintf(b, `<g font-family="'%s', sans-serif" font-size="%s">`, attr(t.Font), f(t.SizePt))
	for r := 0; r < rows; r++ {
		y := e.Y + float64(r)*rh
		fill, color, cells, weight := t.BodyFill, t.BodyText, []string(nil), "400"
		if r == 0 {
			fill, color, cells = t.HeaderFill, t.HeaderText, t.Header
			if t.HeaderBold {
				weight = "700"
			}
		} else {
			cells = t.Rows[r-1]
			if t.BandFill != "" && r%2 == 1 {
				fill = t.BandFill
			}
		}
		if fill != "" {
			fmt.Fprintf(b, `<rect x="%s" y="%s" width="%s" height="%s" fill="%s"/>`, f(e.X), f(y), f(e.W), f(rh), attr(fill))
		}
		if t.Border != "" {
			fmt.Fprintf(b, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="0.75"/>`, f(e.X), f(y+rh), f(e.X+e.W), f(y+rh), attr(t.Border))
		}
		for c := 0; c < cols && c < len(cells); c++ {
			fmt.Fprintf(b, `<text x="%s" y="%s" fill="%s" font-weight="%s">%s</text>`, f(e.X+float64(c)*cw+7.2), f(y+rh/2+t.SizePt*0.35), attr(color), weight, html.EscapeString(clipText(cells[c], cw-14.4, t.SizePt)))
		}
	}
	b.WriteString(`</g>`)
}

func clipText(s string, w, size float64) string {
	max := int(w / (size * 0.52))
	r := []rune(s)
	if max > 1 && len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}
