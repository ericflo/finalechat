package artifact

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func sampleManifest() Manifest {
	b := []byte("<!doctype html><p>Archive</p>")
	return Manifest{Format: Format, Producer: Producer{Name: "test", Version: "1"}, Entrypoint: "index.html", Dataset: map[string]any{"format": "test.jsonl/v1"}, CapturedAt: time.Now().UTC(), Files: []File{{Path: "index.html", Role: "viewer", ContentType: "text/html", Size: int64(len(b)), SHA256: Digest(b), Chunks: []Chunk{{SHA256: Digest(b), Size: int64(len(b))}}}}}
}

func TestPortablePaths(t *testing.T) {
	for _, s := range []string{"index.html", "sessions/0001.jsonl", "assets/猫.png", "settings/index.html"} {
		if !ValidPath(s) {
			t.Errorf("rejected %q", s)
		}
	}
	for _, s := range []string{"", ".", "../x", "a/../b", "/x", "a//b", "x/", "C:x", "a\\b", "a%2fb", "a?b", "a#b", "a\nb", "NUL.json", "com1/x", "x/aux", "x. ", "a/../index.html", "x\x00"} {
		if ValidPath(s) {
			t.Errorf("accepted unsafe %q", s)
		}
	}
}

func TestManifestValidation(t *testing.T) {
	cases := map[string]func(*Manifest){
		"unknown format":      func(m *Manifest) { m.Format = "future" },
		"missing dataset":     func(m *Manifest) { m.Dataset = nil },
		"missing entry":       func(m *Manifest) { m.Entrypoint = "" },
		"missing settings":    func(m *Manifest) { m.SettingsEntrypoint = "settings.html" },
		"hash":                func(m *Manifest) { m.Files[0].SHA256 = strings.Repeat("Z", 64) },
		"length":              func(m *Manifest) { m.Files[0].Size++ },
		"oversized chunk":     func(m *Manifest) { m.Files[0].Chunks[0].Size = MaxBlobBytes + 1 },
		"header injection":    func(m *Manifest) { m.Files[0].ContentType = "text/html\r\nX: y" },
		"case collision":      func(m *Manifest) { f := m.Files[0]; f.Path = "INDEX.html"; m.Files = append(m.Files, f) },
		"directory collision": func(m *Manifest) { f := m.Files[0]; f.Path = "INDEX.html/file"; m.Files = append(m.Files, f) },
		"reserved manifest":   func(m *Manifest) { m.Files[0].Path = "manifest.json"; m.Entrypoint = "manifest.json" },
	}
	if err := sampleManifest().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			m := sampleManifest()
			change(&m)
			if m.Validate() == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
	m := sampleManifest()
	m.Files = append(m.Files, File{Path: "empty.jsonl", Role: "source", ContentType: "application/x-ndjson", SHA256: Digest(nil), Chunks: []Chunk{}})
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(m)
	var copy Manifest
	if err := json.Unmarshal(raw, &copy); err != nil {
		t.Fatal(err)
	}
	if err := copy.Validate(); err != nil {
		t.Fatal(err)
	}
}

func FuzzPortablePath(f *testing.F) {
	for _, s := range []string{"index.html", "../x", "a\\b", "NUL", "a/b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if ValidPath(s) && (strings.HasPrefix(s, "/") || strings.Contains(s, "\\") || strings.Contains(s, "\x00")) {
			t.Fatal("unsafe portable path")
		}
	})
}
