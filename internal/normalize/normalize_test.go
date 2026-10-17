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
