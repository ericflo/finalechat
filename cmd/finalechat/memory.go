package main

import (
	"log/slog"
	"math"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

// limitMemory tells the Go runtime how much memory the container allows.
// Left alone, the collector lets the heap double between collections, which
// for a server that has just worked on a large image is past what a small
// container has, and the kernel kills it. Told the limit, it collects
// sooner instead. GOMEMLIMIT, when set, is the operator's word and stands.
func limitMemory(log *slog.Logger) {
	if os.Getenv("GOMEMLIMIT") != "" {
		log.Info("memory limit", "go", debug.SetMemoryLimit(-1), "source", "GOMEMLIMIT")
		return
	}
	container := containerMemory("/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes")
	if container <= 0 {
		log.Info("memory limit", "container", "none")
		return
	}
	// The rest is for what the runtime does not count: the kernel's share of
	// sockets and of files being written.
	soft := container / 5 * 4
	debug.SetMemoryLimit(soft)
	log.Info("memory limit", "container", container, "go", soft)
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
