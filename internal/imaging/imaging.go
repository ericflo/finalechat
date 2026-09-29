// Package imaging decodes uploaded images and renders thumbnails.
package imaging

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"

	"golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

// Info describes a decoded image.
type Info struct {
	Width, Height int
}

// ErrUnsupported is returned for content types this package cannot decode.
var ErrUnsupported = errors.New("unsupported image type")

// ImageTypes are the content types that get thumbnails and dimensions.
var ImageTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// MaxPixels bounds decode work so a tiny file cannot declare a huge canvas.
// 300 MP admits everything realistic (50 MP phone sensors, 8K stills at
// ~33 MP, large scans and panoramas) while still rejecting absurd
// gigapixel headers that would exhaust memory. Concurrent decodes are
// additionally capped (see the decoding semaphore in internal/api).
const MaxPixels = 300_000_000

// checkDimensions rejects non-positive sizes and pixel counts above
// MaxPixels. The width×height product uses int64 so extreme header values
// (e.g. 1<<30 × 1<<30) cannot overflow into a small positive int and slip
// past the limit.
func checkDimensions(width, height int) error {
	if width <= 0 || height <= 0 {
		return fmt.Errorf("image dimensions are out of range: image is %dx%d; limit is %d MP", width, height, MaxPixels/1_000_000)
	}
	pixels := int64(width) * int64(height)
	if pixels > int64(MaxPixels) {
		mp := float64(pixels) / 1_000_000
		return fmt.Errorf("image dimensions are out of range: image is %dx%d (%.1f MP); limit is %d MP", width, height, mp, MaxPixels/1_000_000)
	}
	return nil
}

// Decode reads the image using the declared content type.
func Decode(contentType string, data []byte) (image.Image, Info, error) {
	cfg, err := decodeConfig(contentType, bytes.NewReader(data))
	if err != nil {
		return nil, Info{}, err
	}
	if err := checkDimensions(cfg.Width, cfg.Height); err != nil {
		return nil, Info{}, err
	}
	img, err := decode(contentType, bytes.NewReader(data))
	if err != nil {
		return nil, Info{}, err
	}
	b := img.Bounds()
	return img, Info{Width: b.Dx(), Height: b.Dy()}, nil
}

func decodeConfig(contentType string, r io.Reader) (image.Config, error) {
	switch contentType {
	case "image/png":
		return png.DecodeConfig(r)
	case "image/jpeg":
		return jpeg.DecodeConfig(r)
	case "image/gif":
		return gif.DecodeConfig(r)
	case "image/webp":
		return webp.DecodeConfig(r)
	}
	return image.Config{}, ErrUnsupported
}

func decode(contentType string, r io.Reader) (image.Image, error) {
	switch contentType {
	case "image/png":
		return png.Decode(r)
	case "image/jpeg":
		return jpeg.Decode(r)
	case "image/gif":
		return gif.Decode(r)
	case "image/webp":
		return webp.Decode(r)
	}
	return nil, ErrUnsupported
}

// Thumbnail scales the image so its longest side is at most maxSide and
// encodes it as JPEG. Images already within bounds are re-encoded so the
// thumbnail is always a small, fast JPEG.
func Thumbnail(img image.Image, maxSide int) ([]byte, Info, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > maxSide || h > maxSide {
		if w >= h {
			h = int(float64(h) * float64(maxSide) / float64(w))
			w = maxSide
		} else {
			w = int(float64(w) * float64(maxSide) / float64(h))
			h = maxSide
		}
		if w < 1 {
			w = 1
		}
		if h < 1 {
			h = 1
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	// Fill with white so transparent screenshots do not turn black in JPEG.
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 82}); err != nil {
		return nil, Info{}, err
	}
	return buf.Bytes(), Info{Width: w, Height: h}, nil
}
