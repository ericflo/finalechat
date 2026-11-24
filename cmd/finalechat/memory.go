package main

import (
	"log/slog"
	"os"
	"runtime/debug"
)

// limitMemory tells the Go runtime how much memory the container allows.
// Left alone, the collector lets the heap double between collections, which
// for a server that has just worked on a large image is past what a small
// container has, and the kernel kills it. Told the limit, it collects
// sooner instead. GOMEMLIMIT, when set, is the operator's word and stands.
func limitMemory(container int64, log *slog.Logger) {
	if os.Getenv("GOMEMLIMIT") != "" {
		log.Info("memory limit", "go", debug.SetMemoryLimit(-1), "source", "GOMEMLIMIT")
		return
	}
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
