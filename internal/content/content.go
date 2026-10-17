// Package content defines the style-free content the LLM produces. The same
// fixed shape is used for every layout so one JSON schema covers all slides.
package content

import "strings"

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
	ImagePrompt string   `json:"imagePrompt,omitempty"`
	Notes       string   `json:"notes,omitempty"`
}

type Item struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type Table struct {
	Header []string   `json:"header"`
	Rows   [][]string `json:"rows"`
}

// Clean normalizes common LLM formatting slips: bullet lists packed into one
// string, leading bullet glyphs, and empty entries.
func Clean(s *Slide) {
	var out []string
	for _, b := range s.Bullets {
		for _, line := range strings.Split(b, "\n") {
			line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "•●▪◦‣*-–— \t"))
			if line != "" {
				out = append(out, line)
			}
		}
	}
	s.Bullets = out
}
