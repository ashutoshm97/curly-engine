// Package content defines the style-free content the LLM produces. The same
// fixed shape is used for every layout so one JSON schema covers all slides.
package content

import (
	"regexp"
	"strings"
)

type Deck struct {
	Title  string  `json:"title"`
	Slides []Slide `json:"slides"`
}

type Slide struct {
	Layout      string   `json:"layout"`
	Title       string   `json:"title,omitempty"`
	Subtitle    string   `json:"subtitle,omitempty"`
	Body        string   `json:"body,omitempty"`
	Bullets     []string `json:"bullets,omitempty"`
	Items       []Item   `json:"items,omitempty"`
	Table       *Table   `json:"table,omitempty"`
	Chart       *Chart   `json:"chart,omitempty"`
	ImagePrompt string   `json:"imagePrompt,omitempty"`
	Notes       string   `json:"notes,omitempty"`
}

type Item struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Chart is chart data only; its look comes from the profile.
type Chart struct {
	Categories []string      `json:"categories"`
	Series     []ChartSeries `json:"series"`
}

type ChartSeries struct {
	Name   string    `json:"name"`
	Values []float64 `json:"values"`
}

type Table struct {
	Header []string   `json:"header"`
	Rows   [][]string `json:"rows"`
}

// listMarker matches a list prefix: a bullet glyph, or an ASCII dash/star
// followed by whitespace (so "-5%" keeps its sign).
var listMarker = regexp.MustCompile(`^(?:[•●▪◦‣]\s*|[-*–—]\s+)`)

// Clean normalizes common LLM formatting slips in Bullets: lists packed into
// one string, leading bullet markers, wrapped lines and empty entries.
func Clean(s *Slide) {
	var out []string
	for _, b := range s.Bullets {
		out = append(out, SplitList(b)...)
	}
	s.Bullets = out
}

// SplitList turns text into list items. Text with at least two marked lines
// is a packed list: each marked line starts an item, unmarked lines continue
// the previous item, and unmarked lines before the first marker form their
// own leading item. Any other text is one item with wrapped lines rejoined.
func SplitList(text string) []string {
	lines := strings.Split(text, "\n")
	marked := 0
	for _, l := range lines {
		if listMarker.MatchString(strings.TrimSpace(l)) {
			marked++
		}
	}
	if marked < 2 {
		item := strings.Join(strings.Fields(strings.Join(lines, " ")), " ")
		item = strings.TrimSpace(listMarker.ReplaceAllString(item, ""))
		if item == "" {
			return nil
		}
		return []string{item}
	}
	var items []string
	cont := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case t == "":
			cont = false
		case listMarker.MatchString(t):
			if t = strings.TrimSpace(listMarker.ReplaceAllString(t, "")); t != "" {
				items = append(items, t)
				cont = true
			}
		case cont && len(items) > 0:
			items[len(items)-1] += " " + t
		default:
			items = append(items, t)
			cont = true
		}
	}
	return items
}
