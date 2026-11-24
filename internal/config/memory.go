package config

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// nominalMemory is the container the server is sized for, and what it
// assumes of itself when it is not in one.
const nominalMemory = 256 << 20

// ImageMemoryFor is how much of a container's memory decoding images and
// rendering their thumbnails and display copies may hold at once: a little
// over half, which leaves the rest for the uploads themselves and for
// everything else the server is doing. An image that needs more than this
// to decode is refused, so a larger container accepts larger images.
func ImageMemoryFor(container int64) int64 {
	if container <= 0 {
		container = nominalMemory
	}
	return container / 16 * 9
}

// memoryLimit is how much memory this process may use, in bytes: the
// smallest limit on its cgroup or on any above it, 0 when there is none. In
// a container the process's cgroup is the root of what it can see (root);
// run as a systemd unit it is some way down, where proc (/proc/self/cgroup)
// says.
func memoryLimit(proc, root string) int64 {
	var limit int64
	take := func(n int64) {
		if n > 0 && (limit == 0 || n < limit) {
			limit = n
		}
	}
	raw, _ := os.ReadFile(proc)
	for _, line := range strings.Split(string(raw), "\n") {
		// cgroup v2 has the one line, "0::/path".
		path, ok := strings.CutPrefix(line, "0::")
		if !ok || strings.Contains(path, "..") {
			continue
		}
		for dir := filepath.Join(root, path); len(dir) > len(root); dir = filepath.Dir(dir) {
			take(containerMemory(filepath.Join(dir, "memory.max")))
		}
	}
	take(containerMemory(filepath.Join(root, "memory.max"), filepath.Join(root, "memory", "memory.limit_in_bytes")))
	return limit
}

// containerMemory reads the first cgroup memory limit there is (v2, then
// v1), in bytes; 0 when there is none or it is unlimited.
func containerMemory(paths ...string) int64 {
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		// v2 says "max" for no limit; v1 a number near the top of the range.
		if err != nil || n <= 0 || n >= math.MaxInt64/2 {
			return 0
		}
		return n
	}
	return 0
}
