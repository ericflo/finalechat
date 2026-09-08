package blob

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

func TestB2RetiredArtifactCollectionIncludesLostUploadVersions(t *testing.T) {
	versions := []string{"known-version", "lost-response-version"}
	deleted := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "test-token" {
			t.Error("missing authorization")
		}
		switch r.URL.Path {
		case "/b2api/v4/b2_list_file_versions":
			if r.URL.Query().Get("prefix") != "artifacts/unique" || r.URL.Query().Get("bucketId") != "bucket" {
				t.Error("unbounded or wrong-bucket lookup")
			}
			files := []map[string]string{}
			for _, id := range versions {
				files = append(files, map[string]string{"fileName": "artifacts/unique", "fileId": id, "action": "upload"})
			}
			files = append(files, map[string]string{"fileName": "artifacts/unique-neighbor", "fileId": "keep", "action": "upload"})
			_ = json.NewEncoder(w).Encode(map[string]any{"files": files})
		case "/b2api/v4/b2_delete_file_version":
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["fileName"] != "artifacts/unique" || in["fileId"] == "keep" {
				t.Error("collected a prefix neighbor")
			}
			deleted = append(deleted, in["fileId"])
			versions = slices.DeleteFunc(versions, func(id string) bool { return id == in["fileId"] })
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			t.Error("unexpected API route", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	b := &B2{client: server.Client(), authToken: "test-token", apiURL: server.URL, bucketID: "bucket", authorizedAt: time.Now()}
	if err := b.DeleteAll(context.Background(), "artifacts/unique"); err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 2 || len(versions) != 0 {
		t.Fatal("lost upload version retained")
	}
	if err := b.DeleteAll(context.Background(), "artifacts/unique"); err != nil {
		t.Fatal("non-idempotent collection", err)
	}
}

func TestB2CollectionFailureIsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "temporarily unavailable", 503) }))
	defer server.Close()
	b := &B2{client: server.Client(), authToken: "test", apiURL: server.URL, bucketID: "bucket", authorizedAt: time.Now()}
	if err := b.DeleteAll(context.Background(), "artifacts/unique"); err == nil {
		t.Fatal("failed listing treated as successful deletion")
	}
}
