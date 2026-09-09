package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Failed posts to ext:<new-id> must not leave an empty "Untitled thread"
// behind: validation happens before the auto-create where possible, and a
// thread auto-created moments ago is rolled back when the follow-up fails.
func TestAutoCreateOrphanCleanup(t *testing.T) {
	_, a := setup(t)

	mustGone := func(ext string) {
		t.Helper()
		if status, out := a.do("GET", "/api/v1/threads/ext:"+ext, nil); status != http.StatusNotFound {
			t.Fatalf("expected no thread for ext:%s, got %d %v", ext, status, out)
		}
	}

	// 1. Message with an attachment that belongs to no thread: the thread is
	// auto-created first, then CreateMessage rejects the unknown id.
	ext := "orphan-cleanup:msg-1"
	if status, _ := a.do("POST", "/api/v1/threads/ext:"+ext+"/messages", map[string]any{
		"body": "hi", "attachments": []string{uuid.NewString()},
	}); status != http.StatusUnprocessableEntity {
		t.Fatalf("expected unknown attachment to be rejected, got %d", status)
	}
	mustGone(ext)

	// 2. Message with an over-long title is rejected before creating.
	ext = "orphan-cleanup:msg-2"
	if status, _ := a.do("POST", "/api/v1/threads/ext:"+ext+"/messages", map[string]any{
		"body": "hi", "title": strings.Repeat("x", 301),
	}); status != http.StatusUnprocessableEntity {
		t.Fatalf("expected long title to be rejected, got %d", status)
	}
	mustGone(ext)

	// 3. Multipart message with more files than fit is rejected before creating.
	ext = "orphan-cleanup:msg-3"
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for i := 0; i < 9; i++ {
		fw, _ := mw.CreateFormFile("file", "f.txt")
		_, _ = fw.Write([]byte("hello\n"))
	}
	_ = mw.WriteField("body", "too many files")
	mw.Close()
	req, _ := http.NewRequest("POST", testSrv.URL+"/api/v1/threads/ext:"+ext+"/messages", &buf)
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected too many files to be rejected, got %d", res.StatusCode)
	}
	mustGone(ext)

	// 4. Empty raw upload: the thread is auto-created first, then the store
	// rejects the empty bytes.
	ext = "orphan-cleanup:upl-1"
	req, _ = http.NewRequest("POST", testSrv.URL+"/api/v1/threads/ext:"+ext+"/attachments?filename=empty.txt", bytes.NewReader(nil))
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "text/plain")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected empty upload to be rejected, got %d", res.StatusCode)
	}
	mustGone(ext)

	// 5. Multipart upload with no file parts is rejected.
	ext = "orphan-cleanup:upl-2"
	var buf2 bytes.Buffer
	mw2 := multipart.NewWriter(&buf2)
	_ = mw2.WriteField("note", "no files here")
	mw2.Close()
	req, _ = http.NewRequest("POST", testSrv.URL+"/api/v1/threads/ext:"+ext+"/attachments", &buf2)
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", mw2.FormDataContentType())
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected fileless upload to be rejected, got %d", res.StatusCode)
	}
	mustGone(ext)

	// 6. Invalid question is rejected before creating.
	ext = "orphan-cleanup:q-1"
	if status, _ := a.do("POST", "/api/v1/threads/ext:"+ext+"/questions", map[string]any{"prompt": ""}); status != http.StatusUnprocessableEntity {
		t.Fatalf("expected empty prompt to be rejected, got %d", status)
	}
	mustGone(ext)

	// 7. Invalid artifact registration is rejected before creating.
	ext = "orphan-cleanup:art-1"
	if status, _ := a.do("PUT", "/api/v1/threads/ext:"+ext+"/artifacts/report", map[string]any{"title": ""}); status != http.StatusUnprocessableEntity {
		t.Fatalf("expected empty artifact title to be rejected, got %d", status)
	}
	mustGone(ext)

	// Control: a successful auto-create still works and stays.
	ext = "orphan-cleanup:ok-1"
	a.must(http.StatusCreated, "POST", "/api/v1/threads/ext:"+ext+"/messages", map[string]any{"body": "hello", "title": "kept"})
	a.must(http.StatusOK, "GET", "/api/v1/threads/ext:"+ext, nil)
}
