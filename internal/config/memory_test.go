package config

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

func TestMemoryLimit(t *testing.T) {
	tree := func(proc string, files map[string]string) (string, string) {
		root := t.TempDir()
		for name, content := range files {
			p := filepath.Join(root, name)
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		p := filepath.Join(t.TempDir(), "cgroup")
		if err := os.WriteFile(p, []byte(proc), 0o600); err != nil {
			t.Fatal(err)
		}
		return p, root
	}
	for _, c := range []struct {
		name  string
		proc  string
		files map[string]string
		want  int64
	}{
		{"a container", "0::/\n", map[string]string{"memory.max": "268435456\n"}, 256 << 20},
		{"a systemd unit", "0::/system.slice/finalechat.service\n", map[string]string{"system.slice/finalechat.service/memory.max": "536870912\n", "system.slice/memory.max": "max\n"}, 512 << 20},
		{"a limit further up", "0::/user.slice/a.scope\n", map[string]string{"user.slice/a.scope/memory.max": "max\n", "user.slice/memory.max": "1073741824\n"}, 1 << 30},
		{"the smaller of two", "0::/a/b\n", map[string]string{"a/b/memory.max": "1073741824\n", "a/memory.max": "268435456\n"}, 256 << 20},
		{"no limit anywhere", "0::/a/b\n", map[string]string{"a/b/memory.max": "max\n", "a/memory.max": "max\n"}, 0},
		{"cgroup v1", "12:memory:/docker/abc\n1:name=systemd:/docker/abc\n", map[string]string{"memory/memory.limit_in_bytes": "268435456\n"}, 256 << 20},
		{"a path that leaves the tree", "0::/../../etc\n", map[string]string{"memory.max": "268435456\n"}, 256 << 20},
		{"nothing to read", "", nil, 0},
	} {
		proc, root := tree(c.proc, c.files)
		if got := memoryLimit(proc, root); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
	if got := memoryLimit(filepath.Join(t.TempDir(), "missing"), t.TempDir()); got != 0 {
		t.Errorf("no cgroup files at all: got %d", got)
	}
}

func TestImageMemoryFollowsTheContainer(t *testing.T) {
	for limit, want := range map[int64]int64{
		0:         144 << 20, // not in a container: as for the 256 MiB it is sized for
		256 << 20: 144 << 20,
		512 << 20: 288 << 20,
		1 << 30:   576 << 20,
	} {
		if got := ImageMemoryFor(limit); got != want {
			t.Errorf("a %d MiB container: %d MiB for image work, want %d", limit>>20, got>>20, want>>20)
		}
	}
}
