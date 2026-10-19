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

// palette maps raw colors to profile color tokens, registering derived tokens
// for tints/shades and one-off colors.
type palette struct {
	tokens  []profile.ColorToken
	byName  map[string]int
	derived int
}

func newPalette() *palette { return &palette{byName: map[string]int{}} }

func (p *palette) add(name, hex string, derived bool) string {
	hex = strings.ToUpper(hex)
	if i, ok := p.byName[name]; ok {
		return p.tokens[i].Name
	}
	p.byName[name] = len(p.tokens)
	p.tokens = append(p.tokens, profile.ColorToken{Name: name, Value: hex, Derived: derived})
	return name
}

// ref returns the token name for a raw color ("" when the color is empty).
func (p *palette) ref(c rawdeck.Color) string {
	if c.IsZero() {
		return ""
	}
	if c.Token != "" {
		name := strings.NewReplacer("@", "-").Replace(c.Token)
		if _, ok := p.byName[name]; ok {
			return name
		}
		if c.Hex != "" {
			return p.add(name, c.Hex, true)
		}
	}
	if c.Hex == "" {
		return ""
	}
	hex := strings.ToUpper(c.Hex)
	// Prefer base (non-derived) tokens on near matches.
	best, bestD := "", math.MaxFloat64
	for _, t := range p.tokens {
		d := deltaE(hex, t.Value)
		if t.Derived {
			d += 0.5
		}
		if d < bestD {
			best, bestD = t.Name, d
		}
	}
	if bestD < 3 {
		return best
	}
	p.derived++
	return p.add(fmt.Sprintf("c%d", p.derived), hex, true)
}

func hexRGB(hex string) (float64, float64, float64) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 0, 0, 0
	}
	v, _ := strconv.ParseUint(hex, 16, 32)
	return float64(v>>16&0xFF) / 255, float64(v>>8&0xFF) / 255, float64(v&0xFF) / 255
}

func toLab(hex string) (l, a, b float64) {
	r, g, bl := hexRGB(hex)
	lin := func(c float64) float64 {
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	r, g, bl = lin(r), lin(g), lin(bl)
	x := (r*0.4124 + g*0.3576 + bl*0.1805) / 0.95047
	y := r*0.2126 + g*0.7152 + bl*0.0722
	z := (r*0.0193 + g*0.1192 + bl*0.9505) / 1.08883
	f := func(t float64) float64 {
		if t > 0.008856 {
			return math.Cbrt(t)
		}
		return 7.787*t + 16.0/116
	}
	fx, fy, fz := f(x), f(y), f(z)
	return 116*fy - 16, 500 * (fx - fy), 200 * (fy - fz)
}

func deltaE(h1, h2 string) float64 {
	l1, a1, b1 := toLab(h1)
	l2, a2, b2 := toLab(h2)
	return math.Sqrt((l1-l2)*(l1-l2) + (a1-a2)*(a1-a2) + (b1-b2)*(b1-b2))
}

func lightness(hex string) float64 { l, _, _ := toLab(hex); return l }

func chroma(hex string) float64 {
	_, a, b := toLab(hex)
	return math.Sqrt(a*a + b*b)
}

// inferTheme builds theme colors for sources without one (HTML), from
// area-weighted fills/backgrounds and char-weighted text colors.
func inferTheme(d *rawdeck.Deck) rawdeck.Theme {
	type acc struct {
		hex    string
		bg, tx float64
	}
	var buckets []*acc
	addW := func(hex string, bg, tx float64) {
		if hex == "" {
			return
		}
		hex = strings.ToUpper(hex)
		for _, b := range buckets {
			if deltaE(b.hex, hex) < 8 {
				b.bg += bg
				b.tx += tx
				return
			}
		}
		buckets = append(buckets, &acc{hex: hex, bg: bg, tx: tx})
	}
	for _, s := range d.Slides {
		addW(s.Background.Hex, 1, 0)
		for _, el := range s.Elements {
			if !el.Fill.IsZero() {
				addW(el.Fill.Hex, el.Box.Area(), 0)
			}
			if el.Kind == rawdeck.KindText {
				addW(el.Style.Color.Hex, 0, float64(len(el.Text))/400)
			}
			if el.Table != nil {
				addW(el.Table.HeaderFill.Hex, el.Box.Area()*0.2, 0)
			}
		}
	}
	t := rawdeck.Theme{}
	pick := func(score func(*acc) float64, filter func(*acc) bool) string {
		var best *acc
		bs := -1.0
		for _, b := range buckets {
			if filter != nil && !filter(b) {
				continue
			}
			if s := score(b); s > bs {
				best, bs = b, s
			}
		}
		if best == nil {
			return ""
		}
		return best.hex
	}
	used := map[string]bool{}
	lt1 := pick(func(a *acc) float64 { return a.bg }, func(a *acc) bool { return lightness(a.hex) > 85 })
	if lt1 == "" {
		lt1 = "#FFFFFF"
	}
	dk1 := pick(func(a *acc) float64 { return a.tx }, func(a *acc) bool { return lightness(a.hex) < 40 })
	if dk1 == "" {
		dk1 = "#000000"
	}
	used[lt1], used[dk1] = true, true
	t.Colors = append(t.Colors, rawdeck.NamedColor{Name: "dk1", Hex: dk1}, rawdeck.NamedColor{Name: "lt1", Hex: lt1})
	dk2 := pick(func(a *acc) float64 { return a.bg + a.tx }, func(a *acc) bool { return !used[a.hex] && lightness(a.hex) < 35 })
	if dk2 != "" {
		used[dk2] = true
		t.Colors = append(t.Colors, rawdeck.NamedColor{Name: "dk2", Hex: dk2})
	}
	sort.SliceStable(buckets, func(i, j int) bool { return buckets[i].bg+buckets[i].tx > buckets[j].bg+buckets[j].tx })
	n := 0
	for _, b := range buckets {
		if used[b.hex] || chroma(b.hex) < 20 {
			continue
		}
		n++
		used[b.hex] = true
		t.Colors = append(t.Colors, rawdeck.NamedColor{Name: fmt.Sprintf("accent%d", n), Hex: b.hex})
		if n == 6 {
			break
		}
	}
	return t
}
