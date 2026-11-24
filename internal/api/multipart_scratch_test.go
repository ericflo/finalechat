package api

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"syscall"
	"testing"
)

// postFiles sends one multipart request carrying the files and returns the
// status and decoded reply.
func postFiles(t *testing.T, token, path string, fields map[string]string, files map[string][]byte) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	for name, data := range files {
		fw, _ := mw.CreateFormFile("file", name)
		_, _ = fw.Write(data)
	}
	mw.Close()
	req, _ := http.NewRequest("POST", testSrv.URL+path, &buf)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

// A container with a read-only root filesystem and nothing mounted at the
// temp dir has no scratch space. Uploads past the in-memory threshold used
// to fail there with a 400 that blamed the request; they must go through.
func TestMultipartWithoutScratchSpace(t *testing.T) {
	b, a := setup(t)
	// Nothing can be created here, as on a read-only filesystem.
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))

	// Two files that together pass the in-memory threshold, so the second
	// would spill to a temp file.
	one, two := make([]byte, 3<<20), make([]byte, 2<<20)
	_, _ = rand.Read(one)
	_, _ = rand.Read(two)
	if len(one)+len(two) <= multipartMemory {
		t.Fatal("the files must exceed the in-memory threshold to exercise the spill")
	}

	status, out := postFiles(t, a.token, "/api/v1/threads/ext:scratch:1/messages",
		map[string]string{"body": "two stills"}, map[string][]byte{"one.bin": one, "two.bin": two})
	if status != http.StatusCreated {
		t.Fatalf("multipart message without scratch space: %d %v", status, out)
	}
	list := sub(out, "message")["attachments"].([]any)
	if len(list) != 2 {
		t.Fatalf("expected 2 attachments, got %d", len(list))
	}
	want := map[string][]byte{"one.bin": one, "two.bin": two}
	for _, it := range list {
		x := it.(map[string]any)
		res, err := b.http.Get(testSrv.URL + str(x, "url"))
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || !bytes.Equal(got, want[str(x, "filename")]) {
			t.Fatalf("%s: status %d, %d bytes back, want %d", str(x, "filename"), res.StatusCode, len(got), len(want[str(x, "filename")]))
		}
	}

	// The upload endpoint parses the same way.
	status, out = postFiles(t, a.token, "/api/v1/threads/ext:scratch:1/attachments", nil, map[string][]byte{"big.bin": append(one, two...)})
	if status != http.StatusCreated {
		t.Fatalf("multipart upload without scratch space: %d %v", status, out)
	}
}

// A temp file that cannot be written is the server's problem: it must not
// come back as a 400 that tells the client its request was malformed.
func TestMultipartErrorClassification(t *testing.T) {
	disk := &fs.PathError{Op: "write", Path: "/tmp/multipart-1", Err: syscall.ENOSPC}
	var ae *apiError
	if err := multipartError(disk); errors.As(err, &ae) {
		t.Fatalf("a scratch file failure must be an internal error, got %d %s", ae.Status, ae.Code)
	}
	if err := multipartError(multipart.ErrMessageTooLarge); !errors.As(err, &ae) || ae.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("an oversized form must be a 413, got %v", err)
	}
	if err := multipartError(&http.MaxBytesError{Limit: 1}); !errors.As(err, &ae) || ae.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("an oversized body must be a 413, got %v", err)
	}
	if err := multipartError(io.ErrUnexpectedEOF); !errors.As(err, &ae) || ae.Status != http.StatusBadRequest {
		t.Fatalf("a truncated body must be a 400, got %v", err)
	}
}
