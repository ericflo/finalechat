package api

import (
	"net/http"
	"testing"
)

// The web client declares its eagent capability handshake (v1) on every
// user message and answer. Messages already persist a free-form meta object,
// so the capsule passes through untouched; answers synthesize their
// transcript message, which keeps its answer identity and merges only the
// client's eagent.* keys.
func TestClientCapsulePassthrough(t *testing.T) {
	b, _ := setup(t)
	thread := str(sub(b.must(http.StatusCreated, "POST", "/api/v1/threads", map[string]any{"external_id": "caps:1", "title": "caps"}), "thread"), "id")

	capsule := map[string]any{
		"timezone": "America/Los_Angeles",
		"locale":   "en-US",
		"device":   "phone",
		"app":      "finalechat-web/test",
		"screen":   "1512x982",
		"supplies": []string{"tz", "locale", "screen"},
	}

	// A user message carries the capsule verbatim.
	sent := b.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{
		"body": "hello from the phone", "meta": map[string]any{"eagent.client": capsule},
	})
	got := sub(sent, "message")["meta"].(map[string]any)
	round, ok := got["eagent.client"].(map[string]any)
	if !ok || round["timezone"] != "America/Los_Angeles" || round["locale"] != "en-US" || round["device"] != "phone" ||
		round["app"] != "finalechat-web/test" || round["screen"] != "1512x982" {
		t.Fatalf("message meta lost the capsule: %v", got)
	}
	if sup, ok := round["supplies"].([]any); !ok || len(sup) != 3 || sup[0] != "tz" || sup[1] != "locale" || sup[2] != "screen" {
		t.Fatalf("message capsule supplies wrong: %v", round)
	}
	// It survives a fresh read, which is what eagent polls.
	fresh := b.must(http.StatusOK, "GET", "/api/v1/messages/"+str(sub(sent, "message"), "id"), nil)
	if fmeta := sub(fresh, "message")["meta"].(map[string]any); fmeta["eagent.client"] == nil {
		t.Fatalf("re-read message lost the capsule: %v", fmeta)
	}

	// An answer carries the capsule onto the synthesized transcript message
	// without losing its answer identity.
	q := b.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/questions", map[string]any{
		"prompt": "Ship it?", "options": []map[string]any{{"label": "Yes"}, {"label": "No"}},
	})
	qid := str(sub(q, "question"), "id")
	answered := b.must(http.StatusOK, "POST", "/api/v1/questions/"+qid+"/answer", map[string]any{
		"selected": []string{"Yes"}, "meta": map[string]any{"eagent.client": capsule},
	})
	ameta := sub(answered, "message")["meta"].(map[string]any)
	if ameta["kind"] != "answer" || ameta["question_id"] != qid {
		t.Fatalf("answer message lost its identity: %v", ameta)
	}
	acaps, ok := ameta["eagent.client"].(map[string]any)
	if !ok || acaps["timezone"] != "America/Los_Angeles" || acaps["device"] != "phone" {
		t.Fatalf("answer message lost the capsule: %v", ameta)
	}

	// Reserved keys cannot be overridden through answer meta.
	q2 := b.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/questions", map[string]any{"prompt": "Again?"})
	qid2 := str(sub(q2, "question"), "id")
	sneaky := b.must(http.StatusOK, "POST", "/api/v1/questions/"+qid2+"/answer", map[string]any{
		"text": "fine", "meta": map[string]any{"kind": "notification", "question_id": "bogus", "eagent.client": capsule},
	})
	smeta := sub(sneaky, "message")["meta"].(map[string]any)
	if smeta["kind"] != "answer" || smeta["question_id"] != qid2 {
		t.Fatalf("answer meta overrode reserved keys: %v", smeta)
	}
	if smeta["eagent.client"] == nil {
		t.Fatalf("answer meta dropped the capsule while filtering: %v", smeta)
	}

	// Absent meta is unchanged behavior on both routes.
	plain := b.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "no capsule"})
	if pmeta := sub(plain, "message")["meta"].(map[string]any); len(pmeta) != 0 {
		t.Fatalf("plain message gained meta: %v", pmeta)
	}
	q3 := b.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/questions", map[string]any{"prompt": "Plain?"})
	answered3 := b.must(http.StatusOK, "POST", "/api/v1/questions/"+str(sub(q3, "question"), "id")+"/answer", map[string]any{"text": "ok"})
	if m3 := sub(answered3, "message")["meta"].(map[string]any); len(m3) != 2 || m3["kind"] != "answer" {
		t.Fatalf("plain answer message meta wrong: %v", m3)
	}

	// A non-object answer meta is rejected like any other bad meta.
	q4 := b.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/questions", map[string]any{"prompt": "Bad meta?"})
	if status, out := b.do("POST", "/api/v1/questions/"+str(sub(q4, "question"), "id")+"/answer", map[string]any{
		"text": "ok", "meta": "nope",
	}); status != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for non-object answer meta, got %d %v", status, out)
	}
}
