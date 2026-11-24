package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContainerMemory(t *testing.T) {
	dir := t.TempDir()
	file := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	missing := filepath.Join(dir, "missing")
	for _, c := range []struct {
		name  string
		paths []string
		want  int64
	}{
		{"cgroup v2", []string{file("v2", "268435456\n")}, 256 << 20},
		{"cgroup v2 without a limit", []string{file("max", "max\n")}, 0},
		{"cgroup v1", []string{missing, file("v1", "536870912\n")}, 512 << 20},
		{"cgroup v1 without a limit", []string{missing, file("v1max", "9223372036854771712\n")}, 0},
		{"not in a container", []string{missing, missing}, 0},
		{"nonsense", []string{file("bad", "lots\n")}, 0},
	} {
		if got := containerMemory(c.paths...); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}
