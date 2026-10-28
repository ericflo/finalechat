package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVersion(t *testing.T) {
	// No credentials: deploy checks call it bare.
	res, err := http.Get(testSrv.URL + "/version")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}
	if got := res.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control %q", got)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("not JSON (%v): %s", err, raw)
	}
	if len(out) != 1 || out["version"] != "test" {
		t.Fatalf("body: %v", out)
	}

	// An unstamped build says "dev".
	s := &Server{}
	rec := httptest.NewRecorder()
	s.handleVersion(rec, httptest.NewRequest("GET", "/version", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "{\"version\":\"dev\"}\n" {
		t.Fatalf("unstamped: %d %q", rec.Code, rec.Body.String())
	}
}
