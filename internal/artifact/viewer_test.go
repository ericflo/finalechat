package artifact

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReinterpretKeepsDatasetAndRejectsViewerCollisions(t *testing.T) {
	data := sampleManifest()
	data.Dataset["session_id"] = "old-data"
	data.Files = append(data.Files, File{Path: "session.jsonl", Role: "source", ContentType: "application/x-ndjson", SHA256: Digest(nil), Chunks: []Chunk{}})
	renderer := sampleManifest()
	renderer.Entrypoint = "new.html"
	renderer.Files[0].Path = "new.html"
	renderer.Producer.Version = "2"
	before, _ := json.Marshal(data)
	out, err := Reinterpret(data, renderer, "old", "new")
	if err != nil {
		t.Fatal(err)
	}
	if out.Entrypoint != "new.html" || out.SettingsEntrypoint != "" || out.Dataset["session_id"] != "old-data" {
		t.Fatal("replaced dataset metadata or retained settings authority")
	}
	if _, ok := out.Find("session.jsonl"); !ok {
		t.Fatal("source file disappeared")
	}
	if _, ok := out.Find("index.html"); ok {
		t.Fatal("retained the superseded viewer")
	}
	after, _ := json.Marshal(data)
	if string(before) != string(after) {
		t.Fatal("mutated immutable source manifest")
	}
	renderer.Entrypoint = "session.jsonl"
	renderer.Files[0].Path = "session.jsonl"
	if _, err := Reinterpret(data, renderer, "old", "new"); err == nil || !strings.Contains(err.Error(), "combined") {
		t.Fatalf("viewer overwrote a native source: %v", err)
	}
	renderer.Dataset["format"] = "different/v1"
	if _, err := Reinterpret(data, renderer, "old", "new"); err == nil {
		t.Fatal("incompatible viewer accepted")
	}
}
