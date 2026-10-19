// Package rawdeck is the source-agnostic intermediate representation produced
// by the PPTX and HTML extractors and consumed by the normalizer.
package rawdeck

// Box is a rectangle in fractions of the slide (0..1).
type Box struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

func (b Box) Area() float64  { return b.W * b.H }
func (b Box) CX() float64    { return b.X + b.W/2 }
func (b Box) CY() float64    { return b.Y + b.H/2 }
func (b Box) Right() float64 { return b.X + b.W }
func (b Box) Bottom() float64 {
	return b.Y + b.H
}

// IoU returns intersection-over-union of two boxes.
func (b Box) IoU(o Box) float64 {
	ix := max(0, min(b.Right(), o.Right())-max(b.X, o.X))
	iy := max(0, min(b.Bottom(), o.Bottom())-max(b.Y, o.Y))
	inter := ix * iy
	union := b.Area() + o.Area() - inter
	if union <= 0 {
		return 0
	}
	return inter / union
}

type Kind string

const (
	KindText  Kind = "text"
	KindImage Kind = "image"
	KindTable Kind = "table"
	KindShape Kind = "shape"
	KindChart Kind = "chart"
)

type Role string

const (
	RoleTitle      Role = "title"
	RoleSubtitle   Role = "subtitle"
	RoleBody       Role = "body"
	RoleImage      Role = "image"
	RoleTable      Role = "table"
	RoleChart      Role = "chart"
	RoleFooter     Role = "footer"
	RolePageNumber Role = "pageNumber"
	RoleLogo       Role = "logo"
	RoleDecor      Role = "decor"
	RoleCard       Role = "card"
	RoleUnknown    Role = ""
)

// Color is a resolved hex value plus the theme token it came from (if any).
type Color struct {
	Hex   string `json:"hex,omitempty"`
	Token string `json:"token,omitempty"`
}

func (c Color) IsZero() bool { return c.Hex == "" && c.Token == "" }

type TextStyle struct {
	Font          string  `json:"font,omitempty"`
	FontRole      string  `json:"fontRole,omitempty"` // heading|body when theme-referenced
	SizePt        float64 `json:"sizePt,omitempty"`
	Bold          bool    `json:"bold,omitempty"`
	Italic        bool    `json:"italic,omitempty"`
	Color         Color   `json:"color,omitempty"`
	Align         string  `json:"align,omitempty"` // l|ctr|r|just
	Bullet        string  `json:"bullet,omitempty"`
	Paragraphs    int     `json:"paragraphs,omitempty"`
	MaxLevel      int     `json:"maxLevel,omitempty"`
	BulletColor   Color   `json:"bulletColor,omitempty"`
	IndentPt      float64 `json:"indentPt,omitempty"`
	HangPt        float64 `json:"hangPt,omitempty"`
	SpaceBeforePt float64 `json:"spaceBeforePt,omitempty"`
	LineHeight    float64 `json:"lineHeight,omitempty"`
}

type Table struct {
	Rows        int        `json:"rows"`
	Cols        int        `json:"cols"`
	StyleID     string     `json:"styleId,omitempty"`
	FirstRow    bool       `json:"firstRow,omitempty"`
	BandRow     bool       `json:"bandRow,omitempty"`
	HeaderFill  Color      `json:"headerFill,omitempty"`
	HeaderText  Color      `json:"headerText,omitempty"`
	BodyFill    Color      `json:"bodyFill,omitempty"`
	BandFill    Color      `json:"bandFill,omitempty"`
	BodyText    Color      `json:"bodyText,omitempty"`
	HeaderBold  bool       `json:"headerBold,omitempty"`
	FontSizePt  float64    `json:"fontSizePt,omitempty"`
	BorderColor Color      `json:"borderColor,omitempty"`
	Cells       [][]string `json:"cells,omitempty"`
}

type Element struct {
	Kind            Kind       `json:"kind"`
	Role            Role       `json:"role,omitempty"`
	PlaceholderType string     `json:"placeholderType,omitempty"`
	Box             Box        `json:"box"`
	Text            string     `json:"text,omitempty"`
	Style           TextStyle  `json:"style,omitempty"`
	Fill            Color      `json:"fill,omitempty"`
	Line            Color      `json:"line,omitempty"`
	Geometry        string     `json:"geometry,omitempty"`
	RadiusPx        float64    `json:"radiusPx,omitempty"`
	Shadow          bool       `json:"shadow,omitempty"`
	LineWidthPt     float64    `json:"lineWidthPt,omitempty"`
	AltImageRef     string     `json:"altImageRef,omitempty"` // browser-renderable alternative (e.g. SVG next to an EMF)
	ImageRef        string     `json:"imageRef,omitempty"`    // sha256 of the image bytes
	Table           *Table     `json:"table,omitempty"`
	Chart           *Chart     `json:"chart,omitempty"`
	GroupID         int        `json:"groupId,omitempty"`
	Inherited       bool       `json:"inherited,omitempty"` // drawn from master/layout, not the slide itself
	Children        []*Element `json:"children,omitempty"`
}

type Slide struct {
	Index      int        `json:"index"`
	LayoutRef  string     `json:"layoutRef,omitempty"`
	Background Color      `json:"background,omitempty"`
	Elements   []*Element `json:"elements"`
}

// DeclaredLayout is a layout the author defined explicitly (PPTX slideLayout).
type DeclaredLayout struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Type       string     `json:"type"`
	Background Color      `json:"background,omitempty"`
	Elements   []*Element `json:"elements"`
}

type NamedColor struct {
	Name string `json:"name"`
	Hex  string `json:"hex"`
}

type Theme struct {
	Colors      []NamedColor `json:"colors"`
	HeadingFont string       `json:"headingFont,omitempty"`
	BodyFont    string       `json:"bodyFont,omitempty"`
}

func (t Theme) Lookup(name string) string {
	for _, c := range t.Colors {
		if c.Name == name {
			return c.Hex
		}
	}
	return ""
}

type Asset struct {
	Ref  string `json:"ref"`
	Mime string `json:"mime"`
	Data []byte `json:"-"`
}

type Deck struct {
	SourceType string           `json:"sourceType"`
	SourceHash string           `json:"sourceHash"`
	WidthPt    float64          `json:"widthPt"`
	HeightPt   float64          `json:"heightPt"`
	Theme      Theme            `json:"theme"`
	Layouts    []DeclaredLayout `json:"layouts,omitempty"`
	Slides     []*Slide         `json:"slides"`
	Assets     map[string]Asset `json:"-"`
}

func (d *Deck) AddAsset(ref, mime string, data []byte) {
	if d.Assets == nil {
		d.Assets = map[string]Asset{}
	}
	d.Assets[ref] = Asset{Ref: ref, Mime: mime, Data: data}
}

// Chart is the style of a native chart (data is not kept).
type Chart struct {
	Type        string  `json:"type"`               // col|bar|line|area|pie|doughnut
	Grouping    string  `json:"grouping,omitempty"` // clustered|stacked|percentStacked|standard
	Series      []Color `json:"series,omitempty"`   // series colors (pie: slice colors)
	DataLabels  bool    `json:"dataLabels,omitempty"`
	LabelColor  Color   `json:"labelColor,omitempty"`
	LabelSizePt float64 `json:"labelSizePt,omitempty"`
	NumFmt      string  `json:"numFmt,omitempty"`
	Legend      string  `json:"legend,omitempty"` // b|t|l|r, empty = none
	Gridlines   bool    `json:"gridlines,omitempty"`
	GridColor   Color   `json:"gridColor,omitempty"`
	AxisColor   Color   `json:"axisColor,omitempty"`
	FontSizePt  float64 `json:"fontSizePt,omitempty"`
	GapWidth    int     `json:"gapWidth,omitempty"`
	HolePct     int     `json:"holePct,omitempty"`
}
