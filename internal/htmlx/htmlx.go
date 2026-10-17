// Package htmlx measures HTML presentations in headless Chrome (chromedp) and
// converts computed styles and layout boxes into a rawdeck.Deck.
package htmlx

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/ashutoshm97/curly-engine/internal/blob"
	"github.com/ashutoshm97/curly-engine/internal/rawdeck"
)

//go:embed extract.js
var extractJS string

// Extractor owns one headless browser; each Extract call runs in its own tab.
type Extractor struct {
	mu       sync.Mutex
	alloc    context.Context
	cancel   context.CancelFunc
	browser  context.Context
	sem      chan struct{}
	Timeout  time.Duration
	execPath string
}

// ChromePath resolves the Chrome binary: $CHROME_PATH, known install paths, then PATH lookup by chromedp.
func ChromePath() string {
	if p := os.Getenv("CHROME_PATH"); p != "" {
		return p
	}
	if m, _ := filepath.Glob("/opt/.devin/chrome/chrome/linux-*/chrome-linux64/chrome"); len(m) > 0 {
		return m[0]
	}
	for _, c := range []string{"/usr/bin/google-chrome-stable", "/usr/bin/google-chrome", "/opt/google/chrome/chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser"} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func New(maxTabs int) *Extractor {
	if maxTabs <= 0 {
		maxTabs = 4
	}
	return &Extractor{sem: make(chan struct{}, maxTabs), Timeout: 45 * time.Second, execPath: ChromePath()}
}

func (x *Extractor) start() (context.Context, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.browser != nil && x.browser.Err() == nil {
		return x.browser, nil
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.WindowSize(1280, 720),
		chromedp.Flag("disable-background-networking", true),
		// Containers usually lack the user namespaces Chrome's sandbox needs.
		chromedp.Flag("no-sandbox", os.Getenv("CHROME_NO_SANDBOX") == "1"),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	if x.execPath != "" {
		opts = append(opts, chromedp.ExecPath(x.execPath))
	}
	x.alloc, x.cancel = chromedp.NewExecAllocator(context.Background(), opts...)
	b, _ := chromedp.NewContext(x.alloc)
	if err := chromedp.Run(b); err != nil {
		x.cancel()
		return nil, fmt.Errorf("start chrome: %w", err)
	}
	x.browser = b
	return b, nil
}

func (x *Extractor) Close() {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.cancel != nil {
		x.cancel()
		x.browser = nil
	}
}

type jsSlide struct {
	Index      int         `json:"index"`
	Width      float64     `json:"width"`
	Height     float64     `json:"height"`
	Background string      `json:"background"`
	Elements   []jsElement `json:"elements"`
}

type jsElement struct {
	Kind        string      `json:"kind"`
	Box         rawdeck.Box `json:"box"`
	Text        string      `json:"text"`
	Paragraphs  int         `json:"paragraphs"`
	Bullet      string      `json:"bullet"`
	Tag         string      `json:"tag"`
	Src         string      `json:"src"`
	Fill        string      `json:"fill"`
	Radius      float64     `json:"radius"`
	Shadow      bool        `json:"shadow"`
	BorderWidth float64     `json:"borderWidth"`
	BorderColor string      `json:"borderColor"`
	Style       struct {
		Font   string  `json:"font"`
		Size   float64 `json:"size"`
		Weight int     `json:"weight"`
		Italic bool    `json:"italic"`
		Color  string  `json:"color"`
		Align  string  `json:"align"`
	} `json:"style"`
	Cells      [][]string `json:"cells"`
	HeaderFill string     `json:"headerFill"`
	HeaderText string     `json:"headerText"`
	HeaderBold bool       `json:"headerBold"`
	BodyFill   string     `json:"bodyFill"`
	BandFill   string     `json:"bandFill"`
	BodyText   string     `json:"bodyText"`
	Size       float64    `json:"size"`
	Border     string     `json:"border"`
}

// Extract renders the HTML (network blocked) and returns the measured deck.
func (x *Extractor) Extract(ctx context.Context, html []byte) (*rawdeck.Deck, error) {
	browser, err := x.start()
	if err != nil {
		return nil, err
	}
	select {
	case x.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-x.sem }()
	tab, cancelTab := chromedp.NewContext(browser)
	defer cancelTab()
	tab, cancelT := context.WithTimeout(tab, x.Timeout)
	defer cancelT()
	stop := context.AfterFunc(ctx, cancelTab)
	defer stop()

	var raw string
	err = chromedp.Run(tab,
		network.Enable(),
		network.SetBlockedURLS([]string{"http://*", "https://*", "ws://*", "wss://*", "file://*"}),
		chromedp.EmulateViewport(1280, 720),
		chromedp.Navigate("about:blank"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			tree, err := page.GetFrameTree().Do(ctx)
			if err != nil {
				return err
			}
			return page.SetDocumentContent(tree.Frame.ID, string(html)).Do(ctx)
		}),
		chromedp.Sleep(150*time.Millisecond),
		chromedp.Evaluate(extractJS, &raw),
	)
	if err != nil {
		return nil, fmt.Errorf("render html: %w", err)
	}
	var slides []jsSlide
	if err := json.Unmarshal([]byte(raw), &slides); err != nil {
		return nil, err
	}
	if len(slides) == 0 {
		return nil, errors.New("no slides found in HTML")
	}
	return toDeck(slides, html), nil
}

func toDeck(slides []jsSlide, src []byte) *rawdeck.Deck {
	w, h := slides[0].Width, slides[0].Height
	if w <= 0 || h <= 0 {
		w, h = 1280, 720
	}
	d := &rawdeck.Deck{SourceType: "html", SourceHash: blob.Hash(src), WidthPt: 960, HeightPt: round(960 * h / w)}
	ptPerPx := 960 / w
	for _, js := range slides {
		s := &rawdeck.Slide{Index: js.Index, Background: cssColor(js.Background)}
		for _, e := range js.Elements {
			if e.Box.X > 1 || e.Box.Y > 1 || e.Box.Right() < 0 || e.Box.Bottom() < 0 {
				continue
			}
			el := &rawdeck.Element{Box: clip(e.Box)}
			switch e.Kind {
			case "text":
				el.Kind = rawdeck.KindText
				el.Text = e.Text
				el.Style = rawdeck.TextStyle{
					Font: firstFamily(e.Style.Font), SizePt: round(e.Style.Size * ptPerPx), Bold: e.Style.Weight >= 600,
					Italic: e.Style.Italic, Color: cssColor(e.Style.Color), Align: align(e.Style.Align),
					Bullet: e.Bullet, Paragraphs: e.Paragraphs,
				}
				if e.Tag == "h1" {
					el.Style.Bold = el.Style.Bold || e.Style.Weight >= 500
				}
			case "image":
				el.Kind, el.Role = rawdeck.KindImage, ""
				el.RadiusPx = e.Radius
				el.Shadow = e.Shadow
				if e.BorderWidth > 0 {
					el.Line = cssColor(e.BorderColor)
					el.LineWidthPt = round(e.BorderWidth * ptPerPx)
				}
				if ref, mime, data, ok := dataURI(e.Src); ok {
					d.AddAsset(ref, mime, data)
					el.ImageRef = ref
				}
			case "shape":
				el.Kind = rawdeck.KindShape
				el.Fill = cssColor(e.Fill)
				el.RadiusPx = e.Radius
				el.Shadow = e.Shadow
				el.Geometry = "rect"
				if e.Radius > 0 {
					el.Geometry = "roundRect"
				}
			case "table":
				el.Kind, el.Role = rawdeck.KindTable, rawdeck.RoleTable
				t := &rawdeck.Table{Cells: e.Cells, Rows: len(e.Cells), FirstRow: true,
					HeaderFill: cssColor(e.HeaderFill), HeaderText: cssColor(e.HeaderText), HeaderBold: e.HeaderBold,
					BodyFill: cssColor(e.BodyFill), BandFill: cssColor(e.BandFill), BodyText: cssColor(e.BodyText),
					FontSizePt: round(e.Size * ptPerPx), BorderColor: cssColor(e.Border)}
				if t.BandFill == t.BodyFill {
					t.BandFill = rawdeck.Color{}
				}
				for _, r := range e.Cells {
					t.Cols = max(t.Cols, len(r))
				}
				el.Table = t
			default:
				continue
			}
			s.Elements = append(s.Elements, el)
		}
		d.Slides = append(d.Slides, s)
	}
	return d
}

func round(v float64) float64 { return float64(int(v*10+0.5)) / 10 }

func clip(b rawdeck.Box) rawdeck.Box {
	x2, y2 := min(1, b.Right()), min(1, b.Bottom())
	b.X, b.Y = max(0, b.X), max(0, b.Y)
	b.W, b.H = x2-b.X, y2-b.Y
	return b
}

func firstFamily(f string) string {
	f = strings.Split(f, ",")[0]
	return strings.Trim(strings.TrimSpace(f), `"'`)
}

func align(a string) string {
	switch a {
	case "center":
		return "ctr"
	case "right", "end":
		return "r"
	case "justify":
		return "just"
	}
	return "l"
}

// cssColor converts "rgb(r, g, b)" / "rgba(r, g, b, a)" to a rawdeck color.
func cssColor(s string) rawdeck.Color {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "rgb") {
		return rawdeck.Color{}
	}
	in := s[strings.Index(s, "(")+1 : strings.LastIndex(s, ")")]
	parts := strings.FieldsFunc(in, func(r rune) bool { return r == ',' || r == ' ' || r == '/' })
	if len(parts) < 3 {
		return rawdeck.Color{}
	}
	if len(parts) == 4 {
		if a, _ := strconv.ParseFloat(parts[3], 64); a < 0.05 {
			return rawdeck.Color{}
		}
	}
	v := make([]int, 3)
	for i := range 3 {
		f, _ := strconv.ParseFloat(parts[i], 64)
		v[i] = int(f + 0.5)
	}
	return rawdeck.Color{Hex: fmt.Sprintf("#%02X%02X%02X", v[0], v[1], v[2])}
}

func dataURI(src string) (ref, mime string, data []byte, ok bool) {
	if !strings.HasPrefix(src, "data:") {
		return "", "", nil, false
	}
	comma := strings.Index(src, ",")
	if comma < 0 {
		return "", "", nil, false
	}
	meta := src[5:comma]
	mime = strings.Split(meta, ";")[0]
	payload := src[comma+1:]
	var err error
	if strings.HasSuffix(meta, ";base64") {
		data, err = base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return "", "", nil, false
		}
	} else {
		data = []byte(payload)
	}
	return blob.Hash(data), mime, data, true
}
