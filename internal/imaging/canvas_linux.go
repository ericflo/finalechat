package imaging

import (
	"fmt"
	"os"
	"syscall"
)

// File systems that keep their files in memory.
const (
	tmpfsMagic = 0x01021994
	ramfsMagic = 0x858458f6
)

func probeScratch() bool {
	var st syscall.Statfs_t
	if err := syscall.Statfs(os.TempDir(), &st); err != nil || st.Type == tmpfsMagic || st.Type == ramfsMagic {
		return false
	}
	_, free, err := mapScratch(os.Getpagesize())
	if err != nil {
		return false
	}
	free()
	return true
}

// mapScratch maps size bytes of a new scratch file, zeroed, and returns them
// with the function that unmaps them. The file has no name by the time this
// returns, so it is gone when it is unmapped, or if the server dies first.
func mapScratch(size int) ([]byte, func(), error) {
	f, err := os.CreateTemp("", "finalechat-canvas-")
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	_ = os.Remove(f.Name())
	// Claim the blocks now. A mapped file is otherwise filled in as it is
	// written, and a full disk then arrives as a fatal signal in the middle
	// of drawing instead of as an error here.
	if err := syscall.Fallocate(int(f.Fd()), 0, 0, int64(size)); err != nil {
		return nil, nil, fmt.Errorf("reserve %d bytes of scratch space: %w", size, err)
	}
	pix, err := syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return nil, nil, fmt.Errorf("map %d bytes of scratch space: %w", size, err)
	}
	return pix, func() { _ = syscall.Munmap(pix) }, nil
}
