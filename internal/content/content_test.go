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
