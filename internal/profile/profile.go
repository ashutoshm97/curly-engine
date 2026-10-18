// Package profile defines the Style Profile (components.xml): the reusable
// design system extracted from a source deck.
package profile

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"

	"github.com/ashutoshm97/curly-engine/internal/rawdeck"
)

const SchemaVersion = "1.0"

// Box marshals as "x y w h" in slide fractions.
type Box rawdeck.Box

func (b Box) MarshalXMLAttr(name xml.Name) (xml.Attr, error) {
	return xml.Attr{Name: name, Value: fmt.Sprintf("%s %s %s %s", f3(b.X), f3(b.Y), f3(b.W), f3(b.H))}, nil
}

func (b *Box) UnmarshalXMLAttr(attr xml.Attr) error {
	parts := strings.Fields(attr.Value)
	if len(parts) != 4 {
		return fmt.Errorf("box %q: want 4 numbers", attr.Value)
	}
	vals := make([]float64, 4)
	for i, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return fmt.Errorf("box %q: %w", attr.Value, err)
		}
		vals[i] = v
	}
	*b = Box{X: vals[0], Y: vals[1], W: vals[2], H: vals[3]}
	return nil
}

func (b Box) Raw() rawdeck.Box { return rawdeck.Box(b) }

func f3(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

type StyleProfile struct {
	XMLName       xml.Name     `xml:"styleProfile" json:"-"`
	ID            string       `xml:"id,attr" json:"id"`
	Name          string       `xml:"name,attr,omitempty" json:"name,omitempty"`
	Version       int          `xml:"version,attr" json:"version"`
	SchemaVersion string       `xml:"schemaVersion,attr" json:"schemaVersion"`
	Source        string       `xml:"source,attr" json:"source"`
	SourceType    string       `xml:"sourceType,attr" json:"sourceType"`
	Aspect        string       `xml:"aspect,attr" json:"aspect"`
	WidthPt       float64      `xml:"widthPt,attr" json:"widthPt"`
	HeightPt      float64      `xml:"heightPt,attr" json:"heightPt"`
	Tokens        Tokens       `xml:"tokens" json:"tokens"`
	Components    []Component  `xml:"components>component" json:"components"`
	Tables        []TableStyle `xml:"tables>tableStyle" json:"tables,omitempty"`
	Layouts       []Layout     `xml:"layouts>layout" json:"layouts"`
	Patterns      Patterns     `xml:"patterns" json:"patterns"`
	Assets        []Asset      `xml:"assets>asset" json:"assets,omitempty"`
}

type Tokens struct {
	Colors    []ColorToken `xml:"colors>color" json:"colors"`
	Fonts     []FontToken  `xml:"fonts>font" json:"fonts"`
	TypeScale TypeScale    `xml:"typeScale" json:"typeScale"`
	Spacing   Spacing      `xml:"spacing" json:"spacing"`
	Radii     []Radius     `xml:"radii>radius" json:"radii,omitempty"`
}

type ColorToken struct {
	Name    string `xml:"name,attr" json:"name"`
	Value   string `xml:"value,attr" json:"value"`
	Derived bool   `xml:"derived,attr,omitempty" json:"derived,omitempty"`
}

type FontToken struct {
	Role     string `xml:"role,attr" json:"role"`
	Family   string `xml:"family,attr" json:"family"`
	Fallback string `xml:"fallback,attr,omitempty" json:"fallback,omitempty"`
}

type TypeScale struct {
	Unit  string `xml:"unit,attr" json:"unit"`
	Steps []Step `xml:"step" json:"steps"`
}

type Step struct {
	Name       string  `xml:"name,attr" json:"name"`
	Size       float64 `xml:"size,attr" json:"size"`
	LineHeight float64 `xml:"lineHeight,attr" json:"lineHeight"`
}

type Spacing struct {
	Unit   string  `xml:"unit,attr" json:"unit"`
	Grid   int     `xml:"grid,attr" json:"grid"`
	Gutter float64 `xml:"gutter,attr" json:"gutter"`
	Margin string  `xml:"margin,attr" json:"margin"` // top right bottom left
}

type Radius struct {
	Name string  `xml:"name,attr" json:"name"`
	Px   float64 `xml:"px,attr" json:"px"`
}

type TextSpec struct {
	Font   string `xml:"font,attr" json:"font"`
	Step   string `xml:"step,attr" json:"step"`
	Color  string `xml:"color,attr" json:"color"`
	Align  string `xml:"align,attr,omitempty" json:"align,omitempty"`
	Bold   bool   `xml:"bold,attr,omitempty" json:"bold,omitempty"`
	Italic bool   `xml:"italic,attr,omitempty" json:"italic,omitempty"`
}

type Marker struct {
	Glyph string `xml:"glyph,attr" json:"glyph"`
	Color string `xml:"color,attr,omitempty" json:"color,omitempty"`
}

type ShapeSpec struct {
	Kind   string  `xml:"kind,attr" json:"kind"`
	Fill   string  `xml:"fill,attr,omitempty" json:"fill,omitempty"`
	Line   string  `xml:"line,attr,omitempty" json:"line,omitempty"`
	Radius float64 `xml:"radius,attr,omitempty" json:"radius,omitempty"`
	Shadow bool    `xml:"shadow,attr,omitempty" json:"shadow,omitempty"`
	PadPt  float64 `xml:"padPt,attr,omitempty" json:"padPt,omitempty"`
}

// BulletLevel is the marker and paragraph spacing of one list level.
type BulletLevel struct {
	N             int     `xml:"n,attr" json:"n"`
	Glyph         string  `xml:"glyph,attr" json:"glyph"`
	Color         string  `xml:"color,attr,omitempty" json:"color,omitempty"`
	IndentPt      float64 `xml:"indentPt,attr,omitempty" json:"indentPt,omitempty"`
	HangPt        float64 `xml:"hangPt,attr,omitempty" json:"hangPt,omitempty"`
	SpaceBeforePt float64 `xml:"spaceBeforePt,attr,omitempty" json:"spaceBeforePt,omitempty"`
	LineHeight    float64 `xml:"lineHeight,attr,omitempty" json:"lineHeight,omitempty"`
}

type Bullets struct {
	Levels []BulletLevel `xml:"level" json:"levels"`
}

// Frame is how pictures are framed: border, corners, shadow and fit.
type Frame struct {
	Geometry    string  `xml:"geometry,attr,omitempty" json:"geometry,omitempty"`
	Line        string  `xml:"line,attr,omitempty" json:"line,omitempty"`
	LineWidthPt float64 `xml:"lineWidthPt,attr,omitempty" json:"lineWidthPt,omitempty"`
	Radius      float64 `xml:"radius,attr,omitempty" json:"radius,omitempty"`
	Shadow      bool    `xml:"shadow,attr,omitempty" json:"shadow,omitempty"`
	Fit         string  `xml:"fit,attr,omitempty" json:"fit,omitempty"` // cover|contain
}

type Caption struct {
	Position string    `xml:"position,attr" json:"position"` // below|above
	GapPt    float64   `xml:"gapPt,attr,omitempty" json:"gapPt,omitempty"`
	Text     *TextSpec `xml:"text" json:"text,omitempty"`
}

// ChartSpec is a chart's look; generated decks supply only the data.
type ChartSpec struct {
	Type     string        `xml:"type,attr" json:"type"`
	Grouping string        `xml:"grouping,attr,omitempty" json:"grouping,omitempty"`
	GapWidth int           `xml:"gapWidth,attr,omitempty" json:"gapWidth,omitempty"`
	HolePct  int           `xml:"holePct,attr,omitempty" json:"holePct,omitempty"`
	Legend   string        `xml:"legend,attr,omitempty" json:"legend,omitempty"`
	Series   []SeriesStyle `xml:"series" json:"series,omitempty"`
	Labels   *ChartLabels  `xml:"labels" json:"labels,omitempty"`
	Axis     *ChartAxis    `xml:"axis" json:"axis,omitempty"`
}

type SeriesStyle struct {
	Color string `xml:"color,attr" json:"color"`
}

type ChartLabels struct {
	Color  string  `xml:"color,attr,omitempty" json:"color,omitempty"`
	SizePt float64 `xml:"sizePt,attr,omitempty" json:"sizePt,omitempty"`
	NumFmt string  `xml:"numFmt,attr,omitempty" json:"numFmt,omitempty"`
}

type ChartAxis struct {
	Line      string  `xml:"line,attr,omitempty" json:"line,omitempty"`
	Gridlines bool    `xml:"gridlines,attr,omitempty" json:"gridlines,omitempty"`
	Grid      string  `xml:"grid,attr,omitempty" json:"grid,omitempty"`
	SizePt    float64 `xml:"sizePt,attr,omitempty" json:"sizePt,omitempty"`
}

type TableCell struct {
	PadPt         float64 `xml:"padPt,attr,omitempty" json:"padPt,omitempty"`
	Border        string  `xml:"border,attr,omitempty" json:"border,omitempty"`
	BorderWidthPt float64 `xml:"borderWidthPt,attr,omitempty" json:"borderWidthPt,omitempty"`
}

type ComponentSlot struct {
	Name string    `xml:"name,attr" json:"name"`
	Box  Box       `xml:"box,attr" json:"box"` // relative to the component box
	Text *TextSpec `xml:"text" json:"text,omitempty"`
}

type Component struct {
	ID         string          `xml:"id,attr" json:"id"`
	Type       string          `xml:"type,attr" json:"type"` // text|list|group|image|table|chart
	Role       string          `xml:"role,attr,omitempty" json:"role,omitempty"`
	Confidence float64         `xml:"confidence,attr,omitempty" json:"confidence,omitempty"`
	Text       *TextSpec       `xml:"text" json:"text,omitempty"`
	Marker     *Marker         `xml:"marker" json:"marker,omitempty"`
	Shape      *ShapeSpec      `xml:"shape" json:"shape,omitempty"`
	Slots      []ComponentSlot `xml:"slot" json:"slots,omitempty"`
	// Sub-components.
	Bullets *Bullets     `xml:"bullets" json:"bullets,omitempty"`
	Box     *ShapeSpec   `xml:"box" json:"box,omitempty"` // panel behind a list
	Frame   *Frame       `xml:"frame" json:"frame,omitempty"`
	Caption *Caption     `xml:"caption" json:"caption,omitempty"`
	Header  *TableHeader `xml:"header" json:"header,omitempty"`
	Body    *TableBody   `xml:"body" json:"body,omitempty"`
	Cell    *TableCell   `xml:"cell" json:"cell,omitempty"`
	Chart   *ChartSpec   `xml:"chart" json:"chart,omitempty"`
}

type TableStyle struct {
	ID            string      `xml:"id,attr" json:"id"`
	SourceStyleID string      `xml:"sourceStyleId,attr,omitempty" json:"sourceStyleId,omitempty"`
	Header        TableHeader `xml:"header" json:"header"`
	Body          TableBody   `xml:"body" json:"body"`
	Border        string      `xml:"border,attr,omitempty" json:"border,omitempty"`
}

type TableHeader struct {
	Fill string `xml:"fill,attr,omitempty" json:"fill,omitempty"`
	Text string `xml:"text,attr" json:"text"`
	Bold bool   `xml:"bold,attr,omitempty" json:"bold,omitempty"`
	Step string `xml:"step,attr" json:"step"`
}

type TableBody struct {
	Fill     string `xml:"fill,attr,omitempty" json:"fill,omitempty"`
	BandRows bool   `xml:"bandRows,attr,omitempty" json:"bandRows,omitempty"`
	BandFill string `xml:"bandFill,attr,omitempty" json:"bandFill,omitempty"`
	Text     string `xml:"text,attr" json:"text"`
	Step     string `xml:"step,attr" json:"step"`
}

type Layout struct {
	ID           string   `xml:"id,attr" json:"id"`
	Name         string   `xml:"name,attr" json:"name"`
	Kind         string   `xml:"kind,attr" json:"kind"` // title|section|content|data|closing
	Freq         float64  `xml:"freq,attr" json:"freq"`
	SourceSlides string   `xml:"sourceSlides,attr,omitempty" json:"sourceSlides,omitempty"`
	Description  string   `xml:"description,omitempty" json:"description,omitempty"`
	Background   string   `xml:"background,attr,omitempty" json:"background,omitempty"`
	Slots        []Slot   `xml:"slot" json:"slots"`
	Repeats      []Repeat `xml:"repeat" json:"repeats,omitempty"`
	Decor        []Decor  `xml:"decor" json:"decor,omitempty"`
}

type Slot struct {
	Name       string  `xml:"name,attr" json:"name"`
	Type       string  `xml:"type,attr" json:"type"` // text|list|image|table|chart
	Component  string  `xml:"component,attr,omitempty" json:"component,omitempty"`
	Box        Box     `xml:"box,attr" json:"box"`
	MaxChars   int     `xml:"maxChars,attr,omitempty" json:"maxChars,omitempty"`
	MinStep    string  `xml:"minStep,attr,omitempty" json:"minStep,omitempty"`
	TableStyle string  `xml:"tableStyle,attr,omitempty" json:"tableStyle,omitempty"`
	MaxRows    int     `xml:"maxRows,attr,omitempty" json:"maxRows,omitempty"`
	MaxCols    int     `xml:"maxCols,attr,omitempty" json:"maxCols,omitempty"`
	Radius     float64 `xml:"radius,attr,omitempty" json:"radius,omitempty"`
	Optional   bool    `xml:"optional,attr,omitempty" json:"optional,omitempty"`
	Example    string  `xml:"example,attr,omitempty" json:"example,omitempty"`
}

type Repeat struct {
	Name      string  `xml:"name,attr" json:"name"`
	Component string  `xml:"component,attr" json:"component"`
	Min       int     `xml:"min,attr" json:"min"`
	Max       int     `xml:"max,attr" json:"max"`
	Region    Box     `xml:"region,attr" json:"region"`
	Direction string  `xml:"direction,attr" json:"direction"`
	Gap       float64 `xml:"gap,attr" json:"gap"`
}

type Decor struct {
	Kind     string    `xml:"kind,attr" json:"kind"` // shape|image|text
	Role     string    `xml:"role,attr,omitempty" json:"role,omitempty"`
	Box      Box       `xml:"box,attr" json:"box"`
	Fill     string    `xml:"fill,attr,omitempty" json:"fill,omitempty"`
	Geometry string    `xml:"geometry,attr,omitempty" json:"geometry,omitempty"`
	Radius   float64   `xml:"radius,attr,omitempty" json:"radius,omitempty"`
	Asset    string    `xml:"asset,attr,omitempty" json:"asset,omitempty"`
	Text     string    `xml:"text,attr,omitempty" json:"text,omitempty"`
	TextSpec *TextSpec `xml:"text" json:"textSpec,omitempty"`
}

type Patterns struct {
	Placements  []Placement  `xml:"placement" json:"placements,omitempty"`
	Sequence    string       `xml:"sequence,omitempty" json:"sequence,omitempty"`
	Transitions []Transition `xml:"transition" json:"transitions,omitempty"`
}

type Placement struct {
	Element string  `xml:"element,attr" json:"element"`
	Anchor  string  `xml:"anchor,attr" json:"anchor"`
	Box     Box     `xml:"box,attr" json:"box"`
	Support float64 `xml:"support,attr" json:"support"`
	Asset   string  `xml:"asset,attr,omitempty" json:"asset,omitempty"`
}

type Transition struct {
	From  string `xml:"from,attr" json:"from"`
	To    string `xml:"to,attr" json:"to"`
	Count int    `xml:"count,attr" json:"count"`
}

type Asset struct {
	ID   string `xml:"id,attr" json:"id"`
	Ref  string `xml:"ref,attr" json:"ref"`   // sha256 in the blob store
	Kind string `xml:"kind,attr" json:"kind"` // logo|icon|image
	Mime string `xml:"mime,attr" json:"mime"`
	// Format is the file type (svg|png|jpeg|emf|…); Alt names a browser-renderable twin asset.
	Format string `xml:"format,attr,omitempty" json:"format,omitempty"`
	Alt    string `xml:"alt,attr,omitempty" json:"alt,omitempty"`
}

func (p *StyleProfile) MarshalIndent() ([]byte, error) {
	b, err := xml.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(b, '\n')...), nil
}

func Parse(data []byte) (*StyleProfile, error) {
	var p StyleProfile
	if err := xml.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Validate checks referential integrity (the XSD-equivalent rules we rely on).
func (p *StyleProfile) Validate() error {
	if p.WidthPt <= 0 || p.HeightPt <= 0 {
		return fmt.Errorf("profile: invalid slide size")
	}
	comps := map[string]bool{}
	for _, c := range p.Components {
		comps[c.ID] = true
	}
	tables := map[string]bool{}
	for _, t := range p.Tables {
		tables[t.ID] = true
	}
	steps := map[string]bool{}
	for _, s := range p.Tokens.TypeScale.Steps {
		steps[s.Name] = true
	}
	if len(p.Layouts) == 0 {
		return fmt.Errorf("profile: no layouts")
	}
	ids := map[string]bool{}
	for _, l := range p.Layouts {
		if ids[l.ID] {
			return fmt.Errorf("profile: duplicate layout %s", l.ID)
		}
		ids[l.ID] = true
		for _, s := range l.Slots {
			if s.Component != "" && !comps[s.Component] {
				return fmt.Errorf("layout %s slot %s: unknown component %s", l.ID, s.Name, s.Component)
			}
			if s.TableStyle != "" && !tables[s.TableStyle] {
				return fmt.Errorf("layout %s slot %s: unknown tableStyle %s", l.ID, s.Name, s.TableStyle)
			}
			if s.MinStep != "" && !steps[s.MinStep] {
				return fmt.Errorf("layout %s slot %s: unknown step %s", l.ID, s.Name, s.MinStep)
			}
		}
		for _, r := range l.Repeats {
			if !comps[r.Component] {
				return fmt.Errorf("layout %s repeat %s: unknown component %s", l.ID, r.Name, r.Component)
			}
		}
	}
	return nil
}

// Helpers used by the layout engine and renderers.

func (p *StyleProfile) Color(ref string) string {
	if ref == "" {
		return ""
	}
	if strings.HasPrefix(ref, "#") {
		return ref
	}
	for _, c := range p.Tokens.Colors {
		if c.Name == ref {
			return c.Value
		}
	}
	return "#000000"
}

func (p *StyleProfile) Font(role string) FontToken {
	for _, f := range p.Tokens.Fonts {
		if f.Role == role {
			return f
		}
	}
	if len(p.Tokens.Fonts) > 0 {
		return p.Tokens.Fonts[0]
	}
	return FontToken{Role: role, Family: "Arial"}
}

func (p *StyleProfile) Step(name string) Step {
	for _, s := range p.Tokens.TypeScale.Steps {
		if s.Name == name {
			return s
		}
	}
	return Step{Name: name, Size: 18, LineHeight: 1.2}
}

// StepsDesc returns step names from largest to smallest.
func (p *StyleProfile) StepsBelow(name string) []Step {
	var out []Step
	cur := p.Step(name).Size
	for _, s := range p.Tokens.TypeScale.Steps {
		if s.Size < cur {
			out = append(out, s)
		}
	}
	return out
}

func (p *StyleProfile) Component(id string) *Component {
	for i := range p.Components {
		if p.Components[i].ID == id {
			return &p.Components[i]
		}
	}
	return nil
}

func (p *StyleProfile) Layout(id string) *Layout {
	for i := range p.Layouts {
		if p.Layouts[i].ID == id {
			return &p.Layouts[i]
		}
	}
	return nil
}

func (p *StyleProfile) TableStyle(id string) *TableStyle {
	for i := range p.Tables {
		if p.Tables[i].ID == id {
			return &p.Tables[i]
		}
	}
	if len(p.Tables) > 0 {
		return &p.Tables[0]
	}
	return nil
}

func (p *StyleProfile) Asset(id string) *Asset {
	for i := range p.Assets {
		if p.Assets[i].ID == id {
			return &p.Assets[i]
		}
	}
	return nil
}
