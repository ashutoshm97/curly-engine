package ooxml

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/ashutoshm97/curly-engine/internal/rawdeck"
	"github.com/ashutoshm97/curly-engine/internal/xmldom"
)

var defaultClrMap = map[string]string{
	"bg1": "lt1", "tx1": "dk1", "bg2": "lt2", "tx2": "dk2",
	"accent1": "accent1", "accent2": "accent2", "accent3": "accent3",
	"accent4": "accent4", "accent5": "accent5", "accent6": "accent6",
	"hlink": "hlink", "folHlink": "folHlink",
}

var presetColors = map[string]string{
	"black": "000000", "white": "FFFFFF", "red": "FF0000", "green": "008000",
	"blue": "0000FF", "yellow": "FFFF00", "gray": "808080", "grey": "808080",
	"orange": "FFA500", "purple": "800080", "navy": "000080", "teal": "008080",
}

type colorCtx struct {
	theme  rawdeck.Theme
	clrMap map[string]string
}

// resolve reads the color child of n (solidFill, fgClr, a color choice parent…).
func (c *colorCtx) resolve(n *xmldom.Node) rawdeck.Color {
	if n == nil {
		return rawdeck.Color{}
	}
	for _, ch := range n.Children {
		var hex, token string
		switch ch.Name {
		case "srgbClr":
			hex = ch.Attr("val")
		case "sysClr":
			hex = ch.Attr("lastClr")
			if hex == "" {
				if ch.Attr("val") == "window" {
					hex = "FFFFFF"
				} else {
					hex = "000000"
				}
			}
		case "prstClr":
			hex = presetColors[ch.Attr("val")]
		case "scrgbClr":
			r, _ := strconv.ParseFloat(ch.Attr("r"), 64)
			g, _ := strconv.ParseFloat(ch.Attr("g"), 64)
			b, _ := strconv.ParseFloat(ch.Attr("b"), 64)
			hex = fmt.Sprintf("%02X%02X%02X", int(r/100000*255), int(g/100000*255), int(b/100000*255))
		case "schemeClr":
			val := ch.Attr("val")
			if val == "phClr" {
				continue
			}
			mapped := val
			if m, ok := c.clrMap[val]; ok {
				mapped = m
			} else if m, ok := defaultClrMap[val]; ok {
				mapped = m
			}
			token = mapped
			hex = c.theme.Lookup(mapped)
		default:
			continue
		}
		if hex == "" {
			return rawdeck.Color{Token: token}
		}
		mod, changed := applyMods(hex, ch)
		if changed {
			if token != "" {
				token = token + modSuffix(ch)
			}
		}
		return rawdeck.Color{Hex: "#" + strings.ToUpper(mod), Token: token}
	}
	return rawdeck.Color{}
}

func modSuffix(n *xmldom.Node) string {
	for _, m := range n.Children {
		v, _ := strconv.ParseFloat(m.Attr("val"), 64)
		switch m.Name {
		case "tint":
			return fmt.Sprintf("@tint%d", int(math.Round(v/1000)))
		case "lumMod":
			return fmt.Sprintf("@lum%d", int(math.Round(v/1000)))
		case "shade":
			return fmt.Sprintf("@shade%d", int(math.Round(v/1000)))
		}
	}
	return ""
}

func applyMods(hex string, n *xmldom.Node) (string, bool) {
	r, g, b, ok := parseHex(hex)
	if !ok {
		return hex, false
	}
	changed := false
	h, s, l := rgbToHSL(r, g, b)
	for _, m := range n.Children {
		v, err := strconv.ParseFloat(m.Attr("val"), 64)
		if err != nil {
			continue
		}
		v /= 100000
		switch m.Name {
		case "lumMod":
			l *= v
			changed = true
		case "lumOff":
			l += v
			changed = true
		case "tint":
			r, g, b = hslToRGB(h, s, l)
			r, g, b = r+(1-r)*(1-v), g+(1-g)*(1-v), b+(1-b)*(1-v)
			h, s, l = rgbToHSL(r, g, b)
			changed = true
		case "shade":
			r, g, b = hslToRGB(h, s, l)
			r, g, b = r*v, g*v, b*v
			h, s, l = rgbToHSL(r, g, b)
			changed = true
		}
	}
	if !changed {
		return hex, false
	}
	l = math.Max(0, math.Min(1, l))
	r, g, b = hslToRGB(h, s, l)
	return fmt.Sprintf("%02X%02X%02X", to255(r), to255(g), to255(b)), true
}

func to255(v float64) int { return int(math.Round(math.Max(0, math.Min(1, v)) * 255)) }

func parseHex(hex string) (r, g, b float64, ok bool) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return float64(v>>16&0xFF) / 255, float64(v>>8&0xFF) / 255, float64(v&0xFF) / 255, true
}

func rgbToHSL(r, g, b float64) (h, s, l float64) {
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	l = (mx + mn) / 2
	if mx == mn {
		return 0, 0, l
	}
	d := mx - mn
	if l > 0.5 {
		s = d / (2 - mx - mn)
	} else {
		s = d / (mx + mn)
	}
	switch mx {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	return h / 6, s, l
}

func hslToRGB(h, s, l float64) (r, g, b float64) {
	if s == 0 {
		return l, l, l
	}
	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q
	hue := func(t float64) float64 {
		if t < 0 {
			t++
		}
		if t > 1 {
			t--
		}
		switch {
		case t < 1.0/6:
			return p + (q-p)*6*t
		case t < 0.5:
			return q
		case t < 2.0/3:
			return p + (q-p)*(2.0/3-t)*6
		}
		return p
	}
	return hue(h + 1.0/3), hue(h), hue(h - 1.0/3)
}
