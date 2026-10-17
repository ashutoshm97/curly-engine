package content

import (
	"reflect"
	"testing"
)

func TestClean(t *testing.T) {
	s := Slide{Bullets: []string{
		"• One: first\n• Two\n  continues here\n\n- Three",
		"  ",
		"-5% churn",
		"Expand into\nEMEA next quarter",
		"* Starred point",
		"Four — keeps dash",
		"1. Numbered",
	}}
	Clean(&s)
	want := []string{"One: first", "Two continues here", "Three", "-5% churn", "Expand into EMEA next quarter", "Starred point", "Four — keeps dash", "1. Numbered"}
	if !reflect.DeepEqual(s.Bullets, want) {
		t.Fatalf("got %q", s.Bullets)
	}
}

func TestSplitList(t *testing.T) {
	cases := map[string][]string{
		"Summary:\n• Revenue rose\n• Costs fell":                {"Summary:", "Revenue rose", "Costs fell"},
		"A plain paragraph.\nWith a wrapped line - not a list.": {"A plain paragraph. With a wrapped line - not a list."},
		"-5% churn\n-3% CAC":                                    {"-5% churn -3% CAC"},
		"":                                                      nil,
	}
	for in, want := range cases {
		if got := SplitList(in); !reflect.DeepEqual(got, want) {
			t.Errorf("SplitList(%q) = %q, want %q", in, got, want)
		}
	}
}
