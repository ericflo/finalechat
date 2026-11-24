package imaging

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

var (
	red    = color.RGBA{220, 30, 30, 255}
	green  = color.RGBA{30, 160, 30, 255}
	blue   = color.RGBA{30, 30, 220, 255}
	yellow = color.RGBA{230, 200, 20, 255}
)

// quadrants paints red top left, green top right, blue bottom left and
// yellow bottom right, so every orientation is told apart by its corners.
func quadrants(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := red
			switch {
			case x >= w/2 && y < h/2:
				c = green
			case x < w/2 && y >= h/2:
				c = blue
			case x >= w/2 && y >= h/2:
				c = yellow
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// withOrientation splices an EXIF block carrying the orientation tag into a
// JPEG, right after its start-of-image marker.
func withOrientation(t *testing.T, jpg []byte, order binary.ByteOrder, orientation int) []byte {
	t.Helper()
	tiff := make([]byte, 8+2+12+4)
	if order == binary.LittleEndian {
		copy(tiff, "II")
	} else {
		copy(tiff, "MM")
	}
	order.PutUint16(tiff[2:], 42)
	order.PutUint32(tiff[4:], 8)       // first IFD
	order.PutUint16(tiff[8:], 1)       // one entry
	order.PutUint16(tiff[10:], 0x0112) // orientation
	order.PutUint16(tiff[12:], 3)      // SHORT
	order.PutUint32(tiff[14:], 1)      // count
	order.PutUint16(tiff[18:], uint16(orientation))
	seg := append([]byte("Exif\x00\x00"), tiff...)
	out := []byte{0xFF, 0xD8, 0xFF, 0xE1, byte((len(seg) + 2) >> 8), byte(len(seg) + 2)}
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

func near(a, b color.RGBA) bool {
	d := func(x, y uint8) int {
		if x > y {
			return int(x - y)
		}
		return int(y - x)
	}
	return d(a.R, b.R) < 40 && d(a.G, b.G) < 40 && d(a.B, b.B) < 40
}

// corners reads the colour a little inside each corner: top left, top right,
// bottom left, bottom right.
func corners(t *testing.T, jpg []byte) (w, h int, c [4]color.RGBA) {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(jpg))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	w, h = b.Dx(), b.Dy()
	at := func(x, y int) color.RGBA { return color.RGBAModel.Convert(img.At(x, y)).(color.RGBA) }
	return w, h, [4]color.RGBA{at(w/8, h/8), at(w-1-w/8, h/8), at(w/8, h-1-h/8), at(w-1-w/8, h-1-h/8)}
}

// What a viewer shows for each EXIF orientation of the quadrant picture
// (checked against Pillow's exif_transpose).
var upright = map[int][4]color.RGBA{
	1: {red, green, blue, yellow},
	2: {green, red, yellow, blue},
	3: {yellow, blue, green, red},
	4: {blue, yellow, red, green},
	5: {red, blue, green, yellow},
	6: {blue, red, yellow, green},
	7: {yellow, green, blue, red},
	8: {green, yellow, red, blue},
}

func TestRenditionsAreUpright(t *testing.T) {
	var plain bytes.Buffer
	if err := jpeg.Encode(&plain, quadrants(240, 160), &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	for orientation, want := range upright {
		for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
			data := withOrientation(t, plain.Bytes(), order, orientation)
			if got := Orientation("image/jpeg", data); got != orientation {
				t.Fatalf("orientation %d (%v) read as %d", orientation, order, got)
			}
			img, _, err := Decode("image/jpeg", data)
			if err != nil {
				t.Fatalf("an EXIF block must not break decoding: %v", err)
			}
			wantW, wantH := 240, 160
			if orientation >= 5 {
				wantW, wantH = 160, 240
			}
			display, info, err := Display(img, orientation)
			if err != nil {
				t.Fatal(err)
			}
			if w, h, got := corners(t, display); w != wantW || h != wantH || info.Width != wantW || info.Height != wantH {
				t.Fatalf("orientation %d: display is %dx%d (info %dx%d), want %dx%d", orientation, w, h, info.Width, info.Height, wantW, wantH)
			} else {
				for i := range want {
					if !near(got[i], want[i]) {
						t.Fatalf("orientation %d: display corner %d is %v, want %v", orientation, i, got[i], want[i])
					}
				}
			}
			thumb, tinfo, err := Thumbnail(img, 64, orientation)
			if err != nil {
				t.Fatal(err)
			}
			w, h, got := corners(t, thumb)
			if max(w, h) != 64 || (w > h) != (wantW > wantH) || tinfo.Width != w || tinfo.Height != h {
				t.Fatalf("orientation %d: thumbnail is %dx%d (info %dx%d)", orientation, w, h, tinfo.Width, tinfo.Height)
			}
			for i := range want {
				if !near(got[i], want[i]) {
					t.Fatalf("orientation %d: thumbnail corner %d is %v, want %v", orientation, i, got[i], want[i])
				}
			}
		}
	}
}

func TestOrientationIgnoresWhatItCannotRead(t *testing.T) {
	var plain bytes.Buffer
	_ = jpeg.Encode(&plain, quadrants(16, 16), nil)
	tagged := withOrientation(t, plain.Bytes(), binary.BigEndian, 6)
	cases := map[string][]byte{
		"no EXIF":          plain.Bytes(),
		"empty":            nil,
		"not a JPEG":       []byte("\x89PNG\r\n\x1a\n"),
		"cut in the block": tagged[:20],
		"cut in the tag":   tagged[:len(tagged)-plain.Len()-2],
	}
	for name, data := range cases {
		if got := Orientation("image/jpeg", data); got != 1 {
			t.Fatalf("%s: got %d, want 1", name, got)
		}
	}
	if got := Orientation("image/png", tagged); got != 1 {
		t.Fatalf("only JPEG carries the tag here; got %d", got)
	}
	// A value outside 1-8 is not an orientation.
	if got := Orientation("image/jpeg", withOrientation(t, plain.Bytes(), binary.LittleEndian, 9)); got != 1 {
		t.Fatalf("orientation 9: got %d, want 1", got)
	}
}

func TestDisplayKeepsResolutionUpToItsBudget(t *testing.T) {
	// A 4K screenshot keeps every pixel.
	if _, info, err := Display(image.NewGray(image.Rect(0, 0, 3840, 2160)), 1); err != nil || info.Width != 3840 || info.Height != 2160 {
		t.Fatalf("4K: got %dx%d, %v", info.Width, info.Height, err)
	}
	// A 20 MP photo is scaled to the budget, keeping its shape.
	_, info, err := Display(image.NewGray(image.Rect(0, 0, 5200, 3900)), 1)
	if err != nil {
		t.Fatal(err)
	}
	if px := info.Width * info.Height; px > DisplayPixels || px < DisplayPixels*98/100 {
		t.Fatalf("20 MP: got %dx%d (%d px), want just under %d", info.Width, info.Height, px, DisplayPixels)
	}
	if got := info.Width * 3 / 4; got < info.Height-2 || got > info.Height+2 {
		t.Fatalf("20 MP: %dx%d is no longer 4:3", info.Width, info.Height)
	}
}

func TestDisplayPutsTransparencyOnWhite(t *testing.T) {
	// Fully transparent pixels would be black in a JPEG without a backdrop.
	out, _, err := Display(image.NewNRGBA(image.Rect(0, 0, 32, 32)), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, got := corners(t, out); !near(got[0], color.RGBA{255, 255, 255, 255}) {
		t.Fatalf("transparent pixels came out %v, want white", got[0])
	}
}
