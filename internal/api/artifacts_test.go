package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericflo/finalechat/internal/artifact"
	"github.com/ericflo/finalechat/internal/auth"
	"github.com/ericflo/finalechat/internal/blob"
	"github.com/ericflo/finalechat/internal/store"
	"github.com/google/uuid"
)

func artifactFixture(t *testing.T) (string, artifact.Manifest, map[string][]byte) {
	t.Helper()
	_, a := setup(t)
	out := a.must(200, "PUT", "/api/v1/threads/ext:artifact-"+uuid.NewString()+"/artifacts/session", map[string]any{"title": "Session archive"})
	base := "/api/v1/artifacts/" + str(sub(out, "artifact"), "id")
	files := map[string][]byte{"index.html": []byte("<!doctype html><script>document.title='archive'</script><p>Viewer</p>"), "settings/index.html": []byte("<!doctype html><p>Settings</p>"), "sessions/0001.jsonl": []byte("{\"seq\":1,\"text\":\"猫\"}\n{\"seq\":2}\n"), "empty.jsonl": {}}
	// Chunks deduplicate across this owner's artifacts. Give each fixture a
	// fresh chunk so missing-upload checks do not depend on test order.
	files["index.html"] = append(files["index.html"], []byte("<!--"+uuid.NewString()+"-->")...)
	m := artifact.Manifest{Format: artifact.Format, Producer: artifact.Producer{Name: "test", Version: "1"}, Entrypoint: "index.html", SettingsEntrypoint: "settings/index.html", CapturedAt: time.Now().UTC(), Dataset: map[string]any{"format": "test.jsonl/v1", "session_id": "sample"}, Files: []artifact.File{}}
	for _, p := range []string{"index.html", "settings/index.html", "sessions/0001.jsonl", "empty.jsonl"} {
		b := files[p]
		role, ct := "source", "application/x-ndjson"
		if strings.HasSuffix(p, ".html") {
			role, ct = "viewer", "text/html"
		}
		f := artifact.File{Path: p, Role: role, ContentType: ct, Size: int64(len(b)), SHA256: artifact.Digest(b), Chunks: []artifact.Chunk{}}
		// Deliberately split JSON records and UTF-8 across chunk boundaries.
		for i := 0; i < len(b); i += 7 {
			end := min(i+7, len(b))
			part := b[i:end]
			f.Chunks = append(f.Chunks, artifact.Chunk{SHA256: artifact.Digest(part), Size: int64(len(part))})
		}
		m.Files = append(m.Files, f)
	}
	return base, m, files
}

func artifactRequest(t *testing.T, c *client, method, path string, body []byte) (int, http.Header, []byte) {
	t.Helper()
	r, err := http.NewRequest(method, testSrv.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if c.token != "" {
		r.Header.Set("Authorization", "Bearer "+c.token)
	} else {
		r.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	r.Header.Set("Content-Type", "application/octet-stream")
	res, err := c.http.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, res.Header, raw
}

func uploadFixture(t *testing.T, base string, m artifact.Manifest, files map[string][]byte) {
	t.Helper()
	_, a := setup(t)
	for _, f := range m.Files {
		offset := int64(0)
		for _, c := range f.Chunks {
			status, _, raw := artifactRequest(t, a, "PUT", base+"/blobs/"+c.SHA256, files[f.Path][offset:offset+c.Size])
			if status != 200 && status != 201 {
				t.Fatalf("upload: %d %s", status, raw)
			}
			offset += c.Size
		}
	}
}

func TestArtifactPublicationAndPortableDownload(t *testing.T) {
	b, a := setup(t)
	base, m, files := artifactFixture(t)
	in := map[string]any{"manifest": m, "client_key": "one", "previous_revision_id": nil}
	a.must(404, "POST", base+"/revisions", in) // Missing blobs cannot replace current.
	if sub(a.must(200, "GET", base, nil), "artifact")["current_revision_id"] != nil {
		t.Fatal("failed publish advanced current")
	}
	uploadFixture(t, base, m, files)
	first := a.must(201, "POST", base+"/revisions", in)
	id := str(sub(first, "revision"), "id")
	retry := a.must(200, "POST", base+"/revisions", in)
	if str(sub(retry, "revision"), "id") != id {
		t.Fatal("retry created a revision")
	}
	stale := map[string]any{"manifest": m, "client_key": "two", "previous_revision_id": nil}
	a.must(409, "POST", base+"/revisions", stale)
	stale["previous_revision_id"] = id
	second := a.must(201, "POST", base+"/revisions", stale)
	// Even after a later publication, the old request's retry is idempotent.
	a.must(200, "POST", base+"/revisions", in)
	current := sub(a.must(200, "GET", base, nil), "artifact")
	if current["current_revision_id"] != sub(second, "revision")["id"] {
		t.Fatal("retry rewound current")
	}
	m.Producer.Version = "different"
	in["manifest"] = m
	a.must(409, "POST", base+"/revisions", in)
	old := base + "/revisions/" + id
	for p, want := range files {
		status, h, got := artifactRequest(t, a, "GET", old+"/files/"+p, nil)
		if status != 200 || !bytes.Equal(got, want) {
			t.Fatalf("%s: %d %q", p, status, got)
		}
		if !strings.HasPrefix(h.Get("Content-Disposition"), "attachment;") {
			t.Fatal("raw HTML was not inert")
		}
	}
	status, _, part := artifactRequest(t, a, "GET", old+"/files/sessions/0001.jsonl?offset=3&length=21", nil)
	if status != 200 || !bytes.Equal(part, files["sessions/0001.jsonl"][3:24]) {
		t.Fatal("range reconstruction changed bytes")
	}
	a.must(422, "GET", old+"/files/index.html?offset=-1", nil)
	a.must(403, "GET", old+"/preview", nil)
	status, h, raw := artifactRequest(t, b, "GET", old+"/preview", nil)
	if status != 200 || !bytes.Equal(raw, files["index.html"]) {
		t.Fatalf("preview %d", status)
	}
	csp := h.Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox allow-scripts;") || strings.Contains(csp, "allow-same-origin") || !strings.Contains(csp, "connect-src 'none'") {
		t.Fatalf("unsafe CSP %s", csp)
	}
	status, _, raw = artifactRequest(t, b, "GET", old+"/preview?surface=settings", nil)
	if status != 200 || !bytes.Equal(raw, files["settings/index.html"]) {
		t.Fatal("wrong settings entrypoint")
	}
	status, _, raw = artifactRequest(t, a, "GET", old+"/download", nil)
	if status != 200 {
		t.Fatal(status)
	}
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if len(z.File) != len(files)+1 {
		t.Fatal("incomplete ZIP")
	}
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if f.Name == "manifest.json" {
			var manifest artifact.Manifest
			if json.Unmarshal(got, &manifest) != nil || manifest.Validate() != nil {
				t.Fatal("invalid archived manifest")
			}
		} else if !bytes.Equal(got, files[f.Name]) {
			t.Fatalf("archive changed %s", f.Name)
		}
	}
	a.must(403, "DELETE", base, nil)
	b.must(200, "DELETE", base, nil)
	a.must(404, "GET", old, nil)
}

func TestArtifactOwnershipHashAndConcurrency(t *testing.T) {
	_, a := setup(t)
	base, m, files := artifactFixture(t)
	status, _, _ := artifactRequest(t, a, "PUT", base+"/blobs/"+artifact.Digest([]byte("right")), []byte("wrong"))
	if status != 422 {
		t.Fatal(status)
	}
	status, _, _ = artifactRequest(t, a, "PUT", base+"/blobs/"+artifact.Digest([]byte("x")), bytes.Repeat([]byte("x"), artifact.MaxBlobBytes+1))
	if status != 413 {
		t.Fatal(status)
	}
	hash, err := auth.HashPassword("other-password-long")
	if err != nil {
		t.Fatal(err)
	}
	u, err := testAPI.store.CreateUser(context.Background(), uuid.NewString()+"@test.invalid", hash, "Other")
	if err != nil {
		t.Fatal(err)
	}
	other := newBrowser(t)
	other.must(200, "POST", "/api/v1/auth/login", map[string]any{"email": u.Email, "password": "other-password-long"})
	other.must(404, "GET", base, nil)
	other.must(404, "POST", base+"/blobs/check", map[string]any{"hashes": []string{artifact.Digest([]byte("right"))}})
	uploadFixture(t, base, m, files)
	missing, err := testAPI.store.MissingArtifactBlobs(context.Background(), u.ID, []string{m.Files[0].Chunks[0].SHA256})
	if err != nil || len(missing) != 1 {
		t.Fatal("cross-user dedup leaked existence", err)
	}
	id := uuid.MustParse(strings.TrimPrefix(base, "/api/v1/artifacts/"))
	me := a.must(200, "GET", "/api/v1/me", nil)
	userID := uuid.MustParse(str(sub(me, "user"), "id"))
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, key := range []string{"writer-one", "writer-two"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			_, _, err := testAPI.store.CommitArtifactRevision(context.Background(), userID, id, nil, key, m)
			results <- err
		}(key)
	}
	wg.Wait()
	close(results)
	ok, conflicts := 0, 0
	for err := range results {
		if err == nil {
			ok++
		} else if errors.Is(err, store.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || conflicts != 1 {
		t.Fatalf("writers: %d winners %d conflicts", ok, conflicts)
	}
}

func TestArtifactDeletionQueueRetainsSharedChunks(t *testing.T) {
	b, a := setup(t)
	base, m, files := artifactFixture(t)
	raw := []byte(uuid.NewString())
	digest := artifact.Digest(raw)
	files["unique.txt"] = raw
	m.Files = append(m.Files, artifact.File{Path: "unique.txt", Role: "context", ContentType: "text/plain", SHA256: digest, Size: int64(len(raw)), Chunks: []artifact.Chunk{{SHA256: digest, Size: int64(len(raw))}}})
	uploadFixture(t, base, m, files)
	a.must(201, "POST", base+"/revisions", map[string]any{"manifest": m, "client_key": "one"})
	me := a.must(200, "GET", "/api/v1/me", nil)
	userID := uuid.MustParse(str(sub(me, "user"), "id"))
	ctx := context.Background()
	chunk, err := testAPI.store.GetArtifactBlob(ctx, userID, digest)
	if err != nil {
		t.Fatal(err)
	}
	// Test-only time travel keeps production SQL in the store package.
	if _, err := testPool.Exec(ctx, "UPDATE artifact_blobs SET updated_at=now()-interval '2 days' WHERE user_id=$1", userID); err != nil {
		t.Fatal(err)
	}
	testAPI.pruneArtifactObjects(ctx)
	if _, err := testAPI.store.GetArtifactBlob(ctx, userID, chunk.SHA256); err != nil {
		t.Fatal("collected referenced chunk", err)
	}
	b.must(200, "DELETE", base, nil)
	testAPI.pruneArtifactObjects(ctx)
	if _, err := testAPI.store.GetArtifactBlob(ctx, userID, chunk.SHA256); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("unreferenced chunk retained", err)
	}
	r, _, _, err := testAPI.blobs.Get(ctx, chunk.ObjectKey)
	if r != nil {
		r.Close()
	}
	if !errors.Is(err, blob.ErrNotFound) {
		t.Fatal("object deletion was not performed", err)
	}
}
