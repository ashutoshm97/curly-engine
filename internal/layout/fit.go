package layout

import (
	"strings"

	"github.com/ashutoshm97/curly-engine/internal/profile"
	"github.com/ashutoshm97/curly-engine/internal/scene"
)

// PowerPoint's default text insets.
const (
	insetX = 7.2
	insetY = 3.6
)

// Fit lays out paragraphs in a w×h box, stepping down the type scale from
// start to minStep, and truncating with an ellipsis as a last resort.
func Fit(t *scene.Text, w, h float64, steps []profile.Step) {
	t.InsetX, t.InsetY = insetX, insetY
	for i, st := range steps {
		lines, used := place(t, st.Size, st.LineHeight, w-2*insetX)
		if used <= h-2*insetY || i == len(steps)-1 {
			t.SizePt, t.LineHeight, t.Lines = st.Size, st.LineHeight, lines
			if used > h-2*insetY {
				truncate(t, h-2*insetY)
			}
			return
		}
	}
}

func place(t *scene.Text, size, lh, w float64) ([]scene.Line, float64) {
	var out []scene.Line
	adv := size * lh
	y := 0.0
	indent := 0.0
	if t.Bullet != "" {
		indent = size * 1.1
		if t.IndentPt > 0 {
			indent = t.IndentPt
		}
	}
	for pi, p := range t.Paragraphs {
		if pi > 0 && t.Bullet != "" {
			if t.SpaceBefore > 0 {
				y += t.SpaceBefore
			} else {
				y += adv * 0.35
			}
		}
		for li, l := range Wrap(p, size, w-indent, t.Bold) {
			out = append(out, scene.Line{Text: l, X: insetX + indent, Baseline: insetY + y + size*0.9 + (lh-1)*size/2, Bullet: t.Bullet != "" && li == 0})
			y += adv
		}
	}
	return out, y
}

func truncate(t *scene.Text, h float64) {
	n := 0
	for n < len(t.Lines) && t.Lines[n].Baseline+t.SizePt*0.25 <= h+insetY {
		n++
	}
	if n == 0 {
		n = 1
	}
	if n < len(t.Lines) {
		t.Lines = t.Lines[:n]
		last := &t.Lines[n-1]
		last.Text = strings.TrimRight(last.Text, " .,;:") + "…"
		t.Truncated = true
		// Keep paragraphs consistent with what is visible (PPTX writes paragraphs).
		var paras []string
		var cur []string
		for _, l := range t.Lines {
			if l.Bullet && len(cur) > 0 {
				paras = append(paras, strings.Join(cur, " "))
				cur = nil
			}
			cur = append(cur, l.Text)
		}
		if t.Bullet == "" {
			paras = []string{strings.Join(cur, " ")}
		} else {
			paras = append(paras, strings.Join(cur, " "))
		}
		t.Paragraphs = paras
	}
}
