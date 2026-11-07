// Package media хранит фото, загруженные в Mini App: уменьшает до разумного
// размера, пережимает в JPEG и кладёт на диск (том media на сервере).
package media

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png" // PNG тоже принимаем
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

const (
	MaxUploadBytes = 15 << 20 // 15 МБ на фото до сжатия
	MaxSide        = 1600     // длинная сторона после уменьшения
	jpegQuality    = 82
)

var ErrUnsupported = errors.New("формат фото не поддерживается — нужен JPEG или PNG")

type Store struct {
	dir string
}

func New(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("не задана папка для фото")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// Save читает картинку, уменьшает и сохраняет. Возвращает ключ вида 2026/11/<id>.jpg.
func (s *Store) Save(r io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxUploadBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > MaxUploadBytes {
		return "", fmt.Errorf("фото больше %d МБ", MaxUploadBytes>>20)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", ErrUnsupported
	}
	img = downscale(img, MaxSide)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return "", err
	}
	rnd := make([]byte, 12)
	if _, err := rand.Read(rnd); err != nil {
		return "", err
	}
	key := time.Now().UTC().Format("2006/01") + "/" + hex.EncodeToString(rnd) + ".jpg"
	path := filepath.Join(s.dir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return "", err
	}
	return key, os.Rename(tmp, path)
}

var keyRe = regexp.MustCompile(`^\d{4}/\d{2}/[0-9a-f]{24}\.jpg$`)

// ValidKey — ключ похож на наш, без попыток выйти из папки.
func ValidKey(key string) bool { return keyRe.MatchString(key) }

// Open открывает файл по ключу.
func (s *Store) Open(key string) (*os.File, error) {
	if !ValidKey(key) {
		return nil, os.ErrNotExist
	}
	return os.Open(filepath.Join(s.dir, filepath.FromSlash(key)))
}

// downscale уменьшает картинку усреднением по площади, если она больше max по длинной стороне.
func downscale(src image.Image, max int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= max && h <= max {
		return src
	}
	nw, nh := max, h*max/w
	if h > w {
		nw, nh = w*max/h, max
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy0 := b.Min.Y + y*h/nh
		sy1 := b.Min.Y + (y+1)*h/nh
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for x := 0; x < nw; x++ {
			sx0 := b.Min.X + x*w/nw
			sx1 := b.Min.X + (x+1)*w/nw
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			var r, g, bl, n uint64
			// Берём не больше 4×4 точек из блока: быстро и достаточно гладко для фото.
			stepX := (sx1-sx0)/4 + 1
			stepY := (sy1-sy0)/4 + 1
			for sy := sy0; sy < sy1; sy += stepY {
				for sx := sx0; sx < sx1; sx += stepX {
					cr, cg, cb, _ := src.At(sx, sy).RGBA()
					r, g, bl, n = r+uint64(cr), g+uint64(cg), bl+uint64(cb), n+1
				}
			}
			dst.SetRGBA(x, y, color.RGBA{R: uint8(r / n >> 8), G: uint8(g / n >> 8), B: uint8(bl / n >> 8), A: 255})
		}
	}
	return dst
}
