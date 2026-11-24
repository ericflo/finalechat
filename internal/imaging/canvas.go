package imaging

import (
	"image"
	"sync"
)

// scratchMin is the size from which a canvas is kept on disk rather than in
// memory, when there is a disk to keep it on.
const scratchMin = 4 << 20

// A canvas is the RGBA image a rendition is drawn on. A large one lives in a
// scratch file mapped into memory: the kernel pages it out to disk as it
// needs to, so a canvas far larger than the server's memory is drawn and
// read back without the server holding it. free gives the memory back and
// must be called once the canvas is no longer read.
type canvas struct {
	*image.RGBA
	free func()
}

// newCanvas makes a w×h canvas. When a large one cannot be put on disk
// although the disk was there at start, that is an error rather than a
// quiet return to memory: the caller budgeted for a canvas on disk.
func newCanvas(w, h int) (*canvas, error) {
	r := image.Rect(0, 0, w, h)
	size := w * h * 4
	if size < scratchMin || !DiskScratch() {
		return &canvas{RGBA: image.NewRGBA(r), free: func() {}}, nil
	}
	pix, free, err := mapScratch(size)
	if err != nil {
		return nil, err
	}
	return &canvas{RGBA: &image.RGBA{Pix: pix, Stride: w * 4, Rect: r}, free: free}, nil
}

var scratch struct {
	once sync.Once
	disk bool
}

// DiskScratch reports whether large canvases are kept on disk: whether the
// temp directory is writable, on a real disk (a file on tmpfs is memory by
// another name), and can be mapped. It is settled on first use.
func DiskScratch() bool {
	scratch.once.Do(func() { scratch.disk = probeScratch() })
	return scratch.disk
}
