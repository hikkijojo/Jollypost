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
	out, err := Apply(bytes.NewReader(buf.Bytes()), "10/8-1", "7")
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	found := false
	for y := 0; y < h/3; y++ {
		for x := w / 2; x < w; x++ {
			if isRed(img.At(x, y)) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("stamp color not found in the upper right")
	}
	if isRed(img.At(w/2, h/2)) {
		t.Fatal("stamp still covers the middle")
	}
	if !hasKeycapBlue(img) {
		t.Fatal("keycap emoji not found in the upper right")
	}
	plain, err := Apply(bytes.NewReader(buf.Bytes()), "10/8-1", "")
	if err != nil {
		t.Fatal(err)
	}
	plainImg, err := jpeg.Decode(bytes.NewReader(plain))
	if err != nil {
		t.Fatal(err)
	}
	if hasKeycapBlue(plainImg) {
		t.Fatal("empty account still drew a keycap")
	}
}

func hasKeycapBlue(img image.Image) bool {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	for y := 0; y < h/3; y++ {
		for x := w / 2; x < w; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if b>>8 > 150 && r>>8 < 80 && g>>8 > 90 && g>>8 < 190 {
				return true
			}
		}
	}
	return false
}

func isRed(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return r>>8 > 150 && g>>8 < 120 && b>>8 < 120
}
