package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ericflo/finalechat/internal/control"
	"github.com/ericflo/finalechat/internal/messenger"
)

// fakeGraph stands in for Meta's Send API.
type fakeGraph struct {
	mu   sync.Mutex
	sent []sentMsg
	next int
	// refuse makes the next send fail with this Graph error body.
	refuse string
}

type sentMsg struct {
	// Attachment is "kind:filename:size" for a file message.
	Attachment string
	MID        string
	PSID       string
	Text       string
	Quick      []messenger.QuickReply
	Action     string
	ReplyTo    string
}

func (f *fakeGraph) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Recipient    struct{ ID string } `json:"recipient"`
		SenderAction string              `json:"sender_action"`
		Message      struct {
			Text         string                 `json:"text"`
			QuickReplies []messenger.QuickReply `json:"quick_replies"`
		} `json:"message"`
	}
	var attachment string
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(32 << 20); err == nil {
			_ = json.Unmarshal([]byte(r.FormValue("recipient")), &body.Recipient)
			var m struct {
				Attachment struct{ Type string } `json:"attachment"`
			}
			_ = json.Unmarshal([]byte(r.FormValue("message")), &m)
			if fh := r.MultipartForm.File["filedata"]; len(fh) == 1 {
				attachment = m.Attachment.Type + ":" + fh[0].Filename + ":" + strconv.FormatInt(fh[0].Size, 10)
			}
		}
	} else {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuse != "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, f.refuse)
		f.refuse = ""
		return
	}
	if body.SenderAction != "" {
		f.sent = append(f.sent, sentMsg{PSID: body.Recipient.ID, Action: body.SenderAction})
		_, _ = io.WriteString(w, `{"recipient_id":"`+body.Recipient.ID+`"}`)
		return
	}
	f.next++
	mid := "m_page_" + strconv.Itoa(f.next)
	f.sent = append(f.sent, sentMsg{MID: mid, PSID: body.Recipient.ID, Text: body.Message.Text, Quick: body.Message.QuickReplies, Attachment: attachment})
	_, _ = io.WriteString(w, `{"recipient_id":"`+body.Recipient.ID+`","message_id":"`+mid+`"}`)
}

// texts returns the text messages sent since index from.
func (f *fakeGraph) texts(from int) []sentMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []sentMsg
	for _, m := range f.sent[min(from, len(f.sent)):] {
		if m.Action == "" {
			out = append(out, m)
		}
	}
	return out
}

func (f *fakeGraph) mark() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

const (
	testPage   = "1359515167241068"
	testSecret = "app-secret"
	testVerify = "verify-me"
)

// withMessenger enables the connector on the shared test server.
func withMessenger(t *testing.T) *fakeGraph {
	t.Helper()
	g := &fakeGraph{}
	srv := httptest.NewServer(g)
	oldCfg, oldClient, oldSettle, oldPoll, oldLimiter := testAPI.cfg, testAPI.messenger, testAPI.messengerSettle, testAPI.messengerPoll, testAPI.messengerLinkLimiter
	testAPI.cfg.MessengerPageID, testAPI.cfg.MessengerPageToken, testAPI.cfg.MessengerAppSecret, testAPI.cfg.MessengerVerifyToken = testPage, "EAAtest", testSecret, testVerify
	testAPI.messenger = messenger.NewClient(srv.URL, testPage, "EAAtest")
	testAPI.messengerSettle, testAPI.messengerPoll = 0, 50*time.Millisecond
	testAPI.messengerLinkLimiter = newRateLimiter(1000)
	t.Cleanup(func() {
		srv.Close()
		testAPI.cfg, testAPI.messenger, testAPI.messengerSettle, testAPI.messengerPoll, testAPI.messengerLinkLimiter = oldCfg, oldClient, oldSettle, oldPoll, oldLimiter
	})
	return g
}

var webhookSeq int

// hook delivers one messaging event as Meta would and returns the status.
func hook(t *testing.T, psid string, message map[string]any, postback map[string]any) int {
	t.Helper()
	webhookSeq++
	ev := map[string]any{"sender": map[string]any{"id": psid}, "recipient": map[string]any{"id": testPage}, "timestamp": time.Now().UnixMilli()}
	if message != nil {
		if _, ok := message["mid"]; !ok {
			message["mid"] = "m_user_" + strconv.Itoa(webhookSeq) + "_" + uuid.NewString()[:8]
		}
		ev["message"] = message
	}
	if postback != nil {
		ev["postback"] = postback
	}
	body, _ := json.Marshal(map[string]any{"object": "page", "entry": []any{map[string]any{"id": testPage, "time": time.Now().UnixMilli(), "messaging": []any{ev}}}})
	req, _ := http.NewRequest("POST", testSrv.URL+"/api/messenger/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", messenger.Sign(testSecret, body))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func say(t *testing.T, psid, text string) {
	t.Helper()
	if code := hook(t, psid, map[string]any{"text": text}, nil); code != http.StatusOK {
		t.Fatalf("webhook %q: %d", text, code)
	}
}

func tap(t *testing.T, psid, title, payload string) {
	t.Helper()
	if code := hook(t, psid, map[string]any{"text": title, "quick_reply": map[string]any{"payload": payload}}, nil); code != http.StatusOK {
		t.Fatalf("tap %q: %d", payload, code)
	}
}

func lastText(t *testing.T, g *fakeGraph, from int) string {
	t.Helper()
	got := g.texts(from)
	if len(got) == 0 {
		t.Fatalf("nothing was sent to Messenger")
	}
	return got[len(got)-1].Text
}

func relay(t *testing.T) {
	t.Helper()
	testAPI.messengerRelayOnce(context.Background())
}

// linkMessenger links psid to the test account.
func linkMessenger(t *testing.T, g *fakeGraph, psid string) {
	t.Helper()
	b, _ := setup(t)
	out := b.must(http.StatusCreated, "POST", "/api/v1/messenger/code", nil)
	code := str(out, "code")
	if len(code) != 9 || code[4] != '-' {
		t.Fatalf("code shape: %q", code)
	}
	from := g.mark()
	say(t, psid, "link "+strings.ToLower(code))
	if txt := lastText(t, g, from); !strings.Contains(txt, "Linked to Finalechat as eric@example.com") {
		t.Fatalf("link reply: %q", txt)
	}
}

func TestMessengerWebhookSecurityAndLinking(t *testing.T) {
	b, a := setup(t)
	g := withMessenger(t)

	// Meta's subscription handshake.
	res, _ := http.Get(testSrv.URL + "/api/messenger/webhook?hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=123")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong verify token: %d", res.StatusCode)
	}
	res, _ = http.Get(testSrv.URL + "/api/messenger/webhook?hub.mode=subscribe&hub.verify_token=" + testVerify + "&hub.challenge=ch4ll3nge")
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || string(raw) != "ch4ll3nge" {
		t.Fatalf("handshake: %d %q", res.StatusCode, raw)
	}

	// Unsigned or mis-signed deliveries are refused.
	body := []byte(`{"object":"page","entry":[]}`)
	for _, sig := range []string{"", "sha256=00", messenger.Sign("other", body)} {
		req, _ := http.NewRequest("POST", testSrv.URL+"/api/messenger/webhook", bytes.NewReader(body))
		req.Header.Set("X-Hub-Signature-256", sig)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("signature %q accepted: %d", sig, res.StatusCode)
		}
	}

	// Linking is for the app only; an agent token cannot mint a code.
	a.must(http.StatusForbidden, "POST", "/api/v1/messenger/code", nil)
	status := b.must(http.StatusOK, "GET", "/api/v1/messenger", nil)
	if status["enabled"] != true || status["page_url"] != "https://m.me/"+testPage {
		t.Fatalf("status: %v", status)
	}

	// A stranger gets instructions, and a wrong code does nothing.
	psid := "psid-" + uuid.NewString()[:8]
	from := g.mark()
	say(t, psid, "hello?")
	if txt := lastText(t, g, from); !strings.Contains(txt, "private Finalechat bridge") {
		t.Fatalf("stranger reply: %q", txt)
	}
	say(t, psid, "link AAAA-BBBB")
	if txt := lastText(t, g, from); !strings.Contains(txt, "unknown or has expired") {
		t.Fatalf("bad code reply: %q", txt)
	}
	linkMessenger(t, g, psid)
	status = b.must(http.StatusOK, "GET", "/api/v1/messenger", nil)
	if status["link"] == nil {
		t.Fatalf("link not reported: %v", status)
	}
	// A code works once.
	b.must(http.StatusOK, "DELETE", "/api/v1/messenger", nil)
	if st := b.must(http.StatusOK, "GET", "/api/v1/messenger", nil); st["link"] != nil {
		t.Fatalf("unlink kept the link: %v", st)
	}
}

func TestMessengerRelayAndReplies(t *testing.T) {
	_, a := setup(t)
	g := withMessenger(t)
	psid := "psid-" + uuid.NewString()[:8]
	linkMessenger(t, g, psid)

	ext := "msgr-" + uuid.NewString()[:8]
	// Things the relay must not forward: the session_start note and a
	// terminal prompt an agent mirrors as the user.
	a.must(http.StatusCreated, "POST", "/api/v1/threads/ext:"+ext+"/messages", map[string]any{"title": "finalechat", "agent": "eagent", "sender": "system", "body": "eagent session started here.", "meta": map[string]any{"kind": "session_start"}})
	a.must(http.StatusCreated, "POST", "/api/v1/threads/ext:"+ext+"/messages", map[string]any{"sender": "user", "body": "typed in the terminal"})
	a.must(http.StatusCreated, "POST", "/api/v1/threads/ext:"+ext+"/messages", map[string]any{"body": "## Done\n\nTests **pass**. See [the log](https://example.com/log).", "importance": "important"})
	from := g.mark()
	relay(t)
	got := g.texts(from)
	if len(got) != 1 {
		t.Fatalf("want one relayed message, got %d: %+v", len(got), got)
	}
	if !strings.HasPrefix(got[0].Text, "❗ [#") || !strings.Contains(got[0].Text, " finalechat]\n*Done*\n\nTests *pass*. See the log (https://example.com/log).") {
		t.Fatalf("relayed text: %q", got[0].Text)
	}
	relayedMID := got[0].MID
	// Relaying again sends nothing new.
	from = g.mark()
	relay(t)
	if n := len(g.texts(from)); n != 0 {
		t.Fatalf("relay repeated %d messages", n)
	}

	// A plain reply lands in the thread that spoke, exactly like the app's.
	from = g.mark()
	mid := "m_reply_" + uuid.NewString()[:8]
	if code := hook(t, psid, map[string]any{"mid": mid, "text": "Nice. Now update the README."}, nil); code != 200 {
		t.Fatal(code)
	}
	// Meta redelivers; nothing doubles.
	if code := hook(t, psid, map[string]any{"mid": mid, "text": "Nice. Now update the README."}, nil); code != 200 {
		t.Fatal(code)
	}
	msgs := a.must(http.StatusOK, "GET", "/api/v1/threads/ext:"+ext+"/messages?sender=user", nil)["messages"].([]any)
	var replies []map[string]any
	for _, m := range msgs {
		if mm := m.(map[string]any); mm["origin"] == "session" {
			replies = append(replies, mm)
		}
	}
	if len(replies) != 1 || replies[0]["body"] != "Nice. Now update the README." {
		t.Fatalf("replies: %v", replies)
	}
	caps := replies[0]["meta"].(map[string]any)["eagent.client"].(map[string]any)
	if !strings.HasPrefix(caps["app"].(string), "finalechat-messenger/") || caps["device"] != "phone" {
		t.Fatalf("capsule: %v", caps)
	}
	// The reply itself is not echoed back to Messenger.
	relay(t)
	for _, m := range g.texts(from) {
		if strings.Contains(m.Text, "update the README") {
			t.Fatalf("echoed the user's reply: %q", m.Text)
		}
	}

	// A second thread speaks; a swipe-reply to the first message still
	// goes to the first thread.
	ext2 := "msgr-" + uuid.NewString()[:8]
	a.must(http.StatusCreated, "POST", "/api/v1/threads/ext:"+ext2+"/messages", map[string]any{"title": "other", "body": "Working on the other thing."})
	relay(t)
	hook(t, psid, map[string]any{"text": "swiped", "reply_to": map[string]any{"mid": relayedMID}}, nil)
	first := a.must(http.StatusOK, "GET", "/api/v1/threads/ext:"+ext+"/messages?sender=user", nil)["messages"].([]any)
	if body := first[len(first)-1].(map[string]any)["body"]; body != "swiped" {
		t.Fatalf("swipe-reply went elsewhere; last in first thread: %v", body)
	}
	// A plain reply now follows the conversation (first thread, where the
	// swipe went), and /go pins another.
	say(t, psid, "/ls")
	list := lastText(t, g, 0)
	if !strings.Contains(list, "finalechat") || !strings.Contains(list, "other") {
		t.Fatalf("/ls: %q", list)
	}
	thread2 := a.must(http.StatusOK, "GET", "/api/v1/threads/ext:"+ext2, nil)
	_ = thread2
	h, err := testAPI.store.MessengerHandle(context.Background(), uuid.MustParse(str(sub(a.must(http.StatusOK, "GET", "/api/v1/me", nil), "user"), "id")), uuid.MustParse(str(sub(thread2, "thread"), "id")))
	if err != nil {
		t.Fatal(err)
	}
	say(t, psid, "/go "+strconv.Itoa(h))
	say(t, psid, "pinned reply")
	second := a.must(http.StatusOK, "GET", "/api/v1/threads/ext:"+ext2+"/messages?sender=user", nil)["messages"].([]any)
	if len(second) == 0 || second[len(second)-1].(map[string]any)["body"] != "pinned reply" {
		t.Fatalf("/go did not pin: %v", second)
	}
	say(t, psid, "/go")

	// A long message is cut into bubbles, three at a time, then /more.
	long := strings.Repeat(strings.Repeat("word ", 300)+"\n\n", 6)
	a.must(http.StatusCreated, "POST", "/api/v1/threads/ext:"+ext+"/messages", map[string]any{"body": long})
	from = g.mark()
	relay(t)
	bubbles := g.texts(from)
	if len(bubbles) != 3 || !strings.Contains(bubbles[2].Text, "/more (") {
		t.Fatalf("long message bubbles: %d, last %q", len(bubbles), bubbles[len(bubbles)-1].Text)
	}
	from = g.mark()
	say(t, psid, "/more")
	if n := len(g.texts(from)); n < 1 {
		t.Fatal("/more sent nothing")
	}

	// /quiet leaves out ordinary messages but not important ones.
	say(t, psid, "/quiet")
	a.must(http.StatusCreated, "POST", "/api/v1/threads/ext:"+ext+"/messages", map[string]any{"body": "routine progress"})
	a.must(http.StatusCreated, "POST", "/api/v1/threads/ext:"+ext+"/messages", map[string]any{"body": "blocked!", "importance": "important"})
	from = g.mark()
	relay(t)
	got = g.texts(from)
	if len(got) != 1 || !strings.Contains(got[0].Text, "blocked!") {
		t.Fatalf("quiet relay: %+v", got)
	}
	say(t, psid, "/loud")

	// A closed 24-hour window holds delivery until the person writes again.
	a.must(http.StatusCreated, "POST", "/api/v1/threads/ext:"+ext+"/messages", map[string]any{"body": "held back"})
	g.mu.Lock()
	g.refuse = `{"error":{"message":"outside window","code":10,"error_subcode":2018278}}`
	g.mu.Unlock()
	relay(t)
	from = g.mark()
	relay(t)
	if n := len(g.texts(from)); n != 0 {
		t.Fatalf("sent %d messages with the window closed", n)
	}
	say(t, psid, "back")
	from = g.mark()
	relay(t)
	found := false
	for _, m := range g.texts(from) {
		found = found || strings.Contains(m.Text, "held back")
	}
	if !found {
		t.Fatal("held message was not delivered after the window reopened")
	}
}

func TestMessengerQuestions(t *testing.T) {
	_, a := setup(t)
	g := withMessenger(t)
	psid := "psid-" + uuid.NewString()[:8]
	linkMessenger(t, g, psid)
	ext := "msgq-" + uuid.NewString()[:8]
	ask := func(body map[string]any) string {
		out := a.must(http.StatusCreated, "POST", "/api/v1/threads/ext:"+ext+"/questions", body)
		return str(sub(out, "question"), "id")
	}
	question := func(id string) map[string]any {
		return sub(a.must(http.StatusOK, "GET", "/api/v1/questions/"+id, nil), "question")
	}

	// Buttons: one per option plus Skip.
	q1 := ask(map[string]any{"title": "deploy", "prompt": "Promote to **production**?", "options": []map[string]any{{"label": "Ship it", "description": "same image"}, {"label": "Hold"}}})
	from := g.mark()
	relay(t)
	sent := g.texts(from)
	if len(sent) != 1 || !strings.Contains(sent[0].Text, "❓ [#") || !strings.Contains(sent[0].Text, "Promote to *production*?") || !strings.Contains(sent[0].Text, "1. Ship it — same image") {
		t.Fatalf("question text: %+v", sent)
	}
	quick := sent[0].Quick
	if len(quick) != 3 || quick[1].Payload != "a:"+q1+":1" || quick[2].Title != "Skip" {
		t.Fatalf("quick replies: %+v", quick)
	}
	tap(t, psid, quick[1].Title, quick[1].Payload)
	if q := question(q1); q["status"] != "answered" || q["answer"].(map[string]any)["selected"].([]any)[0] != "Hold" {
		t.Fatalf("tap did not answer: %v", q)
	}
	// The transcript message is the app's answer message.
	msgs := a.must(http.StatusOK, "GET", "/api/v1/threads/ext:"+ext+"/messages?sender=user", nil)["messages"].([]any)
	ans := msgs[len(msgs)-1].(map[string]any)
	meta := ans["meta"].(map[string]any)
	if ans["origin"] != "session" || meta["kind"] != "answer" || meta["question_id"] != q1 || ans["body"] != "Hold" || meta["eagent.client"] == nil {
		t.Fatalf("answer message: %v", ans)
	}
	// Tapping again says it is resolved.
	from = g.mark()
	tap(t, psid, quick[0].Title, quick[0].Payload)
	if txt := lastText(t, g, from); !strings.Contains(txt, "already answered") {
		t.Fatalf("second tap: %q", txt)
	}

	// A typed number picks an option; typed words answer a free-form one.
	q2 := ask(map[string]any{"prompt": "Which branch?", "options": []map[string]any{{"label": "main"}, {"label": "dev"}}})
	relay(t)
	say(t, psid, "2")
	if q := question(q2); q["answer"].(map[string]any)["selected"].([]any)[0] != "dev" {
		t.Fatalf("typed number: %v", q)
	}
	q3 := ask(map[string]any{"prompt": "Anything else?", "options": []map[string]any{{"label": "No"}}})
	relay(t)
	say(t, psid, "Also bump the version")
	if q := question(q3); q["answer"].(map[string]any)["text"] != "Also bump the version" {
		t.Fatalf("free text: %v", q)
	}
	// Multi-select takes a list.
	q4 := ask(map[string]any{"prompt": "Which checks?", "multi_select": true, "options": []map[string]any{{"label": "lint"}, {"label": "test"}, {"label": "e2e"}}})
	relay(t)
	say(t, psid, "1, 3")
	if sel := question(q4)["answer"].(map[string]any)["selected"].([]any); len(sel) != 2 || sel[0] != "lint" || sel[1] != "e2e" {
		t.Fatalf("multi: %v", sel)
	}
	// /skip dismisses.
	q5 := ask(map[string]any{"prompt": "Rename it?", "options": []map[string]any{{"label": "Yes"}, {"label": "No"}}})
	relay(t)
	say(t, psid, "/skip")
	if q := question(q5); q["status"] != "dismissed" {
		t.Fatalf("/skip: %v", q)
	}
	// /quit asks first.
	from = g.mark()
	say(t, psid, "/quit")
	if txt := lastText(t, g, from); !strings.Contains(txt, "/quit!") {
		t.Fatalf("/quit: %q", txt)
	}
	// Unknown slash commands go to the agent.
	say(t, psid, "/tasks")
	msgs = a.must(http.StatusOK, "GET", "/api/v1/threads/ext:"+ext+"/messages?sender=user", nil)["messages"].([]any)
	if msgs[len(msgs)-1].(map[string]any)["body"] != "/tasks" {
		t.Fatalf("/tasks not forwarded: %v", msgs[len(msgs)-1])
	}
}

func TestMessengerNewSession(t *testing.T) {
	b, a := setup(t)
	g := withMessenger(t)
	psid := "psid-" + uuid.NewString()[:8]
	linkMessenger(t, g, psid)

	// An eagent-like connector offering session.start.
	grant := control.Grant{Key: "project-" + uuid.NewString()[:8], Label: "Project", Scope: "project", Operations: []string{"settings.apply", "session.start"}, Classes: []string{"preference", "cost"}}
	out := a.must(201, "POST", "/api/v1/connectors", map[string]any{"name": "eagent · test", "provider": "eagent", "requested_grants": []control.Grant{grant}})
	id := str(sub(out, "connector"), "id")
	conn := &client{t: t, http: &http.Client{}, token: str(out, "secret")}
	b.must(200, "POST", "/api/v1/connectors/"+id+"/approve", map[string]any{"grants": []control.Grant{grant}})
	instance := uuid.NewString()
	conn.must(200, "POST", "/api/v1/connectors/"+id+"/heartbeat", map[string]any{"instance": instance})
	descriptor := control.Descriptor{Format: control.Format, SchemaVersion: "eagent/1", AdapterVersion: "1", Fields: []control.Field{}, Actions: []control.Action{{
		Operation: "session.start", Label: "Start a session", Class: "cost",
		Parameters: control.Shape{Type: "object", Properties: map[string]control.Shape{"prompt": {Type: "string", MaxLength: 32768}, "cwd": {Type: "string", MaxLength: 4096}}, Required: []string{"prompt"}},
	}}}
	snapshot := control.Snapshot{Version: "v1", Context: "/home/eric/proj", Saved: map[string]any{}, Effective: map[string]any{}, Details: map[string]any{"directories": map[string]any{"root": "/home/eric/proj", "recent": []string{"/home/eric/proj/web"}}}}
	published := conn.must(200, "PUT", "/api/v1/connectors/"+id+"/resources/"+grant.Key, map[string]any{"instance": instance, "descriptor": descriptor, "snapshot": snapshot})
	resource := str(sub(published, "resource"), "id")

	say(t, psid, "/new")
	// With other test connectors around the chat may ask for the project
	// first; the tap works at either step.
	tap(t, psid, "Project", "np:"+resource)
	from := g.mark()
	tap(t, psid, "2 web", "nd:1")
	if txt := lastText(t, g, from); !strings.Contains(txt, "~/proj/web") || !strings.Contains(txt, "What should the session do?") {
		t.Fatalf("dir step: %q", txt)
	}
	say(t, psid, "Add a dark mode toggle")

	// The integration claims the command and reports its thread.
	claimed := conn.must(200, "POST", "/api/v1/connectors/"+id+"/commands/claim", map[string]any{"instance": instance})
	cmd := sub(claimed, "command")
	params := sub(cmd, "proposal")["parameters"].(map[string]any)
	if params["prompt"] != "Add a dark mode toggle" || params["cwd"] != "/home/eric/proj/web" {
		t.Fatalf("session.start parameters: %v", params)
	}
	session := "eagent:" + uuid.NewString()[:8]
	conn.must(200, "POST", "/api/v1/commands/"+str(cmd, "id")+"/result", map[string]any{"claim_token": str(cmd, "claim_token"), "instance": instance, "status": "succeeded",
		"result": map[string]any{"thread": "ext:" + session, "interactive": true, "session_id": "x"}})
	a.must(http.StatusCreated, "POST", "/api/v1/threads/ext:"+url.PathEscape(session)+"/messages", map[string]any{"title": "web", "agent": "eagent", "sender": "user", "body": "Add a dark mode toggle"})

	deadline := time.Now().Add(5 * time.Second)
	for {
		txt := lastText(t, g, from)
		if strings.Contains(txt, "▶ Started [#") && strings.Contains(txt, "~/proj/web") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no start confirmation; last: %q", txt)
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Replies now go to the new session.
	say(t, psid, "use the system setting by default")
	msgs := a.must(http.StatusOK, "GET", "/api/v1/threads/ext:"+url.PathEscape(session)+"/messages?sender=user", nil)["messages"].([]any)
	if msgs[len(msgs)-1].(map[string]any)["body"] != "use the system setting by default" {
		t.Fatalf("reply after start went elsewhere: %v", msgs[len(msgs)-1])
	}
	b.must(200, "DELETE", "/api/v1/connectors/"+id, nil)
}

func TestMessengerPauseResume(t *testing.T) {
	b, a := setup(t)
	g := withMessenger(t)
	psid := "psid-" + uuid.NewString()[:8]
	linkMessenger(t, g, psid)
	ext := "msgp-" + uuid.NewString()[:8]

	say(t, psid, "/pause")
	if st := b.must(http.StatusOK, "GET", "/api/v1/messenger", nil); sub(st, "link")["paused_at"] == nil {
		t.Fatalf("pause not reported: %v", st)
	}
	a.must(http.StatusCreated, "POST", "/api/v1/threads/ext:"+ext+"/messages", map[string]any{"title": "paused", "body": "while you were at your desk"})
	q := str(sub(a.must(http.StatusCreated, "POST", "/api/v1/threads/ext:"+ext+"/questions", map[string]any{"prompt": "Still need this?", "options": []map[string]any{{"label": "Yes"}}}), "question"), "id")
	from := g.mark()
	relay(t)
	if n := len(g.texts(from)); n != 0 {
		t.Fatalf("relayed %d messages while paused", n)
	}
	// Resuming keeps the link, skips what was said meanwhile, and re-sends
	// the open question.
	from = g.mark()
	say(t, psid, "/resume")
	relay(t)
	got := g.texts(from)
	sawQuestion := false
	for _, m := range got {
		if strings.Contains(m.Text, "while you were at your desk") {
			t.Fatalf("replayed a message from the pause: %q", m.Text)
		}
		if strings.Contains(m.Text, "Still need this?") {
			sawQuestion = true
			if m.Quick[0].Payload != "a:"+q+":0" {
				t.Fatalf("re-sent question buttons: %+v", m.Quick)
			}
		}
	}
	if !sawQuestion || !strings.HasPrefix(got[0].Text, "Resumed") {
		t.Fatalf("resume: %+v", got)
	}
	a.must(http.StatusCreated, "POST", "/api/v1/threads/ext:"+ext+"/messages", map[string]any{"body": "after resume"})
	from = g.mark()
	relay(t)
	if txt := lastText(t, g, from); !strings.Contains(txt, "after resume") {
		t.Fatalf("relay after resume: %q", txt)
	}
}

func TestMessengerFiles(t *testing.T) {
	_, a := setup(t)
	g := withMessenger(t)
	psid := "psid-" + uuid.NewString()[:8]
	linkMessenger(t, g, psid)
	ext := "msgf-" + uuid.NewString()[:8]
	png := makePNG(t, 40, 30)

	// An agent's screenshot arrives as an image after the text.
	postFile := func(body string) {
		t.Helper()
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		_ = w.WriteField("body", body)
		_ = w.WriteField("title", "files")
		fw, _ := w.CreateFormFile("file", "shot.png")
		_, _ = fw.Write(png)
		_ = w.Close()
		req, _ := http.NewRequest("POST", testSrv.URL+"/api/v1/threads/ext:"+ext+"/messages", &buf)
		req.Header.Set("Authorization", "Bearer "+a.token)
		req.Header.Set("Content-Type", w.FormDataContentType())
		res, err := http.DefaultClient.Do(req)
		if err != nil || res.StatusCode != http.StatusCreated {
			t.Fatalf("multipart post: %v %v", err, res.StatusCode)
		}
		res.Body.Close()
	}
	postFile("Here is the new layout.")
	from := g.mark()
	relay(t)
	got := g.texts(from)
	if len(got) != 2 || !strings.Contains(got[0].Text, "📎 shot.png") || got[1].Attachment != "image:shot.png:"+strconv.Itoa(len(png)) {
		t.Fatalf("relayed file: %+v", got)
	}
	// A photo from Messenger becomes an attachment on the user's message.
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer cdn.Close()
	if code := hook(t, psid, map[string]any{"text": "the bug, see top left", "attachments": []any{map[string]any{"type": "image", "payload": map[string]any{"url": cdn.URL + "/v/t1/IMG_0421.png?stp=dst&oh=abc"}}}}, nil); code != 200 {
		t.Fatal(code)
	}
	msgs := a.must(http.StatusOK, "GET", "/api/v1/threads/ext:"+ext+"/messages?sender=user", nil)["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	atts := last["attachments"].([]any)
	if last["body"] != "the bug, see top left" || last["origin"] != "session" || len(atts) != 1 {
		t.Fatalf("inbound photo message: %v", last)
	}
	att := atts[0].(map[string]any)
	if att["kind"] != "image" || att["filename"] != "IMG_0421.png" {
		t.Fatalf("inbound attachment: %v", att)
	}
	// The thumbs-up button (a sticker) reads as 👍.
	hook(t, psid, map[string]any{"attachments": []any{map[string]any{"type": "image", "payload": map[string]any{"url": cdn.URL + "/sticker.png", "sticker_id": 369239263222822}}}}, nil)
	msgs = a.must(http.StatusOK, "GET", "/api/v1/threads/ext:"+ext+"/messages?sender=user", nil)["messages"].([]any)
	if body := msgs[len(msgs)-1].(map[string]any)["body"]; body != "👍" {
		t.Fatalf("sticker: %v", body)
	}
}
