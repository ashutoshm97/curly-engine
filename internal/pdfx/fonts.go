package pdfx

import (
	"fmt"
	"math"
	"strings"

	"github.com/ledongthuc/pdf"
)

type fontInfo struct {
	family       string
	bold, italic bool
	symbol       bool
	twoByte      bool
	first        int
	widths       []float64
	cid          map[int]float64
	dw, missing  float64
	enc          pdf.TextEncoding
}

func (f *fontInfo) width(code int) float64 {
	if f.twoByte {
		if w, ok := f.cid[code]; ok {
			return w
		}
		return f.dw
	}
	if i := code - f.first; i >= 0 && i < len(f.widths) && f.widths[i] > 0 {
		return f.widths[i]
	}
	if f.missing > 0 {
		return f.missing
	}
	return 500
}

func (x *interp) font(res pdf.Value, name string) *fontInfo {
	v := res.Key("Font").Key(name)
	if v.IsNull() {
		return nil
	}
	// Subset prefixes (e.g. "AAAAAA+") are reused across pages by some
	// producers, so the cache is per page and keyed on the encoding too.
	key := fmt.Sprint(name, "|", v.Key("BaseFont").Name(), "|", v.Key("FirstChar").Int64(), "|", v.Key("Widths").Len(), "|", v.Key("ToUnicode").Key("Length").Int64())
	if f, ok := x.fonts[key]; ok {
		return f
	}
	f := loadFont(v)
	x.fonts[key] = f
	return f
}

func loadFont(v pdf.Value) *fontInfo {
	f := &fontInfo{dw: 1000}
	f.family, f.bold, f.italic = parseFontName(v.Key("BaseFont").Name())
	var fd pdf.Value
	if v.Key("Subtype").Name() == "Type0" {
		f.twoByte = true
		df := v.Key("DescendantFonts").Index(0)
		if dw := df.Key("DW"); dw.Kind() == pdf.Integer || dw.Kind() == pdf.Real {
			f.dw = dw.Float64()
		}
		f.cid = map[int]float64{}
		w := df.Key("W")
		for i := 0; i+1 < w.Len(); {
			c := int(w.Index(i).Int64())
			if nx := w.Index(i + 1); nx.Kind() == pdf.Array {
				for j := 0; j < nx.Len(); j++ {
					f.cid[c+j] = nx.Index(j).Float64()
				}
				i += 2
				continue
			}
			if i+2 >= w.Len() {
				break
			}
			c2, wd := int(w.Index(i+1).Int64()), w.Index(i+2).Float64()
			for k := c; k <= c2 && k-c < 65536; k++ {
				f.cid[k] = wd
			}
			i += 3
		}
		fd = df.Key("FontDescriptor")
	} else {
		f.first = int(v.Key("FirstChar").Int64())
		ws := v.Key("Widths")
		for i := 0; i < ws.Len(); i++ {
			f.widths = append(f.widths, ws.Index(i).Float64())
		}
		fd = v.Key("FontDescriptor")
		f.missing = fd.Key("MissingWidth").Float64()
	}
	if fw := fd.Key("FontWeight").Float64(); fw >= 600 {
		f.bold = true
	}
	if int(fd.Key("Flags").Int64())&64 != 0 {
		f.italic = true
	}
	lf := strings.ToLower(f.family)
	f.symbol = strings.Contains(lf, "wingding") || strings.Contains(lf, "symbol") || strings.Contains(lf, "dingbat")
	func() {
		defer func() { _ = recover() }()
		f.enc = pdf.Font{V: v}.Encoder()
	}()
	return f
}

var weightWords = []string{"ExtraBold", "SemiBold", "Semibold", "DemiBold", "UltraBold", "ExtraLight", "Bold", "Black", "Heavy", "Regular", "Italic", "Oblique", "Light", "Medium", "Thin", "Book", "Roman", "Normal", "Demi"}

// parseFontName turns a PostScript name ("ABCDEF+CitiSans-DisplayBold",
// "Arial-BoldItalicMT") into a family ("CitiSans Display", "Arial") plus flags.
func parseFontName(base string) (family string, bold, italic bool) {
	if i := strings.Index(base, "+"); i == 6 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(base, "-Identity-H")
	fam, style := base, ""
	if i := strings.IndexAny(base, "-,"); i > 0 {
		fam, style = base[:i], base[i+1:]
	}
	for _, suf := range []string{"PSMT", "MT", "PS"} {
		if len(fam) > len(suf)+2 {
			fam = strings.TrimSuffix(fam, suf)
		}
		style = strings.TrimSuffix(style, suf)
	}
	l := strings.ToLower(base)
	bold = strings.Contains(l, "bold") || strings.Contains(l, "black") || strings.Contains(l, "heavy") || strings.Contains(l, "demi")
	italic = strings.Contains(l, "italic") || strings.Contains(l, "oblique") || strings.HasSuffix(style, "It")
	rest := style
	for _, w := range weightWords {
		rest = strings.ReplaceAll(rest, w, "")
	}
	rest = strings.TrimSuffix(rest, "It")
	if rest = strings.TrimSpace(rest); rest != "" {
		fam += " " + rest
	}
	return fam, bold, italic
}

func (x *interp) show(raw string) {
	g := &x.g
	f := g.font
	if f == nil || raw == "" {
		return
	}
	trm := func() mat { return mat{g.tfs * g.th, 0, 0, g.tfs, 0, g.rise}.mul(g.tm).mul(g.ctm) }
	t0 := trm()
	step := 1
	if f.twoByte {
		step = 2
	}
	for i := 0; i+step <= len(raw); i += step {
		code := int(raw[i])
		if step == 2 {
			code = code<<8 | int(raw[i+1])
		}
		adv := f.width(code)/1000*g.tfs + g.tc
		if step == 1 && code == 32 {
			adv += g.tw
		}
		g.tm = mat{1, 0, 0, 1, adv * g.th, 0}.mul(g.tm)
	}
	if g.mode == 3 || g.mode == 7 {
		return
	}
	size := math.Hypot(t0[2], t0[3])
	if size < 2 || t0[0] <= 0 || math.Abs(t0[1]) > 0.05*t0[0] {
		return // invisible, tiny or rotated text
	}
	text := raw
	if f.enc != nil {
		text = f.enc.Decode(raw)
	}
	text = strings.Map(func(r rune) rune {
		if r < 32 || r == 0xFFFD {
			return -1
		}
		return r
	}, text)
	if text == "" {
		return
	}
	sx, sy := t0.apply(0, 0)
	ex, _ := trm().apply(0, 0)
	x.runs = append(x.runs, run{x0: sx, x1: math.Max(ex, sx), y: sy, size: size, font: f, color: g.fill, text: text})
}
