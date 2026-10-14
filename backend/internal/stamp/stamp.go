package stamp

import (
	"bytes"
	"embed"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"

	"github.com/disintegration/imageorient"
	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/webp"
)

//go:embed emoji/*.png
var emojiFS embed.FS

const maxEdge = 1600

func Apply(r io.Reader, text, account string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, 20<<20))
	if err != nil {
		return nil, err
	}
	src, err := decode(data)
	if err != nil {
		return nil, err
	}
	dst := fit(src, maxEdge)
	if err := drawLabel(dst, text, account); err != nil {
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

func drawLabel(dst *image.RGBA, text, account string) error {
	ft, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return err
	}
	bounds := dst.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	size := float64(width) / 11
	if size > 118 {
		size = 118
	}
	if size < 40 {
		size = 40
	}
	padRight := width / 18
	if padRight < 12 {
		padRight = 12
	}
	mark, err := keycap(account)
	if err != nil {
		return err
	}
	var face font.Face
	var textWidth, box, gap, ascent, descent int
	for {
		if face != nil {
			closeFace(face)
		}
		face, err = opentype.NewFace(ft, &opentype.FaceOptions{
			Size:    size,
			DPI:     72,
			Hinting: font.HintingFull,
		})
		if err != nil {
			return err
		}
		drawer := &font.Drawer{Face: face}
		textWidth = drawer.MeasureString(text).Round()
		metrics := face.Metrics()
		ascent = metrics.Ascent.Round()
		descent = metrics.Descent.Round()
		box = 0
		gap = 0
		if mark != nil {
			box = int(size * 0.95)
			gap = int(size * 0.12)
			if gap < 6 {
				gap = 6
			}
		}
		if textWidth+gap+box <= width-padRight*2 || size <= 42 {
			break
		}
		size -= 4
	}
	defer closeFace(face)

	padTop := height / 14
	if padTop < 12 {
		padTop = 12
	}
	x := width - padRight - textWidth - gap - box
	if x < 8 {
		x = 8
	}
	y := padTop + ascent
	if limit := height - descent - 8; y > limit {
		y = limit
	}
	drawer := &font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(color.RGBA{R: 220, G: 38, B: 38, A: 255}),
		Face: face,
	}
	drawer.Dot = fixed.P(x, y)
	drawer.DrawString(text)
	if mark != nil && box > 0 {
		top := y - ascent + (ascent-box)/2
		if top < 0 {
			top = 0
		}
		left := x + textWidth + gap
		draw.CatmullRom.Scale(dst, image.Rect(left, top, left+box, top+box), mark, mark.Bounds(), draw.Over, nil)
	}
	return nil
}

func keycap(account string) (image.Image, error) {
	if account == "" {
		return nil, nil
	}
	if len(account) != 1 || account[0] < '1' || account[0] > '9' {
		return nil, fmt.Errorf("bad account")
	}
	file, err := emojiFS.Open("emoji/" + account + ".png")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return png.Decode(file)
}

func closeFace(face font.Face) {
	if c, ok := face.(interface{ Close() error }); ok {
		_ = c.Close()
	}
}
