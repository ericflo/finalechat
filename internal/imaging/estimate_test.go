package imaging

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type sample struct {
	name        string
	contentType string
	data        []byte
}

// samples is one image for every way a decoder lays an image out: those
// Go's encoders can write, made here, and from testdata those they cannot.
func samples(t *testing.T) []sample {
	t.Helper()
	encode := func(name, contentType string, img image.Image, orientation int) sample {
		var buf bytes.Buffer
		var err error
		if contentType == "image/png" {
			err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img)
		} else {
			err = jpeg.Encode(&buf, img, nil)
		}
		if err != nil {
			t.Fatal(err)
		}
		data := buf.Bytes()
		if orientation != 1 {
			data = withOrientation(t, data, binary.BigEndian, orientation)
		}
		return sample{name, contentType, data}
	}
	// The encoder writes an image without transparency as RGB; a clear pixel
	// keeps the alpha channel in the file.
	alpha := func(w, h int) *image.NRGBA {
		img := image.NewNRGBA(image.Rect(0, 0, w, h))
		for i := 3; i < len(img.Pix); i += 4 {
			img.Pix[i] = 0xff
		}
		img.Pix[3] = 0
		return img
	}
	photo := func(w, h int) *image.YCbCr {
		return image.NewYCbCr(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio420)
	}
	all := []sample{
		encode("a 1080p screenshot with alpha", "image/png", alpha(1920, 1080), 1),
		encode("a 1080p screenshot without", "image/png", image.NewRGBA(image.Rect(0, 0, 1920, 1080)), 1),
		encode("a phone screenshot", "image/jpeg", photo(1440, 3120), 1),
		encode("a long screenshot", "image/jpeg", photo(1440, 16000), 1),
		encode("a long PNG screenshot", "image/png", alpha(1080, 9000), 1),
		encode("a 4K still", "image/png", alpha(3840, 2160), 1),
		encode("a 16-bit render", "image/png", image.NewNRGBA64(image.Rect(0, 0, 2000, 1500)), 1),
		encode("a 16-bit grey scan", "image/png", image.NewGray16(image.Rect(0, 0, 2000, 1500)), 1),
		encode("a 12 MP photo", "image/jpeg", photo(4000, 3000), 1),
		encode("a 12 MP photo on its side", "image/jpeg", photo(4000, 3000), 6),
		encode("a 24 MP photo", "image/jpeg", photo(6000, 4000), 1),
		encode("a tall poster", "image/jpeg", photo(3000, 9000), 1),
		encode("a grey scan", "image/jpeg", image.NewGray(image.Rect(0, 0, 3000, 4000)), 1),
		encode("an icon", "image/png", alpha(64, 64), 1),
	}
	files, err := filepath.Glob("testdata/*")
	if err != nil || len(files) == 0 {
		t.Fatalf("no testdata: %v", err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		contentType := map[string]string{".jpg": "image/jpeg", ".png": "image/png", ".webp": "image/webp"}[filepath.Ext(f)]
		all = append(all, sample{filepath.Base(f), contentType, data})
	}
	return all
}

// Estimate is what the server admits and refuses images by, so it has to
// cover what the work really allocates, without being so far over that
// images are turned away for nothing. This holds wherever the canvases live.
func TestEstimateCoversWhatTheWorkAllocates(t *testing.T) {
	for _, disk := range []bool{true, false} {
		if disk && !DiskScratch() {
			t.Log("no disk scratch here; checking canvases in memory only")
			continue
		}
		scratchOnDisk(t, disk)
		for _, c := range samples(t) {
			cost := Estimate(c.contentType, c.data, 640)
			if cost.Width == 0 {
				t.Errorf("%s: no estimate", c.name)
				continue
			}
			var base, display uint64
			var img image.Image
			var orientation int
			base = allocated(func() {
				var err error
				if img, _, err = Decode(c.contentType, c.data); err != nil {
					t.Fatalf("%s: %v", c.name, err)
				}
				orientation = Orientation(c.contentType, c.data)
				if _, _, err := Thumbnail(img, 640, orientation); err != nil {
					t.Fatalf("%s: %v", c.name, err)
				}
			})
			display = allocated(func() {
				if _, _, err := Display(img, orientation, math.MaxInt); err != nil {
					t.Fatalf("%s: %v", c.name, err)
				}
			})
			t.Logf("canvases on disk %-5v %-30s decode and thumbnail: budgeted %3d MiB, used %3d; display: budgeted %3d, used %3d",
				disk, c.name, cost.Base>>20, base>>20, cost.Display>>20, display>>20)
			for _, part := range []struct {
				what     string
				budgeted int64
				used     uint64
			}{{"decoding and the thumbnail", cost.Base, base}, {"the display copy", cost.Display, display}} {
				// A megabyte or two of decoder and encoder state is not counted.
				if int64(part.used) > part.budgeted+part.budgeted/10+(2<<20) {
					t.Errorf("canvases on disk %v: %s: %s allocated %d MiB but only %d was budgeted", disk, c.name, part.what, part.used>>20, part.budgeted>>20)
				}
				if part.budgeted > 2*int64(part.used)+(4<<20) {
					t.Errorf("canvases on disk %v: %s: %d MiB budgeted for %s, which allocates %d", disk, c.name, part.budgeted>>20, part.what, part.used>>20)
				}
			}
		}
	}
}

// What describe says of an image has to be what the decoder hands back, or
// the estimate budgets for a canvas that is not made, or misses one that is.
func TestDescribeKnowsWhichImagesNeedNoCanvas(t *testing.T) {
	for _, c := range samples(t) {
		l, ok := describe(c.contentType, c.data)
		if !ok {
			t.Errorf("%s: not described", c.name)
			continue
		}
		img, info, err := Decode(c.contentType, c.data)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if l.w != info.Width || l.h != info.Height {
			t.Errorf("%s: described as %dx%d, decodes as %dx%d", c.name, l.w, l.h, info.Width, info.Height)
		}
		if direct := flatten(img) != nil; direct != l.direct {
			t.Errorf("%s: decodes to %T, which needs no canvas: %v; described as %v", c.name, img, direct, l.direct)
		}
	}
}

func TestEstimateOfWhatCannotBeDecodedIsNothing(t *testing.T) {
	var small bytes.Buffer
	if err := png.Encode(&small, image.NewGray(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]sample{
		"not an image":     {"", "image/png", []byte("not an image")},
		"a truncated JPEG": {"", "image/jpeg", []byte{0xFF, 0xD8, 0xFF, 0xC0, 0x00}},
		"a JPEG, no frame": {"", "image/jpeg", []byte{0xFF, 0xD8, 0xFF, 0xD9}},
		"an unknown type":  {"", "image/tiff", small.Bytes()},
		"an empty file":    {"", "image/webp", nil},
	} {
		if got := Estimate(c.contentType, c.data, 640); got != (Cost{}) {
			t.Errorf("%s: estimated %+v, want nothing", name, got)
		}
		if _, _, err := Decode(c.contentType, c.data); err == nil {
			t.Errorf("%s: decodes, so it has to have a cost", name)
		}
	}
}

// A header may claim any size at all. The cost of what it claims is worked
// out without overflowing into something small, and without the decoder
// being asked, so that the image is refused for its size.
func TestEstimateOfAnAbsurdHeader(t *testing.T) {
	var small bytes.Buffer
	if err := png.Encode(&small, image.NewGray(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	sized := func(w, h uint32, depth byte) []byte {
		data := append([]byte(nil), small.Bytes()...)
		binary.BigEndian.PutUint32(data[16:], w)
		binary.BigEndian.PutUint32(data[20:], h)
		data[24], data[25] = depth, 6 // RGBA
		binary.BigEndian.PutUint32(data[29:], crc32.ChecksumIEEE(data[12:29]))
		return data
	}
	for _, c := range []struct {
		name string
		w, h uint32
	}{
		{"3600 MP, past what Decode takes", 60000, 60000},
		{"2^30 a side", 1 << 30, 1 << 30},
	} {
		cost := Estimate("image/png", sized(c.w, c.h, 16), 640)
		if cost.Width != int(c.w) || cost.Height != int(c.h) {
			t.Errorf("%s: read as %dx%d", c.name, cost.Width, cost.Height)
		}
		if cost.Base < 8*3_600_000_000 || cost.Display < 0 || cost.Base+cost.Display < cost.Base {
			t.Errorf("%s: costed at %d and %d bytes", c.name, cost.Base, cost.Display)
		}
	}

	// Past that the decoder turns the header away itself.
	largest := sized(1<<31-1, 1<<31-1, 16)
	if _, _, err := Decode("image/png", largest); err == nil {
		t.Error("a PNG of 2^31-1 a side was decoded")
	}
	if cost := Estimate("image/png", largest, 640); cost.Base < 0 || cost.Display < 0 {
		t.Errorf("a PNG of 2^31-1 a side: costed at %+v", cost)
	}

	// A WebP in the extended format gives its canvas up front, and the
	// decoder allocates an alpha plane of that size before looking further.
	data, err := os.ReadFile("testdata/lossy-alpha.webp")
	if err != nil {
		t.Fatal(err)
	}
	if string(data[12:16]) != "VP8X" {
		t.Fatal("the fixture is not in the extended format")
	}
	huge := append([]byte(nil), data...)
	copy(huge[24:30], []byte{0x03, 0xB5, 0x00, 0x03, 0xB5, 0x00}) // 46340×46340, the most the format allows
	var cost Cost
	used := allocated(func() { cost = Estimate("image/webp", huge, 640) })
	if cost.Width != 46340 || cost.Height != 46340 || cost.Base < 46340*46340 {
		t.Errorf("a WebP claiming 46340×46340: %+v", cost)
	}
	if used > 1<<20 {
		t.Errorf("costing it allocated %d MiB; nothing of the image should be read", used>>20)
	}
}

// What the decoder accepts is wider than what encoders write. Each of these
// is a file it decodes; the cost has to be for the image it finds there,
// however the file is dressed.
func TestEstimateIsNotFooledByOddFiles(t *testing.T) {
	scratchOnDisk(t, false)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewYCbCr(image.Rect(0, 0, 2000, 1500), image.YCbCrSubsampleRatio420), nil); err != nil {
		t.Fatal(err)
	}
	plain := append([]byte(nil), buf.Bytes()...)
	join := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	// The frame header of a 16×16 image, inside a comment. A reader that
	// takes the stuffed pair before it for a segment lands on this header;
	// the decoder skips the pair and then the comment.
	decoy := join([]byte{0xFF, 0xD8, 0xFF, 0x00, 0x00, 0x04, 0xFF, 0xFE, 0xFF, 0xC0},
		[]byte{0, 17, 8, 0, 16, 0, 16, 3, 1, 0x22, 0, 2, 0x11, 1, 3, 0x11, 1})
	decoy = join(decoy, make([]byte, 8+0xFFC0-len(decoy)), plain[2:])
	// Three components marked as RGB by a segment that comes after the image.
	adobe := join(plain[:len(plain)-2], []byte{0xFF, 0xEE, 0, 14}, []byte("Adobe"), make([]byte, 7), []byte{0xFF, 0xD9})

	buf.Reset()
	if err := gif.Encode(&buf, image.NewPaletted(image.Rect(0, 0, 2000, 1500), palette.Plan9), nil); err != nil {
		t.Fatal(err)
	}
	interlaced := append([]byte(nil), buf.Bytes()...)
	at := 13
	if interlaced[10]&0x80 != 0 {
		at += 3 << (interlaced[10]&7 + 1)
	}
	for interlaced[at] == 0x21 { // extensions
		for at += 2; interlaced[at] != 0; at += 1 + int(interlaced[at]) {
		}
		at++
	}
	if interlaced[at] != 0x2C {
		t.Fatal("no image descriptor where one was expected")
	}
	interlaced[at+9] |= 0x40

	var first bytes.Buffer
	if err := png.Encode(&first, image.NewNRGBA64(image.Rect(0, 0, 1200, 900))); err != nil {
		t.Fatal(err)
	}
	// A chunk the decoder does not know, ahead of the header.
	chunk := []byte{0, 0, 0, 4, 'o', 'd', 'D', 'y', 1, 2, 3, 4, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(chunk[12:], crc32.ChecksumIEEE(chunk[4:12]))
	late := join(first.Bytes()[:8], chunk, first.Bytes()[8:])

	for _, c := range []struct {
		name        string
		contentType string
		data        []byte
		w, h        int
	}{
		{"a plain JPEG", "image/jpeg", plain, 2000, 1500},
		{"stray bytes before the first segment", "image/jpeg", join(plain[:2], []byte("stray"), plain[2:]), 2000, 1500},
		{"a stuffed pair before the first segment", "image/jpeg", join(plain[:2], []byte{0xFF, 0x00}, plain[2:]), 2000, 1500},
		{"fill bytes before a marker", "image/jpeg", join(plain[:2], []byte{0xFF, 0xFF, 0xFF}, plain[2:]), 2000, 1500},
		{"a restart marker out of place", "image/jpeg", join(plain[:2], []byte{0xFF, 0xD3}, plain[2:]), 2000, 1500},
		{"a small frame header hidden in a comment", "image/jpeg", decoy, 2000, 1500},
		{"marked RGB after the image", "image/jpeg", adobe, 2000, 1500},
		{"an interlaced GIF", "image/gif", interlaced, 2000, 1500},
		{"a PNG whose header is not first", "image/png", late, 1200, 900},
	} {
		cost := Estimate(c.contentType, c.data, 640)
		var img image.Image
		var err error
		used := allocated(func() { img, _, err = Decode(c.contentType, c.data) })
		if err != nil {
			t.Errorf("%s: the decoder was meant to take this: %v", c.name, err)
			continue
		}
		if b := img.Bounds(); b.Dx() != c.w || b.Dy() != c.h || cost.Width != c.w || cost.Height != c.h {
			t.Errorf("%s: decodes as %dx%d, costed as %dx%d, want %dx%d", c.name, b.Dx(), b.Dy(), cost.Width, cost.Height, c.w, c.h)
		}
		t.Logf("%-42s decodes to %-13T budgeted %2d MiB, used %2d", c.name, img, cost.Base>>20, used>>20)
		if int64(used) > cost.Base+cost.Base/10+(2<<20) {
			t.Errorf("%s: decoding allocated %d MiB and %d was budgeted", c.name, used>>20, cost.Base>>20)
		}
		if l, _ := describe(c.contentType, c.data); l.direct && flatten(img) == nil {
			t.Errorf("%s: decodes to %T, which needs a canvas that was not budgeted", c.name, img)
		}
	}
}

// The limits an image meets depend on its kind. These are the ones people
// will meet with what a 256 MiB server gives image work (144 MiB), here so
// that a change to the estimate that moves them is seen.
func TestWhatFitsInTheServersBudget(t *testing.T) {
	scratchOnDisk(t, true)
	const budget = 144 << 20
	header := func(contentType string, w, h int) []byte {
		if contentType == "image/png" {
			var buf bytes.Buffer
			_ = png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 1, 1)))
			data := buf.Bytes()
			binary.BigEndian.PutUint32(data[16:], uint32(w))
			binary.BigEndian.PutUint32(data[20:], uint32(h))
			data[25] = 6 // RGBA
			return data
		}
		// A baseline 4:2:0 frame header is all Estimate reads of a JPEG.
		return []byte{0xFF, 0xD8, 0xFF, 0xC0, 0, 17, 8, byte(h >> 8), byte(h), byte(w >> 8), byte(w), 3, 1, 0x22, 0, 2, 0x11, 1, 3, 0x11, 1, 0xFF, 0xD9}
	}
	for _, c := range []struct {
		name        string
		contentType string
		w, h        int
		fits        bool
	}{
		{"a 50 MP phone photo", "image/jpeg", 8160, 6120, true},
		{"a long JPEG screenshot, 1440×30000", "image/jpeg", 1440, 30000, true},
		{"a JPEG as long as JPEGs get, 1440×65535", "image/jpeg", 1440, 65535, true},
		{"a 100 MP JPEG", "image/jpeg", 10000, 10000, false},
		{"a long PNG screenshot, 1080×30000", "image/png", 1080, 30000, true},
		{"a long PNG screenshot, 1440×25000", "image/png", 1440, 25000, true},
		{"a longer one, 1440×27000", "image/png", 1440, 27000, false},
		{"a 33 MP PNG (8K)", "image/png", 7680, 4320, true},
		{"a 40 MP PNG", "image/png", 8000, 5000, false},
	} {
		cost := Estimate(c.contentType, header(c.contentType, c.w, c.h), 640)
		if cost.Width != c.w || cost.Height != c.h {
			t.Fatalf("%s: read as %dx%d", c.name, cost.Width, cost.Height)
		}
		t.Logf("%-36s %5.1f MP needs %3d MiB", c.name, float64(c.w)*float64(c.h)/1e6, cost.Base>>20)
		if fits := cost.Base <= budget; fits != c.fits {
			t.Errorf("%s: needs %d MiB; fits in %d: %v, want %v", c.name, cost.Base>>20, budget>>20, fits, c.fits)
		}
	}
}

func TestDisplaySize(t *testing.T) {
	for _, c := range []struct {
		name         string
		w, h, dw, dh int
	}{
		{"a 1080p screenshot", 1920, 1080, 1920, 1080},
		{"a 4K still", 3840, 2160, 3840, 2160},
		{"a 12 MP photo, to the pixel budget", 4000, 3000, 3325, 2494},
		{"a 24 MP photo", 6000, 4000, 3527, 2351},
		{"a phone screenshot", 1440, 3120, 1440, 3120},
		{"a long screenshot keeps its width", 1440, 25000, 1440, 25000},
		{"so does a longer one", 1080, 60000, 1080, 60000},
		{"and one on its side", 30000, 2160, 30000, 2160},
		{"a tall poster, to the short side", 3000, 12000, 2160, 8640},
		{"a panorama", 16000, 4000, 8640, 2160},
	} {
		if w, h := displaySize(c.w, c.h); w != c.dw || h != c.dh {
			t.Errorf("%s: %dx%d becomes %dx%d, want %dx%d", c.name, c.w, c.h, w, h, c.dw, c.dh)
		}
	}
}

// The canvas-free path has to draw what the canvas would have: the same
// picture, with the same white behind anything transparent.
func TestDisplayWithoutACanvasLooksTheSame(t *testing.T) {
	const w, h = 320, 240
	paint := func(set func(x, y int, c color.NRGBA)) {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				// Soft colour, fading out to the right and gone in a corner.
				a := uint8(255 - x*255/w)
				if x < 40 && y < 40 {
					a = 0
				}
				set(x, y, color.NRGBA{uint8(x * 255 / w), uint8(y * 255 / h), uint8(200 - x/2), a})
			}
		}
	}
	nrgba := image.NewNRGBA(image.Rect(0, 0, w, h))
	paint(func(x, y int, c color.NRGBA) { nrgba.SetNRGBA(x, y, c) })
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	paint(func(x, y int, c color.NRGBA) { rgba.Set(x, y, c) })
	ycbcr := image.NewYCbCr(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio420)
	gray := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			ycbcr.Y[ycbcr.YOffset(x, y)] = uint8(x * 255 / w)
			ycbcr.Cb[ycbcr.COffset(x, y)] = uint8(y * 255 / h)
			ycbcr.Cr[ycbcr.COffset(x, y)] = uint8(255 - y*255/h)
			gray.SetGray(x, y, color.Gray{uint8((x + y) * 255 / (w + h))})
		}
	}
	// Part of a larger image: rows that do not start at the slice's start.
	inner := image.NewNRGBA(image.Rect(0, 0, w+20, h+10))
	paint(func(x, y int, c color.NRGBA) { inner.SetNRGBA(x+7, y+3, c) })
	for name, img := range map[string]image.Image{
		"NRGBA": nrgba, "RGBA": rgba, "YCbCr": ycbcr, "Gray": gray,
		"NRGBA sub-image": inner.SubImage(image.Rect(7, 3, 7+w, 3+h)),
	} {
		var drawn bytes.Buffer
		if _, err := render(&drawn, img, w, h, 1, displayQuality, false); err != nil {
			t.Fatal(err)
		}
		direct, info, err := Display(img, 1, math.MaxInt)
		if err != nil || info.Width != w || info.Height != h {
			t.Fatalf("%s: %dx%d, %v", name, info.Width, info.Height, err)
		}
		a, err := jpeg.Decode(bytes.NewReader(drawn.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		b, err := jpeg.Decode(bytes.NewReader(direct))
		if err != nil {
			t.Fatal(err)
		}
		var worst, total int
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				p, q := color.RGBAModel.Convert(a.At(x, y)).(color.RGBA), color.RGBAModel.Convert(b.At(x, y)).(color.RGBA)
				for _, d := range []int{int(p.R) - int(q.R), int(p.G) - int(q.G), int(p.B) - int(q.B)} {
					if d < 0 {
						d = -d
					}
					worst, total = max(worst, d), total+d
				}
			}
		}
		mean := float64(total) / float64(w*h*3)
		t.Logf("%s: mean difference %.2f levels, largest %d", name, mean, worst)
		if mean > 1.5 || worst > 24 {
			t.Errorf("%s: drawn directly it differs from the canvas by %.2f levels on average, %d at most", name, mean, worst)
		}
		if strings.HasPrefix(name, "NRGBA") || name == "RGBA" {
			if c := color.RGBAModel.Convert(b.At(10, 10)).(color.RGBA); !near(c, color.RGBA{255, 255, 255, 255}) {
				t.Errorf("%s: the transparent corner came out %v, want white", name, c)
			}
		}
	}
}

func TestDisplayOfALongScreenshot(t *testing.T) {
	long := image.NewYCbCr(image.Rect(0, 0, 1440, 30000), image.YCbCrSubsampleRatio420) // 43 MP
	var out []byte
	var info Info
	var err error
	used := allocated(func() { out, info, err = Display(long, 1, math.MaxInt) })
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 1440 || info.Height != 30000 {
		t.Errorf("a 1440×30000 screenshot came out %dx%d; it should keep every pixel", info.Width, info.Height)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(out))
	if err != nil || cfg.Width != 1440 || cfg.Height != 30000 {
		t.Errorf("the JPEG is %dx%d, %v", cfg.Width, cfg.Height, err)
	}
	// Nothing is drawn anywhere first: all that is allocated is the output.
	if used > uint64(len(out))*3+(1<<20) {
		t.Errorf("allocated %d KiB to write %d KiB", used>>10, len(out)>>10)
	}
	if _, _, err := Display(image.NewGray(image.Rect(0, 0, 100, 70000)), 1, math.MaxInt); err == nil {
		t.Error("70000 pixels is longer than a JPEG can be, and Display did not say so")
	}
}

func TestDisplayGivesUpOnceItIsNoSmaller(t *testing.T) {
	noise := image.NewRGBA(image.Rect(0, 0, 400, 400))
	for i := range noise.Pix {
		noise.Pix[i] = uint8(i*2654435761>>13) | 1
	}
	full, _, err := Display(noise, 1, math.MaxInt)
	if err != nil {
		t.Fatal(err)
	}
	if out, _, err := Display(noise, 1, len(full)-1); err == nil {
		t.Errorf("made a %d-byte rendition with a limit of %d", len(out), len(full)-1)
	}
	if out, _, err := Display(noise, 1, len(full)); err != nil || len(out) != len(full) {
		t.Errorf("at exactly its size: %d bytes, %v", len(out), err)
	}
}

func TestLargeCanvasesLiveOnDisk(t *testing.T) {
	if !DiskScratch() {
		t.Skip("no disk scratch here")
	}
	var c *canvas
	var err error
	used := allocated(func() { c, err = newCanvas(3000, 2000) }) // 24 MB
	if err != nil {
		t.Fatal(err)
	}
	if used > 64<<10 {
		t.Errorf("a canvas on disk allocated %d KiB of memory", used>>10)
	}
	if len(c.Pix) != 3000*2000*4 || c.Pix[0] != 0 || c.Pix[len(c.Pix)-1] != 0 {
		t.Fatalf("canvas is %d bytes, first %d, last %d; want 24000000 zeroes", len(c.Pix), c.Pix[0], c.Pix[len(c.Pix)-1])
	}
	c.SetRGBA(2999, 1999, color.RGBA{1, 2, 3, 255})
	if got := c.RGBAAt(2999, 1999); got != (color.RGBA{1, 2, 3, 255}) {
		t.Errorf("read back %v", got)
	}
	c.free()
	small, err := newCanvas(640, 480)
	if err != nil {
		t.Fatal(err)
	}
	small.free()
	small.SetRGBA(0, 0, color.RGBA{9, 9, 9, 255}) // a small canvas is ordinary memory, still there
}

// Canvases kept on disk cost no memory, and the disk they take is told
// apart, so that it can be shared out too.
func TestEstimateSaysWhatGoesToDisk(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewYCbCr(image.Rect(0, 0, 6000, 4000), image.YCbCrSubsampleRatio420), nil); err != nil {
		t.Fatal(err)
	}
	photo := buf.Bytes()
	sideways := withOrientation(t, photo, binary.BigEndian, 6)
	const canvas = 3527 * 2351 * 4 // a 24 MP photo's display copy
	for _, c := range []struct {
		name            string
		disk            bool
		data            []byte
		memory, scratch int64
	}{
		{"in memory", false, photo, canvas, 0},
		{"in memory, turned", false, sideways, 2 * canvas, 0},
		{"on disk", true, photo, 0, canvas},
		{"on disk, turned", true, sideways, 0, 2 * canvas},
	} {
		if c.disk && !DiskScratch() {
			continue
		}
		scratchOnDisk(t, c.disk)
		cost := Estimate("image/jpeg", c.data, 640)
		if held := cost.Display - int64(len(c.data))*3/2; held < c.memory || held > c.memory+(2<<20) || cost.Scratch != c.scratch {
			t.Errorf("%s: the display copy holds %d MiB of canvas in memory and %d on disk, want %d and %d", c.name, held>>20, cost.Scratch>>20, c.memory>>20, c.scratch>>20)
		}
	}
	// A long screenshot is encoded as it is: no canvas anywhere.
	scratchOnDisk(t, DiskScratch())
	buf.Reset()
	if err := jpeg.Encode(&buf, image.NewYCbCr(image.Rect(0, 0, 1440, 20000), image.YCbCrSubsampleRatio420), nil); err != nil {
		t.Fatal(err)
	}
	if cost := Estimate("image/jpeg", buf.Bytes(), 640); cost.Scratch != 0 || cost.Display > int64(buf.Len())*3/2 {
		t.Errorf("a long screenshot: %+v", cost)
	}
}
