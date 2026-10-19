package gen

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"math"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/ashutoshm97/curly-engine/internal/blob"
	"github.com/ashutoshm97/curly-engine/internal/scene"
	"github.com/ashutoshm97/curly-engine/internal/store"
)

// ImageModel generates one image for a prompt (llm.Gemini implements it).
type ImageModel interface {
	GenerateImage(ctx context.Context, model, prompt, aspect string) ([]byte, string, error)
}

// Images fills image slots from their imagePrompt with a Gemini image model.
// Results are content-addressed blobs; the prompt→blob mapping is cached so a
// prompt is generated once. Failures are logged and leave the placeholder.
type Images struct {
	Client ImageModel
	Model  string
	Blobs  *blob.Store
	Store  *store.Store // cache; optional
	TTL    time.Duration
}

var aspects = []struct {
	name string
	r    float64
}{{"1:1", 1}, {"4:3", 4.0 / 3}, {"3:4", 3.0 / 4}, {"16:9", 16.0 / 9}, {"9:16", 9.0 / 16}, {"3:2", 1.5}, {"2:3", 2.0 / 3}, {"21:9", 21.0 / 9}}

// aspectFor returns the supported aspect ratio closest to w:h.
func aspectFor(w, h float64) string {
	if w <= 0 || h <= 0 {
		return "16:9"
	}
	best, bd := "16:9", math.Inf(1)
	for _, a := range aspects {
		if d := math.Abs(math.Log(w / h / a.r)); d < bd {
			best, bd = a.name, d
		}
	}
	return best
}

func imagePrompt(desc string) string {
	return "Presentation slide image: " + desc + ". Clean, professional, high quality. No text, words, logos or watermarks."
}

// Fill generates images for the slide's unfilled image elements in place.
func (im *Images) Fill(ctx context.Context, sl *scene.Slide) {
	if im == nil || sl == nil {
		return
	}
	g, ctx := errgroup.WithContext(ctx)
	for _, e := range sl.Elements {
		if e.Kind != "image" || e.Image == nil || e.Image.Ref != "" {
			continue
		}
		desc := strings.TrimSpace(e.Image.Placeholder)
		if desc == "" || desc == "Image" {
			continue
		}
		aspect := aspectFor(e.W, e.H)
		g.Go(func() error {
			ref, mime, err := im.get(ctx, desc, aspect)
			if err != nil {
				slog.Warn("image", "slide", sl.Index, "err", err)
				return nil
			}
			e.Image.Ref, e.Image.Mime = ref, mime
			return nil
		})
	}
	g.Wait()
}

func (im *Images) get(ctx context.Context, desc, aspect string) (string, string, error) {
	h := sha256.Sum256([]byte(im.Model + "\x00" + aspect + "\x00" + desc))
	key := "img:" + hex.EncodeToString(h[:])
	if im.Store != nil {
		if v, ok := im.Store.CacheGet(ctx, key); ok {
			if ref, mime, ok := strings.Cut(string(v), " "); ok && blob.ValidRef(ref) {
				if _, err := im.Blobs.Get(ref); err == nil {
					return ref, mime, nil
				}
			}
		}
	}
	data, mime, err := im.Client.GenerateImage(ctx, im.Model, imagePrompt(desc), aspect)
	if err != nil {
		return "", "", err
	}
	ref, err := im.Blobs.Put(data)
	if err != nil {
		return "", "", err
	}
	if im.Store != nil {
		ttl := im.TTL
		if ttl == 0 {
			ttl = 30 * 24 * time.Hour
		}
		im.Store.CachePut(ctx, key, []byte(ref+" "+mime), ttl)
	}
	return ref, mime, nil
}
