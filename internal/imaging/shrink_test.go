package imaging

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"math/rand"
	"runtime"
	"testing"
)

// allocated reports how many bytes fn allocated in total.
func allocated(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// A phone photo is larger than the display budget and has to be scaled down.
// The scaler must not need memory in proportion to the photo: the server
// runs in a few hundred megabytes, and one upload must not take it down.
// (draw.CatmullRom needed 414 MiB for the display copy of this one.)
func TestRenditionsOfALargePhotoStayWithinMemory(t *testing.T) {
	photo := image.NewYCbCr(image.Rect(0, 0, 4608, 3456), image.YCbCrSubsampleRatio420) // 16 MP
	for name, fn := range map[string]func() ([]byte, Info, error){
		"display":   func() ([]byte, Info, error) { return Display(photo, 6) },
		"thumbnail": func() ([]byte, Info, error) { return Thumbnail(photo, 640, 6) },
	} {
		var err error
		got := allocated(func() { _, _, err = fn() })
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Logf("%s of a 16 MP photo allocated %d MiB", name, got>>20)
		// Two canvases at the display budget (one drawn, one turned upright)
		// are 64 MiB; nothing should scale with the photo beyond that.
		if got > 80<<20 {
			t.Errorf("%s of a 16 MP photo allocated %d MiB; want at most 80", name, got>>20)
		}
	}
}

// Cost is what the server budgets image work by, so it has to cover what the
// work allocates, without being so far over that images wait for nothing.
func TestCostCoversWhatTheWorkAllocates(t *testing.T) {
	encode := func(contentType string, img image.Image) []byte {
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
		return buf.Bytes()
	}
	for _, c := range []struct {
		name        string
		contentType string
		img         image.Image
		orientation int
	}{
		{"a 1080p screenshot", "image/png", image.NewNRGBA(image.Rect(0, 0, 1920, 1080)), 1},
		{"a phone screenshot", "image/png", image.NewRGBA(image.Rect(0, 0, 1440, 3120)), 1},
		{"a 4K still", "image/png", image.NewNRGBA(image.Rect(0, 0, 3840, 2160)), 1},
		{"a 16-bit render", "image/png", image.NewNRGBA64(image.Rect(0, 0, 2000, 1500)), 1},
		{"a 12 MP photo", "image/jpeg", image.NewYCbCr(image.Rect(0, 0, 4000, 3000), image.YCbCrSubsampleRatio420), 1},
		{"a 12 MP photo on its side", "image/jpeg", image.NewYCbCr(image.Rect(0, 0, 4000, 3000), image.YCbCrSubsampleRatio420), 6},
		{"a 24 MP photo", "image/jpeg", image.NewYCbCr(image.Rect(0, 0, 6000, 4000), image.YCbCrSubsampleRatio444), 1},
		{"a grey scan", "image/jpeg", image.NewGray(image.Rect(0, 0, 3000, 4000)), 1},
		{"an icon", "image/png", image.NewNRGBA(image.Rect(0, 0, 64, 64)), 1},
	} {
		data := encode(c.contentType, c.img)
		if c.orientation != 1 {
			data = withOrientation(t, data, binary.BigEndian, c.orientation)
		}
		cost := Cost(c.contentType, data, 640)
		used := allocated(func() {
			img, _, err := Decode(c.contentType, data)
			if err != nil {
				t.Fatal(err)
			}
			orientation := Orientation(c.contentType, data)
			if _, _, err := Thumbnail(img, 640, orientation); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Display(img, orientation); err != nil {
				t.Fatal(err)
			}
		})
		t.Logf("%s: budgeted %d MiB, allocated %d MiB", c.name, cost>>20, used>>20)
		// A megabyte or two of decoder and encoder state is not counted.
		if int64(used) > cost+cost/10+(3<<20) {
			t.Errorf("%s: allocated %d MiB but budgeted only %d", c.name, used>>20, cost>>20)
		}
		if cost > 3*int64(used)+(3<<20) {
			t.Errorf("%s: budgeted %d MiB for work that allocates %d", c.name, cost>>20, used>>20)
		}
	}
	if got := Cost("image/png", []byte("not an image"), 640); got != 0 {
		t.Errorf("an unreadable image costs %d, want 0", got)
	}
}

// The largest image the server accepts must not need more than the photo
// above: what shrink holds depends on the width alone, and only a little.
func TestShrinkNeedsLittleMemoryAtAnySize(t *testing.T) {
	wide := image.NewGray(image.Rect(0, 0, 30000, 6000)) // 180 MP
	dst := image.NewRGBA(image.Rect(0, 0, 640, 128))
	got := allocated(func() { shrink(dst, wide) })
	t.Logf("shrinking a 180 MP image allocated %d KiB", got>>10)
	if got > 8<<20 {
		t.Errorf("shrinking a 180 MP image allocated %d MiB; want at most 8", got>>20)
	}
}

// reference scales by the definition: each destination pixel is the mean of
// the source under it, weighted by how much of each source pixel it covers,
// with transparency filled in white.
func reference(src image.Image, dw, dh int) *image.RGBA {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	flat := image.NewRGBA(image.Rect(0, 0, sw, sh))
	draw.Draw(flat, flat.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), src, sb.Min, draw.Over)
	out := image.NewRGBA(image.Rect(0, 0, dw, dh))
	fx, fy := float64(sw)/float64(dw), float64(sh)/float64(dh)
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			x0, x1, y0, y1 := float64(x)*fx, float64(x+1)*fx, float64(y)*fy, float64(y+1)*fy
			var r, g, b, area float64
			for sy := int(y0); sy < sh && float64(sy) < y1; sy++ {
				hy := math.Min(y1, float64(sy+1)) - math.Max(y0, float64(sy))
				for sx := int(x0); sx < sw && float64(sx) < x1; sx++ {
					wx := math.Min(x1, float64(sx+1)) - math.Max(x0, float64(sx))
					if a := wx * hy; a > 0 {
						c := flat.RGBAAt(sx, sy)
						r, g, b, area = r+a*float64(c.R), g+a*float64(c.G), b+a*float64(c.B), area+a
					}
				}
			}
			out.SetRGBA(x, y, color.RGBA{uint8(math.Round(r / area)), uint8(math.Round(g / area)), uint8(math.Round(b / area)), 255})
		}
	}
	return out
}

func TestShrinkAveragesWhatLiesUnderEachPixel(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	noise := func(w, h int) *image.NRGBA {
		img := image.NewNRGBA(image.Rect(0, 0, w, h))
		rnd.Read(img.Pix) // every colour, every alpha
		return img
	}
	cases := []struct {
		name           string
		src            image.Image
		w, h           int
		sw, sh, dw, dh int
	}{
		{name: "halved", sw: 8, sh: 6, dw: 4, dh: 3},
		{name: "uneven", sw: 13, sh: 7, dw: 5, dh: 3},
		{name: "to one pixel", sw: 9, sh: 5, dw: 1, dh: 1},
		{name: "one way only", sw: 40, sh: 90, dw: 40, dh: 31},
		{name: "barely", sw: 101, sh: 70, dw: 100, dh: 69},
		// Taller than a band, and not a whole number of them.
		{name: "several bands", sw: 150, sh: 3*shrinkRows + 11, dw: 47, dh: 29},
		{name: "a band exactly", sw: 64, sh: shrinkRows, dw: 20, dh: 10},
	}
	for _, c := range cases {
		src := image.Image(noise(c.sw, c.sh))
		check := func(name string, src image.Image) {
			got := image.NewRGBA(image.Rect(0, 0, c.dw, c.dh))
			shrink(got, src)
			want := reference(src, c.dw, c.dh)
			for i := range got.Pix {
				if d := int(got.Pix[i]) - int(want.Pix[i]); d < -1 || d > 1 {
					t.Fatalf("%s: %dx%d to %dx%d: byte %d is %d, want %d", name, c.sw, c.sh, c.dw, c.dh, i, got.Pix[i], want.Pix[i])
				}
			}
		}
		check(c.name, src)
		// The same picture as part of a larger one: bounds that do not start
		// at the origin.
		big := noise(c.sw+9, c.sh+5)
		r := image.Rect(4, 3, 4+c.sw, 3+c.sh)
		draw.Draw(big, r, src, image.Point{}, draw.Src)
		check(c.name+" (sub-image)", big.SubImage(r))
	}
}

// A photo big enough to go through shrink still comes out upright, and a
// transparent one on white.
func TestLargeRenditionsAreUprightAndOnWhite(t *testing.T) {
	photo := quadrants(4000, 3000)
	for _, orientation := range []int{1, 6, 8} {
		out, info, err := Display(photo, orientation)
		if err != nil {
			t.Fatal(err)
		}
		w, h, got := corners(t, out)
		if w != info.Width || h != info.Height || int64(w)*int64(h) > DisplayPixels || (orientation > 4) != (h > w) {
			t.Errorf("orientation %d: display is %dx%d (reported %dx%d)", orientation, w, h, info.Width, info.Height)
		}
		for i, want := range upright[orientation] {
			if !near(got[i], want) {
				t.Errorf("orientation %d: corner %d is %v, want %v", orientation, i, got[i], want)
			}
		}
	}
	blank := image.NewNRGBA(image.Rect(0, 0, 4000, 3000))
	out, _, err := Display(blank, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, got := corners(t, out); !near(got[0], color.RGBA{255, 255, 255, 255}) || !near(got[3], color.RGBA{255, 255, 255, 255}) {
		t.Errorf("a transparent image came out %v, want white", got)
	}
}

// Thumbnails of ordinary images are made as they always were; only an image
// whose scaling would not fit the budget takes the other path.
func TestOrdinaryThumbnailsKeepTheKernelScaler(t *testing.T) {
	for _, c := range []struct {
		w, h   int
		kernel bool
	}{
		{1920, 1080, true},
		{2560, 1440, true},
		{1440, 3120, true},  // a phone screenshot
		{3840, 2160, false}, // a 4K still
		{4000, 3000, false}, // a 12 MP photo
		{3000, 4000, false},
	} {
		tw, th := 640, c.h*640/c.w
		if c.h > c.w {
			tw, th = c.w*640/c.h, 640
		}
		if got := kernelFits(tw, th, c.w, c.h); got != c.kernel {
			t.Errorf("%dx%d to %dx%d: kernel scaler %v, want %v", c.w, c.h, tw, th, got, c.kernel)
		}
	}
}
