package imaging

import (
	"bytes"
	"encoding/binary"
	"math"
)

// A Cost is the memory, in bytes, that working on an image takes, worked
// out from its header before any of the work is done.
type Cost struct {
	Width, Height int
	// Base is what decoding the image and making its thumbnail take. A
	// server without this much to give cannot accept the image.
	Base int64
	// Display is what making its display rendition takes on top; an image
	// can be kept without one.
	Display int64
	// Scratch is the disk the display rendition's canvases take while it is
	// drawn, when canvases are kept there (see DiskScratch).
	Scratch int64
}

// Estimate works out an image's Cost. It is zero only for an image the
// decoder turns away at its header, which costs nothing. It is not held to
// MaxPixels: an image past that is costed like any other, and so is refused
// for the memory it would take.
//
// The server lets images in by this, so it must never come to less than
// the decoder goes on to allocate, whatever the file says: each format is
// read here the way its decoder reads it, and a header that cannot be
// followed is costed as the most its format could need.
func Estimate(contentType string, data []byte, thumbSide int) Cost {
	l, ok := describe(contentType, data)
	if !ok || l.w <= 0 || l.h <= 0 {
		return Cost{}
	}
	rotated := turned(Orientation(contentType, data))
	c := Cost{Width: l.w, Height: l.h, Base: l.decode}
	w, h := thumbnailSize(l.w, l.h, thumbSide)
	memory, _ := canvasSize(w, h, rotated) // a thumbnail's canvas is too small for disk
	c.Base += memory + scalerMemory(w, h, l.w, l.h)
	// The encoder's output is kept only while it is smaller than the file,
	// and is gathered in pieces and then put together.
	c.Display = int64(len(data)) * 3 / 2
	w, h = displaySize(l.w, l.h)
	if scaled := w != l.w || h != l.h; scaled || rotated || !l.direct {
		memory, c.Scratch = canvasSize(w, h, rotated)
		c.Display += memory + scalerMemory(w, h, l.w, l.h)
	}
	return c
}

// canvasSize is what a w×h rendition's canvas takes: memory, or disk when it
// is large enough to be kept there, and either twice over when the image has
// to be turned, since that is done from one canvas onto another.
func canvasSize(w, h int, rotated bool) (memory, disk int64) {
	one := int64(max(w, 1)) * int64(max(h, 1)) * 4
	all := one
	if rotated {
		all *= 2
	}
	if one >= scratchMin && DiskScratch() {
		return 0, all
	}
	return all, 0
}

// scalerMemory is what shrink works in to scale sw×sh to w×h: a band of
// source rows, the tables of where each column falls, and two rows of sums.
func scalerMemory(w, h, sw, sh int) int64 {
	if w == sw && h == sh {
		return 0
	}
	return int64(sw)*(shrinkRows*4+8) + int64(w)*48
}

// A layout is what an image's header says about decoding it.
type layout struct {
	w, h int
	// decode is how many bytes the decoder allocates in all: the image it
	// hands back and whatever it needs on the way.
	decode int64
	// direct is whether that image is of a kind flatten can pass to the JPEG
	// encoder without a canvas.
	direct bool
}

// area is w×h, held to where the sums made from it still fit: a header can
// claim far more pixels than any decoder would take on.
func area(w, h int) int64 {
	return min(int64(w)*int64(h), math.MaxInt64>>6)
}

// worstCase is the most a decoder can allocate for an image of a format, in
// bytes a pixel: a progressive CMYK JPEG, a 16-bit interlaced PNG.
var worstCase = map[string]int64{"image/jpeg": 24, "image/png": 16}

// describe reads an image's layout from its header.
func describe(contentType string, data []byte) (layout, bool) {
	var l layout
	var ok bool
	switch contentType {
	case "image/jpeg":
		l, ok = describeJPEG(data)
	case "image/png":
		l, ok = describePNG(data)
	case "image/webp":
		return describeWebP(data)
	case "image/gif":
		cfg, err := decodeConfig(contentType, bytes.NewReader(data))
		if err != nil {
			return layout{}, false
		}
		// The first frame, one palette index a pixel, and the same again
		// while an interlaced one is put in order.
		return layout{w: cfg.Width, h: cfg.Height, decode: 2 * area(cfg.Width, cfg.Height)}, true
	}
	if ok {
		return l, true
	}
	// A header the decoder may still follow where this did not.
	cfg, err := decodeConfig(contentType, bytes.NewReader(data))
	if err != nil {
		return layout{}, false
	}
	return layout{w: cfg.Width, h: cfg.Height, decode: area(cfg.Width, cfg.Height) * worstCase[contentType]}, true
}

// describeWebP reads a WebP's container as the decoder does. A file in the
// extended format is described from that alone: handed to the decoder, even
// only to ask its size, it has its alpha plane allocated first, at whatever
// size its header claims.
func describeWebP(data []byte) (layout, bool) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return layout{}, false
	}
	var l layout
	extended, wantAlpha, alpha := false, false, false
	for i := 12; i+8 <= len(data); {
		id, n := string(data[i:i+4]), int(binary.LittleEndian.Uint32(data[i+4:]))
		body := data[i+8:]
		switch id {
		case "VP8X":
			if extended || n != 10 || len(body) < 10 {
				return layout{}, false
			}
			extended, wantAlpha = true, body[0]&0x10 != 0
			l.w = 1 + (int(body[4]) | int(body[5])<<8 | int(body[6])<<16)
			l.h = 1 + (int(body[7]) | int(body[8])<<8 | int(body[9])<<16)
		case "ALPH":
			if !wantAlpha {
				return layout{}, false
			}
			wantAlpha, alpha = false, true
		case "VP8 ", "VP8L":
			lossy := id == "VP8 "
			if lossy && wantAlpha || !lossy && alpha {
				return layout{}, false
			}
			if !extended {
				cfg, err := decodeConfig("image/webp", bytes.NewReader(data))
				if err != nil {
					return layout{}, false
				}
				l.w, l.h = cfg.Width, cfg.Height
			}
			pixels := area(l.w, l.h)
			switch {
			case lossy && alpha:
				// Three planes, two of them at quarter size, a copy of the
				// compressed data, and an alpha plane that is itself decoded
				// from four bytes a pixel.
				l.decode = pixels*13/2 + int64(len(data))
			case lossy:
				l.decode, l.direct = pixels*3/2+int64(len(data)), true
			default:
				// Lossless: four bytes a pixel, and once more for a transform
				// that cannot be undone in place.
				l.decode, l.direct = pixels*8, true
			}
			return l, true
		}
		i += 8 + n + n&1
	}
	return layout{}, false
}

// describeJPEG reads a JPEG's frame header: its size, how its components
// are sampled, and whether it is progressive.
func describeJPEG(data []byte) (layout, bool) {
	var sof []byte
	var kind byte
	frames := 0
	jpegSegments(data, func(marker byte, seg []byte) bool {
		if marker == 0xC0 || marker == 0xC1 || marker == 0xC2 {
			sof, kind = seg, marker
			frames++
		}
		return true
	})
	// The decoder takes one, three or four components, and one frame.
	if frames != 1 || len(sof) < 6 || len(sof) != 6+3*int(sof[5]) {
		return layout{}, false
	}
	n := int(sof[5])
	l := layout{h: int(sof[1])<<8 | int(sof[2]), w: int(sof[3])<<8 | int(sof[4])}
	hmax, vmax := 1, 1
	for c := 0; c < n; c++ {
		hmax, vmax = max(hmax, int(sof[7+3*c]>>4)), max(vmax, int(sof[7+3*c]&15))
	}
	// Whole 8×8 blocks of the most finely sampled component.
	mw, mh := int64(8*hmax), int64(8*vmax)
	padded := (int64(l.w) + mw - 1) / mw * mw * ((int64(l.h) + mh - 1) / mh * mh)
	var samples int64
	for c := 0; c < n; c++ {
		h, v := int64(sof[7+3*c]>>4), int64(sof[7+3*c]&15)
		samples += padded * max(h, 1) * max(v, 1) / int64(hmax*vmax)
	}
	l.decode = samples
	if kind == 0xC2 {
		l.decode += 4 * samples // the coefficients, kept until the last scan
	}
	pixels := int64(l.w) * int64(l.h)
	switch {
	case n == 1:
		l.direct = true // grey
	case n == 3 && (sof[6] == 'R' && sof[9] == 'G' && sof[12] == 'B' || adobeRGB(data)):
		l.decode += 4 * pixels // stored as RGB: converted to an RGBA image
		l.direct = true
	case n == 3:
		l.direct = true // YCbCr
	default:
		l.decode += 4 * pixels // CMYK, assembled from its four planes
	}
	return l, true
}

// adobeRGB reports whether an Adobe segment anywhere in a JPEG says its
// components are not YCbCr. The decoder goes by the last such segment it
// meets, which may be after the image data; going by any is never less.
func adobeRGB(data []byte) bool {
	for i := 0; ; {
		j := bytes.Index(data[i:], []byte("Adobe"))
		if j < 0 {
			return false
		}
		j += i
		if j >= 4 && data[j-4] == 0xFF && data[j-3] == 0xEE && j+11 < len(data) && data[j+11] == 0 {
			return true
		}
		i = j + 1
	}
}

// describePNG reads the header and looks ahead for a transparency chunk,
// which turns grey and RGB images into four bytes a pixel.
func describePNG(data []byte) (layout, bool) {
	if len(data) < 8+8+13 || string(data[:8]) != "\x89PNG\r\n\x1a\n" || string(data[12:16]) != "IHDR" {
		return layout{}, false
	}
	ihdr := data[16:29]
	width, height := binary.BigEndian.Uint32(ihdr[0:4]), binary.BigEndian.Uint32(ihdr[4:8])
	if width > 1<<30 || height > 1<<30 {
		return layout{}, false
	}
	l := layout{w: int(width), h: int(height)}
	depth, kind, interlaced := ihdr[8], ihdr[9], ihdr[12] == 1
	transparency := false
	for i := 8; i+8 <= len(data); {
		n, name := int(binary.BigEndian.Uint32(data[i:])), string(data[i+4:i+8])
		if name == "IDAT" || name == "IEND" || n > len(data) {
			break
		}
		transparency = transparency || name == "tRNS"
		i += 12 + n
	}
	var perPixel int64
	switch {
	case kind == 3: // palette
		perPixel = 1
	case depth == 16 && kind == 0 && !transparency: // grey
		perPixel = 2
	case depth == 16:
		perPixel = 8
	case kind == 0 && !transparency: // grey
		perPixel, l.direct = 1, true
	default: // RGB, RGBA, grey with alpha, at eight bits
		perPixel, l.direct = 4, true
	}
	l.decode = area(l.w, l.h) * perPixel
	if interlaced {
		// Each of the seven passes is decoded as an image of its own and
		// merged in; between them they are the whole image over again.
		l.decode *= 2
	}
	return l, true
}
