package api

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/google/uuid"
)

func TestArtifactAnchorsStayWithinDatasetAndThread(t *testing.T) {
	b, a := setup(t)
	for _, provider := range []string{"eagent", "claude-code", "codex"} {
		t.Run(provider, func(t *testing.T) {
			base, manifest, files := artifactFixture(t)
			session := uuid.NewString()
			format := provider + ".native-jsonl/v1"
			if provider == "eagent" {
				format = "eagent.session-jsonl/v1"
			}
			manifest.Dataset["format"], manifest.Dataset["session_id"] = format, session
			thread := str(sub(a.must(200, "GET", base, nil), "artifact"), "thread_id")
			if _, err := testPool.Exec(context.Background(), "UPDATE threads SET external_id = $1 WHERE id = $2", provider+":"+session, thread); err != nil {
				t.Fatal(err)
			}
			uploadFixture(t, base, manifest, files)
			revision := str(sub(a.must(201, "POST", base+"/revisions", map[string]any{"client_key": "anchor", "manifest": manifest}), "revision"), "id")
			endpoint := base + "/revisions/" + revision + "/message?anchor="
			lookup := func(anchor any) string {
				raw, _ := json.Marshal(anchor)
				return endpoint + url.QueryEscape(string(raw))
			}
			anchor := map[string]any{"dataset_format": format, "session_id": session, "event_id": "native-record"}
			message := str(sub(a.must(201, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "Mirrored native event", "meta": map[string]any{"source_anchor": anchor}}), "message"), "id")
			if str(b.must(200, "GET", lookup(anchor), nil), "message_id") != message {
				t.Fatal("source anchor did not find the mirrored message")
			}
			anchor["session_id"] = "another-session"
			b.must(422, "GET", lookup(anchor), nil)
			anchor["session_id"] = session
			anchor["url"] = "https://unrelated.invalid"
			b.must(422, "GET", lookup(anchor), nil)
			delete(anchor, "url")
			b.must(422, "GET", lookup(anchor)+url.QueryEscape(" {}"), nil)
			delete(anchor, "event_id")
			anchor["message_id"] = message
			b.must(200, "GET", lookup(anchor), nil)
			other := str(sub(a.must(201, "POST", "/api/v1/threads/ext:other-"+session+"/messages", map[string]any{"body": "Another thread"}), "message"), "id")
			anchor["message_id"] = other
			b.must(404, "GET", lookup(anchor), nil)
			anchor["message_id"] = message
			a.must(200, "DELETE", "/api/v1/messages/"+message, nil)
			b.must(404, "GET", lookup(anchor), nil)
			delete(anchor, "message_id")
			if provider == "eagent" || provider == "claude-code" {
				legacy := map[string]any{"seq": 2, "eagent": "narrator"}
				anchor["seq"] = 2
				if provider == "claude-code" {
					legacy = map[string]any{"via": "claude-code", "transcript_uuid": "original-uuid"}
					delete(anchor, "seq")
					anchor["event_id"] = "original-uuid"
				}
				old := str(sub(a.must(201, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "Existing integration record", "meta": legacy}), "message"), "id")
				if str(b.must(200, "GET", lookup(anchor), nil), "message_id") != old {
					t.Fatal("legacy native anchor failed")
				}
			}
		})
	}
}
