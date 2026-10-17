package layout

import (
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
)

// Text is measured with the Go fonts as a metric proxy for the profile fonts;
// widthFactor leaves headroom for wider display faces.
const widthFactor = 1.04

var (
	regular, bold *opentype.Font
	faces         sync.Map // faceKey -> font.Face
	parseOnce     sync.Once
)

type faceKey struct {
	bold bool
	size float64
}

func face(b bool, size float64) font.Face {
	parseOnce.Do(func() {
		regular, _ = opentype.Parse(goregular.TTF)
		bold, _ = opentype.Parse(gobold.TTF)
	})
	k := faceKey{b, size}
	if f, ok := faces.Load(k); ok {
		return f.(font.Face)
	}
	src := regular
	if b {
		src = bold
	}
	f, err := opentype.NewFace(src, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return nil
	}
	actual, _ := faces.LoadOrStore(k, f)
	return actual.(font.Face)
}

// Width returns the rendered width of s in points.
func Width(s string, size float64, b bool) float64 {
	f := face(b, size)
	if f == nil {
		return float64(utf8.RuneCountInString(s)) * size * 0.55
	}
	return float64(font.MeasureString(f, s)) / 64 * widthFactor
}

// Wrap breaks text into lines no wider than w.
func Wrap(text string, size, w float64, b bool) []string {
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		cur := ""
		for _, word := range words {
			try := word
			if cur != "" {
				try = cur + " " + word
			}
			if cur != "" && Width(try, size, b) > w {
				lines = append(lines, cur)
				cur = word
			} else {
				cur = try
			}
			for Width(cur, size, b) > w && utf8.RuneCountInString(cur) > 1 {
				// Hard-break words longer than the line.
				r := []rune(cur)
				n := len(r) - 1
				for n > 1 && Width(string(r[:n]), size, b) > w {
					n--
				}
				lines = append(lines, string(r[:n]))
				cur = string(r[n:])
			}
		}
		lines = append(lines, cur)
	}
	return lines
}
