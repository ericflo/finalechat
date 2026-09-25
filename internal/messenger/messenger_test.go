package messenger

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMarkdown(t *testing.T) {
	cases := []struct{ in, want string }{
		{"## Deployed **v1.2**", "*Deployed v1.2*"},
		{"Tests **pass** and *mostly* fast", "Tests *pass* and _mostly_ fast"},
		{"- one\n- two\n  * nested", "• one\n• two\n  • nested"},
		{"See [the PR](https://github.com/x/y/pull/1).", "See the PR (https://github.com/x/y/pull/1)."},
		{"[https://a.b](https://a.b)", "https://a.b"},
		{"![shot](a.png)", "[image: shot]"},
		{"use `**not bold**` here", "use `**not bold**` here"},
		{"```go\nx := **y**\n```", "```go\nx := **y**\n```"},
		{"> quoted **bit**", "│ quoted *bit*"},
		{"a\n\n\n\nb", "a\n\nb"},
		{"~~old~~ new", "~old~ new"},
		{"2 * 3 * 4", "2 * 3 * 4"},
	}
	for _, c := range cases {
		if got := Markdown(c.in); got != c.want {
			t.Errorf("Markdown(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestChunk(t *testing.T) {
	if got := Chunk("short", 2000); len(got) != 1 || got[0] != "short" {
		t.Fatalf("short text: %q", got)
	}
	para := strings.Repeat("word ", 150) // 750 runes
	text := para + "\n\n" + para + "\n\n" + para
	pieces := Chunk(text, 1000)
	if len(pieces) != 3 {
		t.Fatalf("want 3 paragraph pieces, got %d", len(pieces))
	}
	for _, p := range pieces {
		if utf8.RuneCountInString(p) > 1000 {
			t.Fatalf("piece too long: %d", utf8.RuneCountInString(p))
		}
	}
	// A code block cut in half is closed and reopened.
	code := "```\n" + strings.Repeat("line of code here\n", 100) + "```"
	pieces = Chunk(code, 500)
	if len(pieces) < 3 {
		t.Fatalf("want several pieces, got %d", len(pieces))
	}
	for i, p := range pieces {
		if strings.Count(p, "```")%2 != 0 {
			t.Fatalf("piece %d has an unbalanced fence: %q", i, p)
		}
		if utf8.RuneCountInString(p) > 500 {
			t.Fatalf("piece %d too long", i)
		}
	}
	// No boundary at all still splits, on rune boundaries.
	pieces = Chunk(strings.Repeat("é", 2500), 1000)
	total := 0
	for _, p := range pieces {
		if !utf8.ValidString(p) || utf8.RuneCountInString(p) > 1000 {
			t.Fatalf("bad piece %q", p)
		}
		total += utf8.RuneCountInString(p)
	}
	if total != 2500 {
		t.Fatalf("lost text: %d", total)
	}
}

func TestSignature(t *testing.T) {
	body := []byte(`{"object":"page"}`)
	sig := Sign("s3cret", body)
	if !ValidSignature("s3cret", body, sig) {
		t.Fatal("valid signature rejected")
	}
	if ValidSignature("other", body, sig) || ValidSignature("s3cret", append(body, ' '), sig) || ValidSignature("s3cret", body, "sha1=abc") || ValidSignature("", body, sig) {
		t.Fatal("invalid signature accepted")
	}
}

func TestSendText(t *testing.T) {
	var got map[string]any
	var auth, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		if strings.Contains(string(raw), "late") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"outside window","type":"OAuthException","code":10,"error_subcode":2018278}}`))
			return
		}
		_, _ = w.Write([]byte(`{"recipient_id":"42","message_id":"m_1"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "123", "EAAtoken")
	mid, err := c.SendText(context.Background(), "42", "hi", []QuickReply{{Title: "A very long option title indeed", Payload: "a:1"}}, "m_0")
	if err != nil || mid != "m_1" {
		t.Fatalf("send: %v %q", err, mid)
	}
	if auth != "Bearer EAAtoken" || path != "/123/messages" {
		t.Fatalf("auth %q path %q", auth, path)
	}
	msg := got["message"].(map[string]any)
	qr := msg["quick_replies"].([]any)[0].(map[string]any)
	if utf8.RuneCountInString(qr["title"].(string)) > MaxQuickReplyTitle || msg["reply_to"].(map[string]any)["mid"] != "m_0" {
		t.Fatalf("message shape: %v", msg)
	}
	_, err = c.SendText(context.Background(), "42", "late", nil, "")
	if !IsWindowClosed(err) {
		t.Fatalf("want window closed, got %v", err)
	}
}
