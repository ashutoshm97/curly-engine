package htmlx

import (
	"context"
	"os"
	"testing"

	"github.com/ashutoshm97/curly-engine/internal/normalize"
	"github.com/ashutoshm97/curly-engine/internal/rawdeck"
)

func TestExtractNimbus(t *testing.T) {
	if ChromePath() == "" && os.Getenv("CHROME_PATH") == "" {
		t.Skip("chrome not available")
	}
	html, err := os.ReadFile("../../testdata/nimbus.html")
	if err != nil {
		t.Fatal(err)
	}
	x := New(2)
	defer x.Close()
	d, err := x.Extract(context.Background(), html)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Slides) != 6 {
		t.Fatalf("slides = %d, want 6", len(d.Slides))
	}
	var table, img bool
	for _, s := range d.Slides {
		for _, el := range s.Elements {
			if el.Kind == rawdeck.KindTable && el.Table.HeaderFill.Hex == "#6C3CE1" {
				table = true
			}
			if el.Kind == rawdeck.KindImage && el.ImageRef != "" {
				img = true
			}
		}
	}
	if !table || !img {
		t.Errorf("table=%v img=%v", table, img)
	}
	if d.Slides[0].Background.Hex != "#1B1F3B" {
		t.Errorf("bg = %v", d.Slides[0].Background)
	}
}

func TestNimbusProfile(t *testing.T) {
	if ChromePath() == "" {
		t.Skip("chrome not available")
	}
	html, _ := os.ReadFile("../../testdata/nimbus.html")
	x := New(1)
	defer x.Close()
	d, err := x.Extract(context.Background(), html)
	if err != nil {
		t.Fatal(err)
	}
	p, err := normalize.Build(d, normalize.Options{ID: "nimbus", Name: "Nimbus"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Font("heading").Family != "Playfair Display" {
		t.Errorf("heading font = %q", p.Font("heading").Family)
	}
	if len(p.Layouts) < 3 || len(p.Tables) == 0 {
		t.Errorf("layouts=%d tables=%d", len(p.Layouts), len(p.Tables))
	}
	if os.Getenv("DUMP") != "" {
		b, _ := p.MarshalIndent()
		os.WriteFile("/tmp/nimbus-components.xml", b, 0o644)
	}
}
