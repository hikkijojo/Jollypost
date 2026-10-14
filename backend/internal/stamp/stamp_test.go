package stamp

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func TestApplyWritesRed(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 800, 1200))
	for y := 0; y < 1200; y++ {
		for x := 0; x < 800; x++ {
			src.SetRGBA(x, y, color.RGBA{R: 20, G: 20, B: 20, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	out, err := Apply(bytes.NewReader(buf.Bytes()), "10/14-1")
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	cx, cy := img.Bounds().Dx()/2, img.Bounds().Dy()/2
	found := false
	for y := cy - 50; y <= cy+50; y++ {
		for x := cx - 120; x <= cx+120; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r>>8 > 150 && g>>8 < 120 && b>>8 < 120 {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("stamp color not found near center")
	}
}
