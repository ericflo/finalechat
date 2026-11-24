// Package imaging decodes uploaded images and renders their thumbnails and
// display renditions.
package imaging

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math"

	"golang.org/x/image/webp"
)

// Info describes a decoded image.
type Info struct {
	Width, Height int
}

// Upright is the size of the image once its EXIF orientation is applied:
// orientations 5-8 lie on their side, so width and height trade places.
func (i Info) Upright(orientation int) Info {
	if orientation >= 5 && orientation <= 8 {
		return Info{Width: i.Height, Height: i.Width}
	}
	return i
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
// gigapixel headers outright. Whether the server has the memory for a
// particular image is a separate question, answered by Estimate.
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

// DisplayPixels is the size a display rendition is brought down to: a 4K
// frame's worth (3840×2160). Screenshots up to that size keep every pixel,
// so text stays sharp when zoomed; a phone photo or a 5K capture is scaled
// to it.
const DisplayPixels = 3840 * 2160

// displayShort is the short side a display rendition is never taken below.
// A long screenshot is many megapixels and only a phone's width across:
// scaled by its pixel count alone it would come out too narrow to read.
const displayShort = 2160

// jpegMaxSide is the longest side a JPEG can have.
const jpegMaxSide = 65535

// JPEG qualities. The display rendition is what the app shows full screen,
// so it is compressed gently; a thumbnail is seen small.
const (
	thumbQuality   = 82
	displayQuality = 85
)

// Thumbnail scales the image so its longest side is at most maxSide and
// encodes it as an upright JPEG. Of an image too long or too wide to make
// out at that size it shows the start instead (see thumbnailRegion).
func Thumbnail(img image.Image, maxSide, orientation int) ([]byte, Info, error) {
	b := img.Bounds()
	if sw, sh := thumbnailRegion(b.Dx(), b.Dy(), orientation); sw != b.Dx() || sh != b.Dy() {
		if whole, ok := img.(interface {
			SubImage(image.Rectangle) image.Image
		}); ok {
			// The start of the upright image is against the left or right
			// and the top or bottom of the stored one, by how it is turned.
			x, y := b.Min.X, b.Min.Y
			switch orientation {
			case 2, 3, 7, 8:
				x = b.Max.X - sw
			}
			switch orientation {
			case 3, 4, 6, 7:
				y = b.Max.Y - sh
			}
			img = whole.SubImage(image.Rect(x, y, x+sw, y+sh))
			b = img.Bounds()
		}
	}
	w, h := thumbnailSize(b.Dx(), b.Dy(), maxSide)
	var out bytes.Buffer
	info, err := render(&out, img, w, h, orientation, thumbQuality, false)
	if err != nil {
		return nil, Info{}, err
	}
	return out.Bytes(), info, nil
}

// thumbnailRegion is the size of the part of a stored w×h image that its
// thumbnail shows. That is all of it, unless the image the right way up is
// more than two and a half times as long as it is wide, or three times as
// wide as it is tall: a long screenshot, a panorama. Squeezed whole into a
// thumbnail those are a sliver, so the thumbnail is of the top of the one
// and the left of the other, as far as those proportions go.
func thumbnailRegion(w, h, orientation int) (int, int) {
	if orientation >= 5 && orientation <= 8 {
		h, w = thumbnailRegion(h, w, 1)
		return w, h
	}
	return max(min(w, h*3), 1), max(min(h, w*5/2), 1)
}

// thumbnailSize fits w×h within a square of maxSide, never enlarging.
func thumbnailSize(w, h, maxSide int) (int, int) {
	if w <= maxSide && h <= maxSide {
		return w, h
	}
	if w >= h {
		return maxSide, int(float64(h) * float64(maxSide) / float64(w))
	}
	return int(float64(w) * float64(maxSide) / float64(h)), maxSide
}

// Display renders the copy of an image the app shows full screen: a JPEG at
// the image's own resolution, or scaled to displaySize, upright per the EXIF
// orientation. It is worth having only while it is smaller than the
// original, so it gives up with an error once it passes maxBytes.
//
// Display may fill the image's transparent pixels in place to save copying
// it: img is not to be used afterwards.
func Display(img image.Image, orientation, maxBytes int) ([]byte, Info, error) {
	b := img.Bounds()
	w, h := displaySize(b.Dx(), b.Dy())
	if max(w, h) > jpegMaxSide {
		return nil, Info{}, fmt.Errorf("a side of %d pixels is longer than a JPEG can be", max(w, h))
	}
	out := &capped{limit: maxBytes}
	info, err := render(out, img, w, h, orientation, displayQuality, true)
	if err != nil {
		return nil, Info{}, err
	}
	return out.bytes(), info, nil
}

// displaySize is the size of an image's display rendition: its own while it
// is within DisplayPixels or no more than displayShort on its short side,
// and otherwise scaled down to whichever of the two it reaches first.
func displaySize(w, h int) (int, int) {
	pixels, short := int64(w)*int64(h), min(w, h)
	if pixels <= DisplayPixels || short <= displayShort {
		return w, h
	}
	f := max(math.Sqrt(float64(DisplayPixels)/float64(pixels)), float64(displayShort)/float64(short))
	return int(float64(w) * f), int(float64(h) * f)
}

// capped collects what is written to it and fails once that passes its
// limit. It keeps what it is given in pieces, so that a long output is not
// copied over and over as it grows.
type capped struct {
	pieces [][]byte
	size   int
	limit  int
}

const cappedPiece = 256 << 10

var errNoSmaller = errors.New("the rendition is no smaller than the image")

func (c *capped) Write(p []byte) (int, error) {
	if c.size+len(p) > c.limit {
		return 0, errNoSmaller
	}
	for rest := p; len(rest) > 0; {
		if n := len(c.pieces); n == 0 || len(c.pieces[n-1]) == cap(c.pieces[n-1]) {
			c.pieces = append(c.pieces, make([]byte, 0, cappedPiece))
		}
		last := &c.pieces[len(c.pieces)-1]
		n := min(len(rest), cap(*last)-len(*last))
		*last = append(*last, rest[:n]...)
		rest = rest[n:]
	}
	c.size += len(p)
	return len(p), nil
}

// bytes is everything written, in one piece.
func (c *capped) bytes() []byte {
	out := make([]byte, 0, c.size)
	for _, p := range c.pieces {
		out = append(out, p...)
	}
	return out
}

// render writes img to out as a w×h JPEG, upright, on white where it is
// transparent. With consume set it may alter img instead of copying it.
func render(out io.Writer, img image.Image, w, h, orientation, quality int, consume bool) (Info, error) {
	b := img.Bounds()
	w, h = max(w, 1), max(h, 1)
	opts := &jpeg.Options{Quality: quality}
	if consume && !turned(orientation) && w == b.Dx() && h == b.Dy() {
		// Nothing to scale or turn: the encoder reads the decoded image as it
		// is, and no canvas is needed at all.
		if flat := flatten(img); flat != nil {
			return Info{Width: w, Height: h}, jpeg.Encode(out, flat, opts)
		}
	}
	c, err := newCanvas(w, h)
	if err != nil {
		return Info{}, err
	}
	defer func() { c.free() }()
	if w == b.Dx() && h == b.Dy() {
		draw.Draw(c.RGBA, c.Rect, image.White, image.Point{}, draw.Src)
		draw.Draw(c.RGBA, c.Rect, img, b.Min, draw.Over)
	} else {
		shrink(c.RGBA, img)
	}
	if turned(orientation) {
		upright, err := orient(c, orientation)
		if err != nil {
			return Info{}, err
		}
		c.free()
		c = upright
	}
	if err := jpeg.Encode(out, c.RGBA, opts); err != nil {
		return Info{}, err
	}
	return Info{Width: c.Rect.Dx(), Height: c.Rect.Dy()}, nil
}

// turned reports whether an EXIF orientation asks for the pixels to be
// moved at all.
func turned(orientation int) bool { return orientation >= 2 && orientation <= 8 }

// flatten returns img in a form the JPEG encoder reads directly, with any
// transparency filled in white, without copying it; nil for a kind of image
// that has to be drawn onto a canvas first. The filling is done in place.
func flatten(img image.Image) image.Image {
	switch m := img.(type) {
	case *image.YCbCr:
		return m
	case *image.Gray:
		return m
	case *image.RGBA:
		// Colour is stored already multiplied by alpha: over white it gains
		// what the alpha leaves uncovered.
		eachRow(m.Pix, m.Stride, m.Rect, func(row []uint8) {
			for i := 0; i < len(row); i += 4 {
				if a := row[i+3]; a != 0xff {
					rest := int(0xff - a)
					row[i] = uint8(min(int(row[i])+rest, 0xff))
					row[i+1] = uint8(min(int(row[i+1])+rest, 0xff))
					row[i+2] = uint8(min(int(row[i+2])+rest, 0xff))
					row[i+3] = 0xff
				}
			}
		})
		return m
	case *image.NRGBA:
		eachRow(m.Pix, m.Stride, m.Rect, func(row []uint8) {
			for i := 0; i < len(row); i += 4 {
				if a := uint32(row[i+3]); a != 0xff {
					white := 0xff*(0xff-a) + 0x7f
					row[i] = uint8((uint32(row[i])*a + white) / 0xff)
					row[i+1] = uint8((uint32(row[i+1])*a + white) / 0xff)
					row[i+2] = uint8((uint32(row[i+2])*a + white) / 0xff)
					row[i+3] = 0xff
				}
			}
		})
		// Fully opaque, the two layouts are the same bytes.
		return &image.RGBA{Pix: m.Pix, Stride: m.Stride, Rect: m.Rect}
	}
	return nil
}

// eachRow calls fn with each row of a four-bytes-a-pixel image.
func eachRow(pix []uint8, stride int, r image.Rectangle, fn func(row []uint8)) {
	for y := 0; y < r.Dy(); y++ {
		fn(pix[y*stride : y*stride+r.Dx()*4])
	}
}

// shrinkRows is how many source rows shrink converts at a time.
const shrinkRows = 32

// shrink scales src down to fill dst, on white where src is transparent, by
// averaging the source pixels that lie under each destination pixel. It
// reads the source a few rows at a time, so it needs the same small amount
// of memory however large the image is.
//
// The kernel scalers in x/image/draw (CatmullRom and the rest) are not used:
// while they work they hold every pixel of (destination width × source
// height) as four float64s. For the thumbnail of a 1080p screenshot that is
// 22 MB; for a 12-megapixel photo brought down to the display budget it is
// over 300 MB, more than the server has. Side by side, thumbnails made the
// two ways are not told apart.
//
// The sums are exact: lengths are measured in units where a source pixel is
// dw wide and a destination pixel sw wide, so every overlap is a whole
// number, and each destination pixel is covered by sw×sh of them.
func shrink(dst *image.RGBA, src image.Image) {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	dw, dh := dst.Bounds().Dx(), dst.Bounds().Dy()

	// Each source column lies under one destination column or straddles two:
	// first[x] is the one it starts in, and part[x] how much of it is there.
	first := make([]int32, sw)
	part := make([]uint32, sw)
	for x := range first {
		left := x * dw
		col := left / sw
		first[x] = int32(col)
		part[x] = uint32(min(left+dw, (col+1)*sw) - left)
	}

	band := image.NewRGBA(image.Rect(0, 0, sw, min(shrinkRows, sh)))
	row := make([]uint64, dw*3) // one source row, summed across
	sum := make([]uint64, dw*3) // the destination row being filled
	area := uint64(sw) * uint64(sh)
	for y := 0; y < sh; y++ {
		at := y % shrinkRows
		if at == 0 {
			r := image.Rect(0, 0, sw, min(shrinkRows, sh-y))
			draw.Draw(band, r, image.White, image.Point{}, draw.Src)
			draw.Draw(band, r, src, image.Pt(sb.Min.X, sb.Min.Y+y), draw.Over)
		}
		clear(row)
		pix := band.Pix[at*band.Stride:]
		for x := 0; x < sw; x++ {
			r, g, b := uint64(pix[x*4]), uint64(pix[x*4+1]), uint64(pix[x*4+2])
			i, w := int(first[x])*3, uint64(part[x])
			row[i] += r * w
			row[i+1] += g * w
			row[i+2] += b * w
			if w = uint64(dw) - w; w > 0 {
				row[i+3] += r * w
				row[i+4] += g * w
				row[i+5] += b * w
			}
		}
		// Likewise a source row lies in one destination row or two.
		for top, bottom := y*dh, (y+1)*dh; top < bottom; {
			line := top / sh
			end := min(bottom, (line+1)*sh)
			weight := uint64(end - top)
			for i, v := range row {
				sum[i] += v * weight
			}
			if end == (line+1)*sh {
				out := dst.Pix[line*dst.Stride:]
				for x := 0; x < dw; x++ {
					out[x*4] = uint8((sum[x*3] + area/2) / area)
					out[x*4+1] = uint8((sum[x*3+1] + area/2) / area)
					out[x*4+2] = uint8((sum[x*3+2] + area/2) / area)
					out[x*4+3] = 0xff
				}
				clear(sum)
			}
			top = end
		}
	}
}

// Orientation reads a JPEG's EXIF orientation: 1 (upright as stored) to 8.
// Browsers apply it when they show the file, so anything rendered from the
// decoded pixels has to apply it too or a portrait photo comes out sideways.
// Other formats, and files without the tag, are 1.
func Orientation(contentType string, data []byte) int {
	orientation := 1
	if contentType != "image/jpeg" {
		return orientation
	}
	jpegSegments(data, func(marker byte, seg []byte) bool {
		if marker != 0xE1 || !bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			return true
		}
		orientation = exifOrientation(seg[6:])
		return false
	})
	return orientation
}

// jpegSegments calls fn with each marker segment of a JPEG that comes before
// its image data, until fn returns false. It reads the file exactly as the
// decoder does, passing over stray bytes between segments, fill bytes and
// misplaced restart markers, so that what is learned here holds for what the
// decoder goes on to do.
func jpegSegments(data []byte, fn func(marker byte, payload []byte) bool) {
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		return
	}
	for i := 2; i+2 <= len(data); {
		lead, marker := data[i], data[i+1]
		i += 2
		for lead != 0xFF {
			if i == len(data) {
				return
			}
			lead, marker = marker, data[i]
			i++
		}
		if marker == 0 {
			continue
		}
		for marker == 0xFF {
			if i == len(data) {
				return
			}
			marker = data[i]
			i++
		}
		if marker >= 0xD0 && marker <= 0xD7 {
			continue
		}
		// The image data, or the end: the headers are behind us.
		if marker == 0xDA || marker == 0xD9 || i+2 > len(data) {
			return
		}
		size := int(data[i])<<8 | int(data[i+1])
		if size < 2 || i+size > len(data) || !fn(marker, data[i+2:i+size]) {
			return
		}
		i += size
	}
}

func exifOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 1
	}
	ifd := int64(order.Uint32(tiff[4:8]))
	if ifd+2 > int64(len(tiff)) {
		return 1
	}
	entries := int64(order.Uint16(tiff[ifd : ifd+2]))
	for k := int64(0); k < entries; k++ {
		e := ifd + 2 + k*12
		if e+12 > int64(len(tiff)) {
			return 1
		}
		if order.Uint16(tiff[e:e+2]) != 0x0112 {
			continue
		}
		// A SHORT with one value sits at the start of the value field.
		if v := int(order.Uint16(tiff[e+8 : e+10])); v >= 1 && v <= 8 {
			return v
		}
		return 1
	}
	return 1
}

// orient applies an EXIF orientation to stored pixels, on a new canvas. 2-4
// mirror or turn the image in place; 5-8 also swap its width and height.
func orient(src *canvas, orientation int) (*canvas, error) {
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	dw, dh := sw, sh
	if orientation >= 5 {
		dw, dh = sh, sw
	}
	dst, err := newCanvas(dw, dh)
	if err != nil {
		return nil, err
	}
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			var sx, sy int
			switch orientation {
			case 2: // mirrored
				sx, sy = sw-1-x, y
			case 3: // upside down
				sx, sy = sw-1-x, sh-1-y
			case 4: // flipped top to bottom
				sx, sy = x, sh-1-y
			case 5: // mirrored, then a quarter turn anticlockwise
				sx, sy = y, x
			case 6: // needs a quarter turn clockwise
				sx, sy = y, sh-1-x
			case 7: // mirrored, then a quarter turn clockwise
				sx, sy = sw-1-y, sh-1-x
			case 8: // needs a quarter turn anticlockwise
				sx, sy = sw-1-y, x
			}
			s := src.Pix[sy*src.Stride+sx*4:]
			d := dst.Pix[y*dst.Stride+x*4:]
			d[0], d[1], d[2], d[3] = s[0], s[1], s[2], s[3]
		}
	}
	return dst, nil
}
