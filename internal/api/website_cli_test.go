package api

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestWebsiteCLIUploadsAndRecoversThroughArtifactAPI(t *testing.T) {
	_, agent := setup(t)
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is needed for the standalone CLI integration test")
	}
	cli, err := filepath.Abs("../../cli/finalechat")
	if err != nil {
		t.Fatal(err)
	}
	root, configRoot := t.TempDir(), t.TempDir()
	original := bytes.Repeat([]byte("{\"unknown_event\":true}\n"), 60000) // More than one chunk.
	for name, data := range map[string][]byte{"index.html": []byte("<!doctype html><style>body{color:green}</style><p>Portable session</p>"), "events.jsonl": original} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	thread := "ext:website-cli-" + uuid.NewString()
	upload := func() map[string]any {
		t.Helper()
		cmd := exec.Command(python, cli, "artifact", "upload", root, "--thread", thread, "--key", "custom-viewer", "--source", "events.jsonl", "--append-only-sources", "--json")
		cmd.Env = append(os.Environ(), "FINALECHAT_URL="+testSrv.URL, "FINALECHAT_TOKEN="+agent.token, "XDG_CONFIG_HOME="+configRoot)
		raw, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("standalone CLI upload failed: %v: %s", err, raw)
		}
		var result map[string]any
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatalf("bad CLI result: %v: %s", err, raw)
		}
		return result
	}
	first := upload()
	if str(first, "artifact_id") == "" || str(first, "revision_id") == "" {
		t.Fatal("CLI did not return durable identifiers")
	}
	if str(upload(), "revision_id") != str(first, "revision_id") {
		t.Fatal("unchanged website created an extra revision")
	}
	newSource := append(bytes.Clone(original), []byte("{\"seq\":2}\n")...)
	if err := os.WriteFile(filepath.Join(root, "events.jsonl"), newSource, 0o600); err != nil {
		t.Fatal(err)
	}
	second := upload()
	if str(second, "artifact_id") != str(first, "artifact_id") || str(second, "revision_id") == str(first, "revision_id") {
		t.Fatal("updated website did not advance the same artifact")
	}
	for _, record := range []struct {
		result map[string]any
		source []byte
	}{{first, original}, {second, newSource}} {
		path := "/api/v1/artifacts/" + str(record.result, "artifact_id") + "/revisions/" + str(record.result, "revision_id") + "/files/events.jsonl"
		status, _, raw := artifactRequest(t, agent, "GET", path, nil)
		if status != 200 || !bytes.Equal(raw, record.source) {
			t.Fatalf("native source recovery mismatch: HTTP %d, bytes=%d", status, len(raw))
		}
	}
}
