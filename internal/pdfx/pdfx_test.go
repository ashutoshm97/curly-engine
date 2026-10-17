package pdfx

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ashutoshm97/curly-engine/internal/normalize"
	"github.com/ashutoshm97/curly-engine/internal/rawdeck"
)

func TestExtractNorthwind(t *testing.T) {
	data, err := os.ReadFile("../../testdata/northwind.pdf")
	if err != nil {
		t.Fatal(err)
	}
	d, err := Extract(data, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if d.SourceType != "pdf" || len(d.Slides) != 3 || d.WidthPt != 720 || d.HeightPt < 404 || d.HeightPt > 406 {
		t.Fatalf("deck = %s %d slides %vx%v", d.SourceType, len(d.Slides), d.WidthPt, d.HeightPt)
	}
	find := func(s *rawdeck.Slide, text string) *rawdeck.Element {
		for _, el := range s.Elements {
			if el.Kind == rawdeck.KindText && el.Text == text {
				return el
			}
		}
		t.Fatalf("slide %d: no text %q", s.Index, text)
		return nil
	}
	cover := find(d.Slides[0], "Northwind Strategy")
	if !cover.Style.Bold || cover.Style.Color.Hex != "#FFFFFF" || cover.Style.Font != "LiberationSans" || cover.Style.SizePt < 30 {
		t.Errorf("cover title style = %+v", cover.Style)
	}
	if d.Slides[0].Elements[0].Fill.Hex != "#0F1632" {
		t.Errorf("cover background = %q", d.Slides[0].Elements[0].Fill.Hex)
	}
	title := find(d.Slides[1], "Three priorities")
	if title.Role != rawdeck.RoleTitle || title.Box.X > 0.06 || title.Box.Y > 0.08 {
		t.Errorf("title = %s %+v", title.Role, title.Box)
	}
	cards := 0
	for _, el := range d.Slides[1].Elements {
		if el.Kind == rawdeck.KindShape && el.Fill.Hex == "#EDEDEF" {
			cards++
		}
	}
	if cards != 3 {
		t.Errorf("cards = %d, want 3", cards)
	}
	list := find(d.Slides[2], "Revenue up 12% year over year\nMargins expanded 150 bps\nRecord client retention")
	if list.Style.Bullet == "" || list.Style.Paragraphs != 3 {
		t.Errorf("list style = %+v", list.Style)
	}
	if foot := find(d.Slides[2], "Northwind confidential"); !foot.Inherited {
		t.Error("repeated footer not marked inherited")
	}

	p, err := normalize.Build(d, normalize.Options{Name: "northwind"})
	if err != nil {
		t.Fatal(err)
	}
	if p.SourceType != "pdf" || len(p.Layouts) == 0 {
		t.Fatalf("profile: %s, %d layouts", p.SourceType, len(p.Layouts))
	}
	var hasNavy bool
	for _, c := range p.Tokens.Colors {
		hasNavy = hasNavy || strings.EqualFold(c.Value, "#0F1632")
	}
	if !hasNavy {
		t.Errorf("palette missing #0F1632: %+v", p.Tokens.Colors)
	}
}

func TestExtractRejects(t *testing.T) {
	if _, err := Extract([]byte("%PDF-1.4 not really"), DefaultLimits); err == nil {
		t.Error("garbage accepted")
	}
	noText := []byte("%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 960 540]/Contents 4 0 R>>endobj\n4 0 obj<</Length 30>>stream\n0 0 1 rg 0 0 960 540 re f\nendstream endobj\n")
	if _, err := Extract(noText, DefaultLimits); !errors.Is(err, ErrNoText) && err == nil {
		t.Errorf("image-only pdf: err = %v", err)
	}
}

func TestParseFontName(t *testing.T) {
	for in, want := range map[string]string{
		"ABCDEF+CitiSans-DisplayBold": "CitiSans Display|true|false",
		"Arial-BoldItalicMT":          "Arial|true|true",
		"ArialMT":                     "Arial|false|false",
		"TimesNewRomanPS-ItalicMT":    "TimesNewRoman|false|true",
		"Helvetica":                   "Helvetica|false|false",
	} {
		f, b, i := parseFontName(in)
		if got := strings.Join([]string{f, boolStr(b), boolStr(i)}, "|"); got != want {
			t.Errorf("%s = %s, want %s", in, got, want)
		}
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
