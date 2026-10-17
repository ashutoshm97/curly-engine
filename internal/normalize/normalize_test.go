package normalize

import (
	"os"
	"strings"
	"testing"

	"github.com/ashutoshm97/curly-engine/internal/ooxml"
	"github.com/ashutoshm97/curly-engine/internal/profile"
)

func loadAcme(t *testing.T) *profile.StyleProfile {
	t.Helper()
	data, err := os.ReadFile("../../testdata/acme.pptx")
	if err != nil {
		t.Fatal(err)
	}
	d, err := ooxml.Extract(data, ooxml.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Build(d, Options{ID: "sp_test"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAcmeProfile(t *testing.T) {
	p := loadAcme(t)
	x, err := p.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("DUMP") != "" {
		os.WriteFile("/tmp/acme-components.xml", x, 0o644)
	}
	if got := p.Color("accent1"); got != "#E94560" {
		t.Errorf("accent1 = %s", got)
	}
	if f := p.Font("heading").Family; f != "Montserrat" {
		t.Errorf("heading font = %s", f)
	}
	kinds := map[string]int{}
	for _, l := range p.Layouts {
		kinds[l.Kind]++
	}
	if kinds["title"] == 0 || kinds["data"] < 2 || kinds["content"] == 0 {
		t.Errorf("unexpected layout kinds: %v (%d layouts)", kinds, len(p.Layouts))
	}
	var hasLogo bool
	for _, pl := range p.Patterns.Placements {
		if pl.Element == "logo" && pl.Asset != "" && pl.Support == 1 {
			hasLogo = true
		}
	}
	if !hasLogo {
		t.Errorf("logo placement not detected: %+v", p.Patterns.Placements)
	}
	if len(p.Tables) == 0 || p.Tables[0].Header.Fill != "dk2" && p.Tables[0].Header.Fill != "accent2" {
		t.Errorf("table header fill: %+v", p.Tables)
	}
	// Round-trip through XML.
	back, err := profile.Parse(x)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Layouts) != len(p.Layouts) || !strings.Contains(string(x), "<styleProfile") {
		t.Errorf("round trip mismatch")
	}
}

func TestSubComponents(t *testing.T) {
	p := loadAcme(t)
	var list, img, table *profile.Component
	for i := range p.Components {
		c := &p.Components[i]
		switch c.Type {
		case "list":
			list = c
		case "image":
			img = c
		case "table":
			table = c
		}
	}
	if list == nil || list.Bullets == nil || len(list.Bullets.Levels) == 0 || list.Bullets.Levels[0].Glyph == "" || list.Bullets.Levels[0].IndentPt == 0 {
		t.Errorf("list bullets = %+v", list)
	}
	if img == nil || img.Frame == nil || img.Frame.Fit == "" {
		t.Errorf("image frame = %+v", img)
	}
	if table == nil || table.Header == nil || table.Body == nil || table.Header.Fill == "" {
		t.Fatalf("table component = %+v", table)
	}
	slots := map[string]string{}
	for _, l := range p.Layouts {
		for _, s := range l.Slots {
			if s.Type == "image" || s.Type == "table" {
				slots[s.Type] = s.Component
			}
		}
	}
	if slots["image"] != img.ID || slots["table"] != table.ID {
		t.Errorf("slot components = %v", slots)
	}
	for _, a := range p.Assets {
		if a.Format == "" || a.Kind == "" {
			t.Errorf("asset %s missing kind/format: %+v", a.ID, a)
		}
	}
	out, err := p.MarshalIndent()
	if err != nil {
		t.Fatal(err)
	}
	back, err := profile.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if c := back.Component(table.ID); c == nil || c.Header == nil || c.Header.Fill != table.Header.Fill {
		t.Errorf("table component did not round-trip: %+v", c)
	}
}
