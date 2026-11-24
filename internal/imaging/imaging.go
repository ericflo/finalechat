// Package imaging decodes uploaded images and renders their thumbnails and
// display renditions.
package imaging

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math"

	"golang.org/x/image/draw"
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

// DisplayPixels caps the display rendition at a 4K frame (3840×2160).
// Screenshots up to that size keep every pixel, so text stays sharp when
// zoomed; anything larger (phone photos, 5K captures) is scaled down to it.
const DisplayPixels = 3840 * 2160

// JPEG qualities. The display rendition is what the app shows full screen,
// so it is compressed gently; a thumbnail is seen small.
const (
	thumbQuality   = 82
	displayQuality = 85
)

// Thumbnail scales the image so its longest side is at most maxSide and
// encodes it as JPEG, upright per the EXIF orientation. Images already
// within bounds are re-encoded so the thumbnail is always a small, fast
// JPEG.
func Thumbnail(img image.Image, maxSide, orientation int) ([]byte, Info, error) {
	b := img.Bounds()
	w, h := thumbnailSize(b.Dx(), b.Dy(), maxSide)
	return render(img, w, h, orientation, thumbQuality)
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
// the image's own resolution up to DisplayPixels, upright per the EXIF
// orientation. The caller keeps it only when it is worth having (it can come
// out larger than an already well-compressed original).
func Display(img image.Image, orientation int) ([]byte, Info, error) {
	b := img.Bounds()
	w, h := displaySize(b.Dx(), b.Dy())
	return render(img, w, h, orientation, displayQuality)
}

// displaySize keeps w×h unless it is over the display budget, and then
// scales it to the budget.
func displaySize(w, h int) (int, int) {
	if pixels := int64(w) * int64(h); pixels > DisplayPixels {
		f := math.Sqrt(float64(DisplayPixels) / float64(pixels))
		return int(float64(w) * f), int(float64(h) * f)
	}
	return w, h
}

// render draws the image at w×h on white (so transparent screenshots do not
// turn black in JPEG), turns it upright, and encodes it.
func render(img image.Image, w, h, orientation, quality int) ([]byte, Info, error) {
	b := img.Bounds()
	w, h = max(w, 1), max(h, 1)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	switch {
	case w == b.Dx() && h == b.Dy():
		draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Over)
	case kernelFits(w, h, b.Dx(), b.Dy()):
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	default:
		shrink(dst, img)
	}
	dst = orient(dst, orientation)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: quality}); err != nil {
		return nil, Info{}, err
	}
	return buf.Bytes(), Info{Width: dst.Bounds().Dx(), Height: dst.Bounds().Dy()}, nil
}

// While it works, a kernel scaler such as draw.CatmullRom holds every pixel
// of (destination width × source height) as four float64s. For the
// thumbnail of a screenshot that is some tens of megabytes. For a
// 12-megapixel photo brought down to the display budget it is over 300 MB,
// more than the server has, so past kernelBudget an image is scaled by
// shrink instead.
const (
	kernelBytes  = 32
	kernelBudget = 32 << 20
)

// kernelFits reports whether scaling sw×sh to w×h with the kernel scaler
// stays within its budget. shrink only scales down, so an enlargement (which
// nothing here asks for) is the kernel scaler's whatever it costs.
func kernelFits(w, h, sw, sh int) bool {
	return w > sw || h > sh || int64(w)*int64(sh)*kernelBytes <= kernelBudget
}

// Cost estimates how many bytes decoding the image and rendering its
// thumbnail and display copy allocate between them, from its header alone,
// so that the caller can keep many images from being worked on at once. It
// is 0 for an image Decode would refuse.
func Cost(contentType string, data []byte, thumbSide int) int64 {
	cfg, err := decodeConfig(contentType, bytes.NewReader(data))
	if err != nil || checkDimensions(cfg.Width, cfg.Height) != nil {
		return 0
	}
	// What the decoders hand back: three planes at most for a JPEG, one or
	// two bytes for grey, four for colour with alpha, eight at 16 bits.
	perPixel := int64(4)
	switch cfg.ColorModel {
	case color.YCbCrModel:
		perPixel = 3
	case color.GrayModel:
		perPixel = 1
	case color.Gray16Model:
		perPixel = 2
	case color.RGBA64Model, color.NRGBA64Model:
		perPixel = 8
	}
	orientation := Orientation(contentType, data)
	cost := int64(cfg.Width) * int64(cfg.Height) * perPixel
	w, h := thumbnailSize(cfg.Width, cfg.Height, thumbSide)
	cost += renderCost(w, h, cfg.Width, cfg.Height, orientation)
	w, h = displaySize(cfg.Width, cfg.Height)
	return cost + renderCost(w, h, cfg.Width, cfg.Height, orientation)
}

// renderCost is what render allocates to make a w×h rendition of a sw×sh
// image: the canvas, a second one if it has to be turned, and whatever the
// scaler works in.
func renderCost(w, h, sw, sh, orientation int) int64 {
	cost := int64(max(w, 1)) * int64(max(h, 1)) * 4
	if orientation > 1 {
		cost *= 2
	}
	switch {
	case w == sw && h == sh:
	case kernelFits(w, h, sw, sh):
		cost += int64(w) * int64(sh) * kernelBytes
	default:
		cost += int64(sw)*(shrinkRows*4+8) + int64(w)*48
	}
	return cost
}

// shrinkRows is how many source rows shrink converts at a time.
const shrinkRows = 32

// shrink scales src down to fill dst, on white where src is transparent, by
// averaging the source pixels that lie under each destination pixel. It
// reads the source a few rows at a time, so it needs the same small amount
// of memory however large the image is.
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
	if contentType != "image/jpeg" || len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xFF {
			return 1
		}
		marker := data[i+1]
		switch {
		case marker == 0xFF: // fill byte
			i++
			continue
		case marker == 0x01 || marker >= 0xD0 && marker <= 0xD8: // no payload
			i += 2
			continue
		case marker == 0xDA || marker == 0xD9: // image data: the metadata is behind us
			return 1
		}
		size := int(data[i+2])<<8 | int(data[i+3])
		if size < 2 || i+2+size > len(data) {
			return 1
		}
		if seg := data[i+4 : i+2+size]; marker == 0xE1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			return exifOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 1
}

// exifOrientation finds tag 0x0112 in the first IFD of a TIFF block.
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

// orient applies an EXIF orientation to stored pixels. 2-4 mirror or turn
// the image in place; 5-8 also swap its width and height.
func orient(src *image.RGBA, orientation int) *image.RGBA {
	if orientation < 2 || orientation > 8 {
		return src
	}
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	dw, dh := sw, sh
	if orientation >= 5 {
		dw, dh = sh, sw
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
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
	return dst
}
