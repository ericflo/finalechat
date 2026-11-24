//go:build !linux

package imaging

import "errors"

// Disk scratch needs fallocate to be safe, which is Linux's; elsewhere
// canvases stay in memory.
func probeScratch() bool { return false }

func mapScratch(int) ([]byte, func(), error) {
	return nil, nil, errors.New("no disk scratch on this platform")
}
