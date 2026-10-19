package pdfx

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"

	"github.com/ledongthuc/pdf"

	"github.com/ashutoshm97/curly-engine/internal/blob"
)

// decodeImage re-encodes 8-bit Flate images (with optional soft mask) as PNG.
// Other encodings (DCT/JPX/CCITT) keep their placement but carry no asset.
func decodeImage(xo pdf.Value, maxPixels int) (ref string, data []byte, ok bool) {
	defer func() {
		if recover() != nil {
			ref, data, ok = "", nil, false
		}
	}()
	if filterName(xo) != "FlateDecode" || xo.Key("BitsPerComponent").Int64() != 8 {
		return "", nil, false
	}
	w, h := int(xo.Key("Width").Int64()), int(xo.Key("Height").Int64())
	nc := components(xo.Key("ColorSpace"))
	if w <= 0 || h <= 0 || w*h > maxPixels || nc == 0 {
		return "", nil, false
	}
	raw, err := io.ReadAll(io.LimitReader(xo.Reader(), int64(w*h*nc)))
	if err != nil || len(raw) < w*h*nc {
		return "", nil, false
	}
	alpha := softMask(xo.Key("SMask"), w, h)
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		var c color.NRGBA
		p := raw[i*nc:]
		switch nc {
		case 1:
			c = color.NRGBA{p[0], p[0], p[0], 255}
		case 3:
			c = color.NRGBA{p[0], p[1], p[2], 255}
		case 4:
			k := 255 - int(p[3])
			c = color.NRGBA{uint8((255 - int(p[0])) * k / 255), uint8((255 - int(p[1])) * k / 255), uint8((255 - int(p[2])) * k / 255), 255}
		}
		if alpha != nil {
			c.A = alpha[i]
		}
		img.SetNRGBA(i%w, i/w, c)
	}
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img); err != nil {
		return "", nil, false
	}
	return blob.Hash(buf.Bytes()), buf.Bytes(), true
}

func softMask(sm pdf.Value, w, h int) (a []byte) {
	defer func() {
		if recover() != nil {
			a = nil
		}
	}()
	if sm.IsNull() || filterName(sm) != "FlateDecode" || int(sm.Key("Width").Int64()) != w || int(sm.Key("Height").Int64()) != h || sm.Key("BitsPerComponent").Int64() != 8 {
		return nil
	}
	a, err := io.ReadAll(io.LimitReader(sm.Reader(), int64(w*h)))
	if err != nil || len(a) < w*h {
		return nil
	}
	return a
}

func filterName(v pdf.Value) string {
	f := v.Key("Filter")
	if f.Kind() == pdf.Array && f.Len() == 1 {
		return f.Index(0).Name()
	}
	return f.Name()
}

func components(cs pdf.Value) int {
	name := cs.Name()
	if cs.Kind() == pdf.Array {
		name = cs.Index(0).Name()
		if name == "ICCBased" {
			return int(cs.Index(1).Key("N").Int64())
		}
	}
	switch name {
	case "DeviceGray", "CalGray", "G":
		return 1
	case "DeviceRGB", "CalRGB", "RGB":
		return 3
	case "DeviceCMYK", "CMYK":
		return 4
	}
	return 0
}
