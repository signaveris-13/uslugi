package media

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func TestSaveDownscalesAndOpens(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 3200, 1000))
	for x := 0; x < 3200; x++ {
		img.Set(x, 500, color.RGBA{R: 200, A: 255})
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)

	key, err := s.Save(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidKey(key) {
		t.Fatalf("ключ %q", key)
	}
	f, err := s.Open(key)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil || format != "jpeg" || cfg.Width != 1600 || cfg.Height != 500 {
		t.Fatalf("после сжатия: %s %dx%d %v", format, cfg.Width, cfg.Height, err)
	}

	if _, err := s.Save(strings.NewReader("not an image")); err != ErrUnsupported {
		t.Fatalf("не картинка: %v", err)
	}
	if _, err := s.Open("../../etc/passwd"); err == nil {
		t.Fatal("путь вне папки должен не открываться")
	}
}
