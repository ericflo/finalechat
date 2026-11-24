package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	pngenc "image/png"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ericflo/finalechat/internal/imaging"
)

// heavyPNG is a screenshot-like picture that PNG stores badly and JPEG
// stores well: a gradient with a little grain.
func heavyPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	r := rand.New(rand.NewSource(1))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			n := r.Intn(9)
			img.SetRGBA(x, y, color.RGBA{uint8(40 + x*120/w + n), uint8(60 + y*120/h + n), uint8(90 + n), 255})
		}
	}
	var buf bytes.Buffer
	if err := pngenc.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if buf.Len() <= displayMinBytes {
		t.Fatalf("the fixture is %d bytes; it must pass %d to get a display rendition", buf.Len(), displayMinBytes)
	}
	return buf.Bytes()
}

// fetch downloads a path as the signed-in app user.
func fetch(t *testing.T, c *client, path string) (int, string, []byte) {
	t.Helper()
	res, err := c.http.Get(testSrv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header.Get("Content-Type"), data
}

func onlyAttachment(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	list, _ := sub(out, "message")["attachments"].([]any)
	if len(list) != 1 {
		t.Fatalf("expected one attachment, got %v", out)
	}
	return list[0].(map[string]any)
}

func TestDisplayRendition(t *testing.T) {
	b, a := setup(t)
	heavy := heavyPNG(t, 1600, 1200)

	status, out := postFiles(t, a.token, "/api/v1/threads/ext:display:1/messages", map[string]string{"body": "a render"}, map[string][]byte{"render.png": heavy})
	if status != http.StatusCreated {
		t.Fatalf("post: %d %v", status, out)
	}
	att := onlyAttachment(t, out)
	url := str(att, "url")
	if str(att, "display_url") != url+"/display" || att["display_width"].(float64) != 1600 || att["display_height"].(float64) != 1200 {
		t.Fatalf("a heavy image should get a display rendition at its own resolution: %v", att)
	}
	size := int(att["display_size"].(float64))
	if size == 0 || size > len(heavy)*3/4 {
		t.Fatalf("display rendition is %d bytes of a %d byte original; it is kept only when at most three quarters", size, len(heavy))
	}

	// The display path serves the compressed copy; the plain path still
	// serves the original, byte for byte.
	code, ct, data := fetch(t, b, url+"/display")
	if code != http.StatusOK || ct != "image/jpeg" || len(data) != size {
		t.Fatalf("display download: %d %s, %d bytes, want %d", code, ct, len(data), size)
	}
	if cfg, err := jpeg.DecodeConfig(bytes.NewReader(data)); err != nil || cfg.Width != 1600 || cfg.Height != 1200 {
		t.Fatalf("display rendition does not decode as 1600x1200: %v %v", cfg, err)
	}
	if code, ct, data := fetch(t, b, url); code != http.StatusOK || ct != "image/png" || !bytes.Equal(data, heavy) {
		t.Fatalf("original download: %d %s, %d bytes", code, ct, len(data))
	}

	// A light image is shown as it is: no rendition, and its display path
	// falls back to the original.
	light := makePNG(t, 900, 300)
	_, out = postFiles(t, a.token, "/api/v1/threads/ext:display:1/messages", nil, map[string][]byte{"light.png": light})
	att = onlyAttachment(t, out)
	if _, has := att["display_url"]; has || att["display_size"] != nil {
		t.Fatalf("a light image should have no display rendition: %v", att)
	}
	if code, ct, data := fetch(t, b, str(att, "url")+"/display"); code != http.StatusOK || ct != "image/png" || !bytes.Equal(data, light) {
		t.Fatalf("display path of an image without a rendition: %d %s, %d bytes", code, ct, len(data))
	}

	// An animation would lose every frame but the first.
	frame := image.NewPaletted(image.Rect(0, 0, 900, 900), color.Palette{color.Black, color.White, color.RGBA{200, 30, 30, 255}, color.RGBA{30, 30, 200, 255}})
	r := rand.New(rand.NewSource(2))
	for i := range frame.Pix {
		frame.Pix[i] = uint8(r.Intn(4))
	}
	var anim bytes.Buffer
	if err := gif.EncodeAll(&anim, &gif.GIF{Image: []*image.Paletted{frame, frame, frame, frame}, Delay: []int{10, 10, 10, 10}}); err != nil {
		t.Fatal(err)
	}
	if anim.Len() <= displayMinBytes {
		t.Fatalf("the GIF fixture is only %d bytes", anim.Len())
	}
	_, out = postFiles(t, a.token, "/api/v1/threads/ext:display:1/messages", nil, map[string][]byte{"loop.gif": anim.Bytes()})
	if att := onlyAttachment(t, out); att["display_url"] != nil || str(att, "kind") != "image" {
		t.Fatalf("a GIF should stay as it is: %v", att)
	}

	// Deleting the thread removes the rendition with the rest.
	id := url[len("/api/v1/attachments/"):]
	if _, _, _, err := testAPI.blobs.Get(context.Background(), "a/"+id+"/display.jpg"); err != nil {
		t.Fatalf("the rendition should be in blob storage: %v", err)
	}
	a.must(http.StatusOK, "DELETE", "/api/v1/threads/ext:display:1", nil)
	if _, _, _, err := testAPI.blobs.Get(context.Background(), "a/"+id+"/display.jpg"); err == nil {
		t.Fatal("the rendition should have been deleted from blob storage")
	}
}

// A phone photo is stored on its side with an EXIF note to turn it. The
// reported size and the thumbnail must be upright, as a browser shows it.
func TestUploadHonoursEXIFOrientation(t *testing.T) {
	_, a := setup(t)
	img := image.NewRGBA(image.Rect(0, 0, 240, 160))
	var plain bytes.Buffer
	if err := jpeg.Encode(&plain, img, nil); err != nil {
		t.Fatal(err)
	}
	// An EXIF block (big-endian TIFF, one entry: orientation 6) right after
	// the start-of-image marker.
	exif := []byte("Exif\x00\x00MM\x00\x2a\x00\x00\x00\x08\x00\x01\x01\x12\x00\x03\x00\x00\x00\x01\x00\x06\x00\x00\x00\x00\x00\x00")
	photo := append([]byte{0xFF, 0xD8, 0xFF, 0xE1, 0, byte(len(exif) + 2)}, exif...)
	photo = append(photo, plain.Bytes()[2:]...)

	status, out := postFiles(t, a.token, "/api/v1/threads/ext:exif:1/messages", nil, map[string][]byte{"photo.jpg": photo})
	if status != http.StatusCreated {
		t.Fatalf("post: %d %v", status, out)
	}
	att := onlyAttachment(t, out)
	if att["width"].(float64) != 160 || att["height"].(float64) != 240 {
		t.Fatalf("a photo that needs a quarter turn is 160x240 upright, got %vx%v", att["width"], att["height"])
	}
	if att["thumb_width"].(float64) != 160 || att["thumb_height"].(float64) != 240 {
		t.Fatalf("its thumbnail must be upright too, got %vx%v", att["thumb_width"], att["thumb_height"])
	}
}

func TestDisplayBackfill(t *testing.T) {
	b, a := setup(t)
	ctx := context.Background()
	heavy := heavyPNG(t, 1600, 1200)
	_, out := postFiles(t, a.token, "/api/v1/threads/ext:backfill:1/messages", nil, map[string][]byte{"old-render.png": heavy})
	heavyID := str(onlyAttachment(t, out), "id")
	_, out = postFiles(t, a.token, "/api/v1/threads/ext:backfill:1/messages", nil, map[string][]byte{"old-light.png": makePNG(t, 64, 64)})
	lightID := str(onlyAttachment(t, out), "id")

	// Make both look as they would had they been stored before display
	// renditions existed.
	if _, err := testPool.Exec(ctx, "UPDATE attachments SET display_key = NULL, display_id = '', display_width = 0, display_height = 0, display_size = 0, display_checked_at = NULL WHERE id = ANY($1)", []string{heavyID, lightID}); err != nil {
		t.Fatal(err)
	}
	_ = testAPI.blobs.Delete(ctx, "a/"+heavyID+"/display.jpg", "")
	if code, ct, _ := fetch(t, b, "/api/v1/attachments/"+heavyID+"/display"); code != http.StatusOK || ct != "image/png" {
		t.Fatalf("before the backfill the display path serves the original: %d %s", code, ct)
	}

	// Newest first, one per call, until the queue is empty.
	var saved int64
	for i := 0; ; i++ {
		found, n := testAPI.backfillDisplay(ctx)
		if !found {
			if i != 2 {
				t.Fatalf("the backfill handled %d images, want 2", i)
			}
			break
		}
		if i == 0 && n != 0 {
			t.Fatal("the newest image is the light one and gets no rendition")
		}
		saved += n
	}
	if saved < int64(len(heavy)/4) {
		t.Fatalf("the heavy image's rendition saves %d bytes of %d", saved, len(heavy))
	}
	code, ct, data := fetch(t, b, "/api/v1/attachments/"+heavyID+"/display")
	if code != http.StatusOK || ct != "image/jpeg" || int64(len(data)) != int64(len(heavy))-saved {
		t.Fatalf("after the backfill: %d %s, %d bytes", code, ct, len(data))
	}
	// The message now reports it, and the light image still has none.
	page := a.must(http.StatusOK, "GET", "/api/v1/threads/ext:backfill:1/messages", nil)
	seen := map[string]any{}
	for _, m := range page["messages"].([]any) {
		for _, it := range m.(map[string]any)["attachments"].([]any) {
			x := it.(map[string]any)
			seen[str(x, "id")] = x["display_url"]
		}
	}
	if seen[heavyID] != "/api/v1/attachments/"+heavyID+"/display" || seen[lightID] != nil {
		t.Fatalf("display urls after the backfill: %v", seen)
	}
	// Each image is decided once.
	if found, _ := testAPI.backfillDisplay(ctx); found {
		t.Fatal("nothing should be left to backfill")
	}
}

// The server works on images in a fixed amount of memory. One that needs
// more than that to decode is refused before any of it is read, with an
// error that says what the limit comes to; a file that is merely an odd
// shape is not.
func TestOversizedImagesAreRefusedAndLongOnesAreNot(t *testing.T) {
	_, a := setup(t)

	// A PNG header that claims 9000×9000 with alpha: 81 MP, 309 MiB decoded.
	huge := heavyPNG(t, 64, 64*200)
	binary.BigEndian.PutUint32(huge[16:], 9000)
	binary.BigEndian.PutUint32(huge[20:], 9000)
	huge[25] = 6
	status, out := postFiles(t, a.token, "/api/v1/threads/ext:oversized:1/messages", map[string]string{"body": "too big"}, map[string][]byte{"huge.png": huge})
	if status != http.StatusRequestEntityTooLarge || str(sub(out, "error"), "code") != "image_too_large" {
		t.Fatalf("an 81 MP PNG: got %d %v, want 413 image_too_large", status, out)
	}
	msg := str(sub(out, "error"), "message")
	for _, want := range []string{"9000×9000", "81 MP", "up to about 37 MP", "zip it"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message should say %q: %s", want, msg)
		}
	}
	// One past what any decoder here would take on is refused the same way.
	binary.BigEndian.PutUint32(huge[16:], 60000)
	binary.BigEndian.PutUint32(huge[20:], 60000)
	if status, out := postFiles(t, a.token, "/api/v1/threads/ext:oversized:1/messages", map[string]string{"body": "far too big"}, map[string][]byte{"huge.png": huge}); status != http.StatusRequestEntityTooLarge || str(sub(out, "error"), "code") != "image_too_large" {
		t.Fatalf("a 3600 MP PNG: got %d %v, want 413 image_too_large", status, out)
	}
	// Nothing of a refused message is kept.
	if status, got := a.do("GET", "/api/v1/threads/ext:oversized:1", nil); status != http.StatusNotFound {
		t.Errorf("a refused upload left a thread behind: %d %v", status, got)
	}

	// A long screenshot, a phone's width and many screens tall, is 13 MP of
	// an unusual shape. It is accepted, and its display copy keeps its width:
	// scaled to a 4K frame's worth of pixels it would be too narrow to read.
	long := heavyPNG(t, 1080, 12000)
	status, out = postFiles(t, a.token, "/api/v1/threads/ext:long:1/messages", map[string]string{"body": "a long screenshot"}, map[string][]byte{"long.png": long})
	if status != http.StatusCreated {
		t.Fatalf("a 1080×12000 screenshot: %d %v", status, out)
	}
	att := onlyAttachment(t, out)
	if att["width"].(float64) != 1080 || att["height"].(float64) != 12000 {
		t.Errorf("reported as %vx%v", att["width"], att["height"])
	}
	if att["display_width"] != float64(1080) || att["display_height"] != float64(12000) {
		t.Errorf("its display copy is %vx%v; it should be 1080x12000", att["display_width"], att["display_height"])
	}
	// Its thumbnail is of the top of it, not all of it as a sliver.
	if att["thumb_width"] != float64(256) || att["thumb_height"] != float64(640) {
		t.Errorf("its thumbnail is %vx%v, want 256x640", att["thumb_width"], att["thumb_height"])
	}
}

// Image work is let in by the memory it needs. Once the budget is spoken
// for, the next image waits, and goes ahead when room is given back.
func TestImageWorkWaitsForMemory(t *testing.T) {
	s := testAPI
	admit := func(ctx context.Context, memory, scratch int64) (func(), error) {
		return s.admitImage(ctx, imaging.Cost{Base: memory, Scratch: scratch}, 0, true)
	}
	half, err := admit(context.Background(), s.imageMemory/2, 0)
	if err != nil {
		t.Fatal(err)
	}
	rest, err := admit(context.Background(), s.imageMemory/2, 0)
	if err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	if _, err := admit(short, 1<<20, 0); err == nil {
		t.Fatal("a third image was let in with the memory all spoken for")
	}
	cancel()
	half()
	// More than there is in all is let in alone rather than never.
	whole := make(chan func(), 1)
	go func() {
		done, err := admit(context.Background(), 4*s.imageMemory, 0)
		if err == nil {
			whole <- done
		}
	}()
	select {
	case <-whole:
		t.Fatal("an image that needs everything was let in beside another")
	case <-time.After(50 * time.Millisecond):
	}
	rest()
	select {
	case done := <-whole:
		done()
	case <-time.After(5 * time.Second):
		t.Fatal("an image that needs everything was never let in")
	}

	// The disk their canvases take is shared out the same way, and an image
	// that gives up waiting for it gives its memory back.
	disk, err := admit(context.Background(), 1<<20, imageScratchBytes)
	if err != nil {
		t.Fatal(err)
	}
	short, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	if _, err := admit(short, s.imageMemory, 1<<20); err == nil {
		t.Fatal("an image was let in with the scratch disk all spoken for")
	}
	cancel()
	if s.roomForDisplay(imaging.Cost{Base: 1 << 20, Scratch: imageScratchBytes + 1}) {
		t.Error("a display copy that needs more scratch disk than there is should not be made")
	}
	disk()
	all, err := admit(context.Background(), s.imageMemory, imageScratchBytes)
	if err != nil {
		t.Fatal(err)
	}
	all()
}
