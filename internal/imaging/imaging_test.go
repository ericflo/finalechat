package imaging

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"strings"
	"testing"
)

// pngHeaderWithSize builds a minimal PNG whose IHDR declares w×h. DecodeConfig
// reads only this header, so tests can exercise multi-megapixel configs
// without allocating the pixels.
func pngHeaderWithSize(w, h uint32) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{137, 80, 78, 71, 13, 10, 26, 10})
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], w)
	binary.BigEndian.PutUint32(ihdr[4:8], h)
	ihdr[8] = 8 // bit depth
	ihdr[9] = 2 // truecolor
	// compression, filter, interlace remain 0.
	var chunk bytes.Buffer
	chunk.WriteString("IHDR")
	chunk.Write(ihdr)
	binary.Write(&buf, binary.BigEndian, uint32(13))
	buf.Write(chunk.Bytes())
	binary.Write(&buf, binary.BigEndian, crc32.ChecksumIEEE(chunk.Bytes()))
	return buf.Bytes()
}

func TestCheckDimensions(t *testing.T) {
	cases := []struct {
		name    string
		w, h    int
		wantErr bool
	}{
		{"small passes", 640, 480, false},
		{"1x1 passes", 1, 1, false},
		{"50MP passes", 8000, 6250, false},
		{"300MP boundary passes", 20000, 15000, false},
		{"just over 300MP rejected", 20001, 15000, true},
		{"400MP rejected", 20000, 20000, true},
		{"zero width rejected", 0, 100, true},
		{"zero height rejected", 100, 0, true},
		{"negative rejected", -10, 100, true},
		{"both negative rejected", -5, -5, true},
		{"overflow-safe rejected", 1 << 30, 1 << 30, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkDimensions(tc.w, tc.h)
			if (err != nil) != tc.wantErr {
				t.Fatalf("checkDimensions(%d,%d) err=%v, wantErr=%v", tc.w, tc.h, err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "image dimensions are out of range") {
				t.Fatalf("error %q must keep substring %q", err.Error(), "image dimensions are out of range")
			}
		})
	}
}

func TestCheckDimensionsErrorMessage(t *testing.T) {
	err := checkDimensions(20000, 20000)
	if err == nil {
		t.Fatal("checkDimensions(20000,20000) = nil, want error")
	}
	msg := err.Error()
	for _, want := range []string{"20000x20000", "400.0 MP", "300 MP"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q must contain %q", msg, want)
		}
	}
}

func TestDecodeSmallPNGPasses(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	got, info, err := Decode("image/png", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 4 || info.Height != 3 {
		t.Fatalf("info=%+v, want 4x3", info)
	}
	if got == nil {
		t.Fatal("nil image")
	}
}

func TestDecode72MPHeaderNotRejectedByDimensions(t *testing.T) {
	// 9000×8000 = 72 MP is under the 300 MP limit, so it must NOT fail
	// with the dimensions error. Full decode of a truncated header fails
	// at the pixel stage, but with a different error.
	data := pngHeaderWithSize(9000, 8000)
	_, _, err := Decode("image/png", data)
	if err != nil && strings.Contains(err.Error(), "image dimensions are out of range") {
		t.Fatalf("72 MP header wrongly rejected by dimensions check: %v", err)
	}
}

func TestDecode400MPHeaderRejected(t *testing.T) {
	// 20000×20000 = 400 MP exceeds the 300 MP limit, so Decode must fail
	// with the dimensions error before touching pixel data.
	data := pngHeaderWithSize(20000, 20000)
	_, _, err := Decode("image/png", data)
	if err == nil {
		t.Fatal("Decode(400MP header) = nil, want dimensions error")
	}
	if !strings.Contains(err.Error(), "image dimensions are out of range") {
		t.Fatalf("error %q must contain %q", err.Error(), "image dimensions are out of range")
	}
}
