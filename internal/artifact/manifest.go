// Package artifact defines the portable website archive format. It has no
// database or integration-specific dependencies.
package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	Format                 = "finalechat.website/v1"
	MaxBlobBytes           = 1 << 20
	MaxManifestBytes       = 1 << 20
	MaxFileBytes     int64 = 512 << 20
	MaxRevisionBytes int64 = 2 << 30
	MaxAccountBytes  int64 = 10 << 30
	MaxFiles               = 4096
	MaxChunks              = 16384
	MaxPreviewBytes        = 8 << 20
)

type Producer struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Chunk struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type File struct {
	Path        string  `json:"path"`
	Role        string  `json:"role"`
	ContentType string  `json:"content_type"`
	Size        int64   `json:"size"`
	SHA256      string  `json:"sha256"`
	Chunks      []Chunk `json:"chunks"`
}

type Manifest struct {
	Format             string         `json:"format"`
	Producer           Producer       `json:"producer"`
	Entrypoint         string         `json:"entrypoint"`
	SettingsEntrypoint string         `json:"settings_entrypoint,omitempty"`
	Dataset            map[string]any `json:"dataset"`
	Viewer             map[string]any `json:"viewer,omitempty"`
	CapturedAt         time.Time      `json:"captured_at"`
	Files              []File         `json:"files"`
}

func Digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func ValidDigest(s string) bool {
	if len(s) != 64 || s != strings.ToLower(s) {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// ValidPath is deliberately stricter than an OS path. These names are used
// on HTTP, in ZIPs and on several operating systems without reinterpretation.
func ValidPath(s string) bool {
	if s == "" || len(s) > 512 || !utf8.ValidString(s) || path.Clean(s) != s || strings.HasPrefix(s, "/") || s == "." || strings.ContainsAny(s, "\\:%?#\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == ".." || part == "" || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
		for _, c := range part {
			if c < 32 || c == 127 {
				return false
			}
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
			return false
		}
	}
	return true
}

func (m Manifest) Find(name string) (File, bool) {
	for _, f := range m.Files {
		if f.Path == name {
			return f, true
		}
	}
	return File{}, false
}

// Validate checks the portable structure. Uploads separately verify chunk
// digests; readers verify each reconstructed file against its declared digest.
func (m Manifest) Validate() error {
	if m.Format != Format {
		return fmt.Errorf("unsupported artifact format")
	}
	if m.Producer.Name == "" || len(m.Producer.Name) > 120 || len(m.Producer.Version) > 200 {
		return fmt.Errorf("producer name/version is invalid")
	}
	if m.CapturedAt.IsZero() {
		return fmt.Errorf("captured_at is required")
	}
	if len(m.Files) == 0 || len(m.Files) > MaxFiles {
		return fmt.Errorf("files must contain 1 to %d entries", MaxFiles)
	}
	if m.Dataset == nil {
		return fmt.Errorf("dataset is required")
	}
	names := map[string]bool{}
	chunks := map[string]int64{}
	var total int64
	count := 0
	for _, f := range m.Files {
		if !ValidPath(f.Path) || strings.EqualFold(f.Path, "manifest.json") {
			return fmt.Errorf("invalid or reserved file path %q", f.Path)
		}
		key := strings.ToLower(f.Path)
		if names[key] {
			return fmt.Errorf("duplicate file path %q", f.Path)
		}
		names[key] = true
		if f.Size < 0 || f.Size > MaxFileBytes || !ValidDigest(f.SHA256) {
			return fmt.Errorf("invalid size or hash for %s", f.Path)
		}
		if f.ContentType == "" || len(f.ContentType) > 200 || strings.ContainsAny(f.ContentType, "\r\n") {
			return fmt.Errorf("invalid content type for %s", f.Path)
		}
		switch f.Role {
		case "viewer", "source", "asset", "derived", "context":
		default:
			return fmt.Errorf("invalid role for %s", f.Path)
		}
		var size int64
		for _, c := range f.Chunks {
			count++
			if count > MaxChunks || c.Size < 1 || c.Size > MaxBlobBytes || !ValidDigest(c.SHA256) {
				return fmt.Errorf("invalid chunk for %s", f.Path)
			}
			if n, ok := chunks[c.SHA256]; ok && n != c.Size {
				return fmt.Errorf("conflicting chunk lengths")
			}
			chunks[c.SHA256] = c.Size
			size += c.Size
		}
		if size != f.Size {
			return fmt.Errorf("chunk lengths do not match %s", f.Path)
		}
		if size == 0 && f.SHA256 != Digest(nil) {
			return fmt.Errorf("invalid empty-file hash")
		}
		total += size
		if total > MaxRevisionBytes {
			return fmt.Errorf("revision exceeds %d bytes", MaxRevisionBytes)
		}
	}
	// A file cannot also be a directory; case-fold to keep exports portable.
	for name := range names {
		for dir := path.Dir(name); dir != "."; dir = path.Dir(dir) {
			if names[dir] {
				return fmt.Errorf("file/directory collision at %s", dir)
			}
		}
	}
	for i, entry := range []string{m.Entrypoint, m.SettingsEntrypoint} {
		if i == 1 && entry == "" {
			continue
		}
		f, ok := m.Find(entry)
		if !ok || f.Role != "viewer" || strings.SplitN(f.ContentType, ";", 2)[0] != "text/html" || f.Size > MaxPreviewBytes {
			return fmt.Errorf("entrypoint must name an HTML viewer of at most %d bytes", MaxPreviewBytes)
		}
	}
	raw, err := json.Marshal(m)
	if err != nil || len(raw) > MaxManifestBytes {
		return fmt.Errorf("manifest is invalid or too large")
	}
	return nil
}

func (m Manifest) Blobs() map[string]int64 {
	out := map[string]int64{}
	for _, f := range m.Files {
		for _, c := range f.Chunks {
			out[c.SHA256] = c.Size
		}
	}
	return out
}
