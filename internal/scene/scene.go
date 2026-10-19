// Package scene is the resolved, renderer-agnostic slide model: absolute
// positions in points, literal colors and fonts, pre-wrapped text lines.
package scene

type Deck struct {
	ProfileID string   `json:"profileId"`
	Title     string   `json:"title"`
	WidthPt   float64  `json:"widthPt"`
	HeightPt  float64  `json:"heightPt"`
	Slides    []*Slide `json:"slides"`
}

type Slide struct {
	Index      int        `json:"index"`
	Layout     string     `json:"layout"`
	Background string     `json:"background"`
	Elements   []*Element `json:"elements"`
	Notes      string     `json:"notes,omitempty"`
}

type Element struct {
	Kind  string  `json:"kind"` // text|shape|image|table
	Role  string  `json:"role,omitempty"`
	Slot  string  `json:"slot,omitempty"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	W     float64 `json:"w"`
	H     float64 `json:"h"`
	Text  *Text   `json:"text,omitempty"`
	Shape *Shape  `json:"shape,omitempty"`
	Image *Image  `json:"image,omitempty"`
	Table *Table  `json:"table,omitempty"`
}

type Text struct {
	Paragraphs  []string `json:"paragraphs"`
	Lines       []Line   `json:"lines"`
	Font        string   `json:"font"`
	Fallback    string   `json:"fallback,omitempty"`
	SizePt      float64  `json:"sizePt"`
	LineHeight  float64  `json:"lineHeight"`
	Color       string   `json:"color"`
	Bold        bool     `json:"bold,omitempty"`
	Italic      bool     `json:"italic,omitempty"`
	Align       string   `json:"align,omitempty"` // l|ctr|r|just
	Bullet      string   `json:"bullet,omitempty"`
	BulletColor string   `json:"bulletColor,omitempty"`
	Truncated   bool     `json:"truncated,omitempty"`
	InsetX      float64  `json:"insetX"`
	InsetY      float64  `json:"insetY"`
}

// Line is one wrapped line; X and Baseline are offsets from the element's top-left.
type Line struct {
	Text     string  `json:"t"`
	X        float64 `json:"x"`
	Baseline float64 `json:"b"`
	Bullet   bool    `json:"bullet,omitempty"`
}

type Shape struct {
	Geometry string  `json:"geometry"` // rect|roundRect|ellipse
	Fill     string  `json:"fill,omitempty"`
	Line     string  `json:"line,omitempty"`
	RadiusPt float64 `json:"radiusPt,omitempty"`
	Shadow   bool    `json:"shadow,omitempty"`
}

type Image struct {
	Ref         string  `json:"ref,omitempty"`
	Mime        string  `json:"mime,omitempty"`
	Placeholder string  `json:"placeholder,omitempty"` // shown when no image is available
	Fill        string  `json:"fill,omitempty"`
	TextColor   string  `json:"textColor,omitempty"`
	RadiusPt    float64 `json:"radiusPt,omitempty"`
}

type Table struct {
	Header     []string   `json:"header"`
	Rows       [][]string `json:"rows"`
	Font       string     `json:"font"`
	SizePt     float64    `json:"sizePt"`
	HeaderFill string     `json:"headerFill,omitempty"`
	HeaderText string     `json:"headerText"`
	HeaderBold bool       `json:"headerBold,omitempty"`
	BodyFill   string     `json:"bodyFill,omitempty"`
	BandFill   string     `json:"bandFill,omitempty"`
	BodyText   string     `json:"bodyText"`
	Border     string     `json:"border,omitempty"`
}
