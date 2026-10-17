package gen

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/ashutoshm97/curly-engine/internal/blob"
	"github.com/ashutoshm97/curly-engine/internal/scene"
	"github.com/ashutoshm97/curly-engine/internal/store/storetest"
)

type fakeImages struct {
	calls  atomic.Int32
	aspect string
	err    error
}

func (f *fakeImages) GenerateImage(_ context.Context, model, prompt, aspect string) ([]byte, string, error) {
	f.calls.Add(1)
	f.aspect = aspect
	if f.err != nil {
		return nil, "", f.err
	}
	return []byte("png:" + prompt), "image/png", nil
}

func imageSlide() *scene.Slide {
	return &scene.Slide{Index: 1, Elements: []*scene.Element{
		{Kind: "image", W: 400, H: 300, Image: &scene.Image{Placeholder: "A lighthouse at dusk"}},
		{Kind: "image", W: 100, H: 100, Image: &scene.Image{Placeholder: "Image"}},
		{Kind: "text", Text: &scene.Text{}},
	}}
}

func TestImagesFillAndCache(t *testing.T) {
	bs, _ := blob.New(filepath.Join(t.TempDir(), "blobs"))
	f := &fakeImages{}
	im := &Images{Client: f, Model: "m", Blobs: bs, Store: storetest.Open(t)}
	ctx := context.Background()

	a := imageSlide()
	im.Fill(ctx, a)
	if a.Elements[0].Image.Ref == "" || a.Elements[0].Image.Mime != "image/png" {
		t.Fatalf("image not filled: %+v", a.Elements[0].Image)
	}
	if a.Elements[1].Image.Ref != "" {
		t.Fatal("generic placeholder should not be generated")
	}
	if f.aspect != "4:3" {
		t.Fatalf("aspect = %s", f.aspect)
	}
	if data, _ := bs.Get(a.Elements[0].Image.Ref); len(data) == 0 {
		t.Fatal("blob missing")
	}
	b := imageSlide()
	im.Fill(ctx, b)
	if f.calls.Load() != 1 || b.Elements[0].Image.Ref != a.Elements[0].Image.Ref {
		t.Fatalf("cache miss: calls=%d", f.calls.Load())
	}
}

func TestImagesFailureKeepsPlaceholder(t *testing.T) {
	bs, _ := blob.New(filepath.Join(t.TempDir(), "blobs"))
	im := &Images{Client: &fakeImages{err: errors.New("quota")}, Model: "m", Blobs: bs}
	sl := imageSlide()
	im.Fill(context.Background(), sl)
	if sl.Elements[0].Image.Ref != "" || sl.Elements[0].Image.Placeholder == "" {
		t.Fatalf("placeholder lost: %+v", sl.Elements[0].Image)
	}
	var nilImages *Images
	nilImages.Fill(context.Background(), imageSlide()) // no-op
}

func TestAspectFor(t *testing.T) {
	for _, c := range []struct {
		w, h float64
		want string
	}{{100, 100, "1:1"}, {1600, 900, "16:9"}, {300, 400, "3:4"}, {0, 10, "16:9"}} {
		if got := aspectFor(c.w, c.h); got != c.want {
			t.Errorf("aspectFor(%v,%v)=%s want %s", c.w, c.h, got, c.want)
		}
	}
}
