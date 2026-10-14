package stamp

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/gif"
	_ "image/png"
	"io"

	"github.com/disintegration/imageorient"
	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/webp"
)

const maxEdge = 1600

func Apply(r io.Reader, text string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, 20<<20))
	if err != nil {
		return nil, err
	}
	src, err := decode(data)
	if err != nil {
		return nil, err
	}
	dst := fit(src, maxEdge)
	if err := drawLabel(dst, text); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decode(data []byte) (image.Image, error) {
	img, _, err := imageorient.Decode(bytes.NewReader(data))
	if err == nil {
		return img, nil
	}
	return webp.Decode(bytes.NewReader(data))
}

func fit(src image.Image, edge int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	nw, nh := w, h
	if w > edge || h > edge {
		if w >= h {
			nw = edge
			nh = h * edge / w
		} else {
			nh = edge
			nw = w * edge / h
		}
		if nw < 1 {
			nw = 1
		}
		if nh < 1 {
			nh = 1
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}

func drawLabel(dst *image.RGBA, text string) error {
	ft, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return err
	}
	size := float64(dst.Bounds().Dx()) / 8
	if size < 42 {
		size = 42
	}
	if size > 150 {
		size = 150
	}
	face, err := opentype.NewFace(ft, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return err
	}
	if c, ok := face.(interface{ Close() error }); ok {
		defer c.Close()
	}
	d := &font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(color.RGBA{R: 220, G: 38, B: 38, A: 255}),
		Face: face,
	}
	width := d.MeasureString(text).Round()
	metrics := face.Metrics()
	ascent := metrics.Ascent.Round()
	textHeight := ascent + metrics.Descent.Round()
	x := (dst.Bounds().Dx() - width) / 2
	y := (dst.Bounds().Dy()-textHeight)/2 + ascent
	d.Dot = fixed.P(x, y)
	d.DrawString(text)
	return nil
}
