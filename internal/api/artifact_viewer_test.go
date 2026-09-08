package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"github.com/ericflo/finalechat/internal/artifact"
)

func TestArtifactViewerReplacementKeepsOriginalDataAndDownload(t *testing.T) {
	browser, agent := setup(t)
	base, original, originalFiles := artifactFixture(t)
	uploadFixture(t, base, original, originalFiles)
	first := sub(agent.must(201, "POST", base+"/revisions", map[string]any{"client_key": "original", "previous_revision_id": nil, "manifest": original}), "revision")
	firstID := str(first, "id")
	encoded, _ := json.Marshal(original)
	var latest artifact.Manifest
	_ = json.Unmarshal(encoded, &latest)
	newFiles := map[string][]byte{}
	for name, raw := range originalFiles {
		newFiles[name] = bytes.Clone(raw)
	}
	newFiles["index.html"] = []byte("<!doctype html><p>A newer renderer</p>")
	newFiles["sessions/0001.jsonl"] = append(newFiles["sessions/0001.jsonl"], []byte("{\"seq\":3}\n")...)
	for i := range latest.Files {
		f := &latest.Files[i]
		raw := newFiles[f.Path]
		f.Size = int64(len(raw))
		f.SHA256 = artifact.Digest(raw)
		f.Chunks = []artifact.Chunk{}
		if len(raw) > 0 {
			f.Chunks = append(f.Chunks, artifact.Chunk{SHA256: f.SHA256, Size: f.Size})
		}
	}
	latest.Producer.Version = "2"
	uploadFixture(t, base, latest, newFiles)
	second := sub(agent.must(201, "POST", base+"/revisions", map[string]any{"client_key": "newer", "previous_revision_id": firstID, "manifest": latest}), "revision")
	secondID := str(second, "id")
	old := base + "/revisions/" + firstID
	query := "?viewer=" + secondID
	status, _, raw := artifactRequest(t, browser, "GET", old+"/preview"+query, nil)
	if status != 200 || !bytes.Equal(raw, newFiles["index.html"]) {
		t.Fatalf("replacement preview: %d %s", status, raw)
	}
	status, _, raw = artifactRequest(t, agent, "GET", old+"/files/sessions/0001.jsonl"+query, nil)
	if status != 200 || !bytes.Equal(raw, originalFiles["sessions/0001.jsonl"]) {
		t.Fatal("replacement used the renderer's newer data")
	}
	status, _, raw = artifactRequest(t, browser, "GET", old+"/preview", nil)
	if status != 200 || !bytes.Equal(raw, originalFiles["index.html"]) {
		t.Fatal("replacement modified original preview")
	}
	browser.must(422, "GET", old+"/preview"+query+"&surface=settings", nil)
	status, _, raw = artifactRequest(t, browser, "GET", old+"/download"+query, nil)
	if status != 200 {
		t.Fatalf("combined download: %d", status)
	}
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	contents := map[string][]byte{}
	for _, file := range z.File {
		f, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		contents[file.Name], err = io.ReadAll(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(contents["sessions/0001.jsonl"], originalFiles["sessions/0001.jsonl"]) || !bytes.Equal(contents["index.html"], newFiles["index.html"]) {
		t.Fatal("combined ZIP does not preserve the selected bytes")
	}
	var manifest artifact.Manifest
	if err := json.Unmarshal(contents["manifest.json"], &manifest); err != nil || manifest.Validate() != nil || manifest.SettingsEntrypoint != "" {
		t.Fatal("invalid or authorized combined archive", err)
	}
	if manifest.Viewer["source_revision_id"] != firstID || manifest.Viewer["renderer_revision_id"] != secondID {
		t.Fatal("lost source/viewer provenance")
	}
	other, otherManifest, otherFiles := artifactFixture(t)
	uploadFixture(t, other, otherManifest, otherFiles)
	otherID := str(sub(agent.must(201, "POST", other+"/revisions", map[string]any{"client_key": "other", "previous_revision_id": nil, "manifest": otherManifest}), "revision"), "id")
	agent.must(404, "GET", old+"?viewer="+otherID, nil)
	if str(sub(agent.must(200, "GET", base, nil), "artifact"), "current_revision_id") != secondID {
		t.Fatal("reinterpretation changed current revision")
	}
}
