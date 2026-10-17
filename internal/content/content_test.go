package content

import (
	"reflect"
	"testing"
)

func TestClean(t *testing.T) {
	s := Slide{Bullets: []string{"• One: first\n• Two\n\n- Three", "  ", "Four — keeps dash", "1. Numbered"}}
	Clean(&s)
	want := []string{"One: first", "Two", "Three", "Four — keeps dash", "1. Numbered"}
	if !reflect.DeepEqual(s.Bullets, want) {
		t.Fatalf("got %q", s.Bullets)
	}
}

func TestCleanBodyList(t *testing.T) {
	s := Slide{Body: "Intro line\n\n• Q3: audit logs\n• Q4: API v2"}
	Clean(&s)
	if s.Body != "" || !reflect.DeepEqual(s.Bullets, []string{"Intro line", "Q3: audit logs", "Q4: API v2"}) {
		t.Fatalf("body=%q bullets=%q", s.Body, s.Bullets)
	}
	p := Slide{Body: "A plain paragraph.\nWith a wrapped second line - not a list."}
	Clean(&p)
	if p.Body == "" || len(p.Bullets) != 0 {
		t.Fatalf("paragraph changed: %+v", p)
	}
}
