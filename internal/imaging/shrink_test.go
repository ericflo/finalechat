package imaging

import (
	"image"
	"image/color"
	"image/draw"
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
	for _, disk := range []bool{true, false} {
		if disk && !DiskScratch() {
			continue
		}
		scratchOnDisk(t, disk)
		// In memory, the two canvases at the display budget (one drawn, one
		// turned upright) are 64 MiB. On disk they are not the server's at all.
		limit := uint64(80 << 20)
		if disk {
			limit = 4 << 20
		}
		for name, fn := range map[string]func() ([]byte, Info, error){
			"display":   func() ([]byte, Info, error) { return Display(photo, 6, math.MaxInt) },
			"thumbnail": func() ([]byte, Info, error) { return Thumbnail(photo, 640, 6) },
		} {
			var err error
			got := allocated(func() { _, _, err = fn() })
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			t.Logf("canvases on disk %v: %s of a 16 MP photo allocated %d KiB", disk, name, got>>10)
			if got > limit {
				t.Errorf("canvases on disk %v: %s of a 16 MP photo allocated %d MiB; want at most %d", disk, name, got>>20, limit>>20)
			}
		}
	}
}

// scratchOnDisk settles, for one test, whether large canvases go to disk.
func scratchOnDisk(t *testing.T, disk bool) {
	t.Helper()
	was := DiskScratch()
	scratch.disk = disk
	t.Cleanup(func() { scratch.disk = was })
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
		out, info, err := Display(photo, orientation, math.MaxInt)
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
	out, _, err := Display(blank, 1, math.MaxInt)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, got := corners(t, out); !near(got[0], color.RGBA{255, 255, 255, 255}) || !near(got[3], color.RGBA{255, 255, 255, 255}) {
		t.Errorf("a transparent image came out %v, want white", got)
	}
}
