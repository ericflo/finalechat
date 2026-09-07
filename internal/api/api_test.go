package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ericflo/finalechat/internal/bus"
	"github.com/ericflo/finalechat/internal/config"
	"github.com/ericflo/finalechat/internal/db"
	"github.com/ericflo/finalechat/internal/push"
	"github.com/ericflo/finalechat/internal/store"
)

// The suite runs against a throwaway PostgreSQL named by
// FINALECHAT_TEST_DATABASE_URL and is skipped without it. Every run resets the
// public schema, so never point it at a database you care about.

var (
	testPool *pgxpool.Pool
	testSrv  *httptest.Server
	testAPI  *Server
)

func TestMain(m *testing.M) {
	url := os.Getenv("FINALECHAT_TEST_DATABASE_URL")
	if url == "" {
		fmt.Println("FINALECHAT_TEST_DATABASE_URL not set; skipping integration tests")
		os.Exit(0)
	}
	ctx, cancel := context.WithCancel(context.Background())
	pool, err := db.Open(ctx, url)
	if err != nil {
		fmt.Println("open test database:", err)
		os.Exit(1)
	}
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		fmt.Println("reset schema:", err)
		os.Exit(1)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := db.Migrate(ctx, pool, log); err != nil {
		fmt.Println("migrate:", err)
		os.Exit(1)
	}
	cfg := config.Config{
		Addr: ":0", DatabaseURL: url, BaseURL: "http://finalechat.test", SecureCookies: false, TrustProxy: false,
		SessionTTL: time.Hour, LogLevel: "error", Version: "test", VAPIDSubject: "mailto:test@example.com",
	}
	testPool = pool
	st := store.New(pool)
	b := bus.New(pool, log)
	go b.Run(ctx)
	sender := push.New(st, log, "", "", cfg.VAPIDSubject)
	testAPI = New(cfg, st, b, sender, log)
	testSrv = httptest.NewServer(testAPI.Handler())
	// Give the LISTEN connection a moment to attach so events are not lost.
	time.Sleep(200 * time.Millisecond)
	code := m.Run()
	testSrv.Close()
	cancel()
	pool.Close()
	os.Exit(code)
}

// client is a small JSON HTTP helper bound to either a cookie jar or a token.
type client struct {
	t     *testing.T
	http  *http.Client
	token string
}

func newBrowser(t *testing.T) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, http: &http.Client{Jar: jar}}
}

func (c *client) do(method, path string, body any, headers ...string) (int, map[string]any) {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, testSrv.URL+path, reader)
	if err != nil {
		c.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	} else if method != http.MethodGet {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(res.Body)
	if len(raw) > 0 && strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(raw, &out)
	}
	if out == nil {
		out = map[string]any{"_raw": string(raw)}
	}
	return res.StatusCode, out
}

func (c *client) must(status int, method, path string, body any) map[string]any {
	c.t.Helper()
	got, out := c.do(method, path, body)
	if got != status {
		c.t.Fatalf("%s %s: want %d got %d: %v", method, path, status, got, out)
	}
	return out
}

func sub(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}

func str(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

var (
	setupOnce sync.Once
	agent     *client
	browser   *client
)

// setup registers the first account, mints a token, and returns clients for
// both the app (cookie) and an agent (bearer token).
func setup(t *testing.T) (*client, *client) {
	t.Helper()
	setupOnce.Do(func() {
		b := newBrowser(t)
		out := b.must(http.StatusCreated, "POST", "/api/v1/auth/register", map[string]any{"email": "eric@example.com", "password": "correct-horse-battery", "display_name": "Eric"})
		if str(sub(out, "user"), "email") != "eric@example.com" {
			t.Fatalf("unexpected register response: %v", out)
		}
		tok := b.must(http.StatusCreated, "POST", "/api/v1/tokens", map[string]any{"name": "test"})
		agent = &client{t: t, http: &http.Client{}, token: str(tok, "secret")}
		browser = b
	})
	agent.t, browser.t = t, t
	return browser, agent
}

func TestSignupClosesAfterFirstAccount(t *testing.T) {
	setup(t)
	anon := newBrowser(t)
	status, out := anon.do("POST", "/api/v1/auth/register", map[string]any{"email": "second@example.com", "password": "correct-horse-battery"})
	if status != http.StatusForbidden || str(sub(out, "error"), "code") != "signup_closed" {
		t.Fatalf("expected signup_closed, got %d %v", status, out)
	}
	status, out = anon.do("GET", "/api/v1/auth/status", nil)
	if status != 200 || out["signup"] != "closed" || out["authenticated"] != false {
		t.Fatalf("unexpected status: %d %v", status, out)
	}
}

func TestInviteCodeGatesEvenTheFirstAccount(t *testing.T) {
	setup(t)
	testAPI.cfg.InviteCode = "let-me-in"
	defer func() { testAPI.cfg.InviteCode = "" }()
	anon := newBrowser(t)
	status, out := anon.do("GET", "/api/v1/auth/status", nil)
	if status != 200 || out["signup"] != "invite" {
		t.Fatalf("expected invite mode, got %d %v", status, out)
	}
	status, out = anon.do("POST", "/api/v1/auth/register", map[string]any{"email": "third@example.com", "password": "correct-horse-battery", "invite_code": "wrong"})
	if status != http.StatusForbidden || str(sub(out, "error"), "code") != "invalid_invite" {
		t.Fatalf("expected invalid_invite, got %d %v", status, out)
	}
	status, _ = anon.do("POST", "/api/v1/auth/register", map[string]any{"email": "third@example.com", "password": "correct-horse-battery", "invite_code": "let-me-in"})
	if status != http.StatusCreated {
		t.Fatalf("expected registration with the invite code, got %d", status)
	}
	if _, err := testPool.Exec(context.Background(), "DELETE FROM users WHERE email = 'third@example.com'"); err != nil {
		t.Fatal(err)
	}
}

func TestLoginAndCSRF(t *testing.T) {
	setup(t)
	b := newBrowser(t)
	status, out := b.do("POST", "/api/v1/auth/login", map[string]any{"email": "eric@example.com", "password": "wrong"})
	if status != http.StatusUnauthorized || str(sub(out, "error"), "code") != "invalid_credentials" {
		t.Fatalf("expected invalid_credentials, got %d %v", status, out)
	}
	b.must(http.StatusOK, "POST", "/api/v1/auth/login", map[string]any{"email": "Eric@Example.com", "password": "correct-horse-battery"})
	b.must(http.StatusOK, "GET", "/api/v1/me", nil)

	// A cookie-authenticated mutation from another site is rejected.
	status, out = b.do("POST", "/api/v1/threads", map[string]any{"title": "x"}, "Sec-Fetch-Site", "cross-site")
	if status != http.StatusForbidden || str(sub(out, "error"), "code") != "csrf" {
		t.Fatalf("expected csrf rejection, got %d %v", status, out)
	}
	status, _ = b.do("POST", "/api/v1/threads", map[string]any{"title": "x"}, "Sec-Fetch-Site", "", "Origin", "https://evil.example")
	if status != http.StatusForbidden {
		t.Fatalf("expected origin rejection, got %d", status)
	}
	b.must(http.StatusOK, "POST", "/api/v1/auth/logout", map[string]any{})
	status, _ = b.do("GET", "/api/v1/me", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("expected 401 after logout, got %d", status)
	}
}

func TestTokenAuth(t *testing.T) {
	_, a := setup(t)
	out := a.must(http.StatusOK, "GET", "/api/v1/me", nil)
	if out["auth"] != "token" || str(sub(out, "token"), "name") != "test" {
		t.Fatalf("unexpected me: %v", out)
	}
	bad := &client{t: t, http: &http.Client{}, token: "fc_" + strings.Repeat("x", 40)}
	status, res := bad.do("GET", "/api/v1/me", nil)
	if status != http.StatusUnauthorized || str(sub(res, "error"), "code") != "invalid_token" {
		t.Fatalf("expected invalid_token, got %d %v", status, res)
	}
	malformed := &client{t: t, http: &http.Client{}, token: "nope"}
	if status, _ := malformed.do("GET", "/api/v1/me", nil); status != http.StatusUnauthorized {
		t.Fatalf("expected 401 for malformed token, got %d", status)
	}
	// Tokens cannot mint tokens.
	if status, _ := a.do("POST", "/api/v1/tokens", map[string]any{"name": "escalate"}); status != http.StatusForbidden {
		t.Fatalf("expected 403 creating a token with a token, got %d", status)
	}
	// Unknown authenticated routes are JSON 404s.
	status, res = a.do("GET", "/api/v1/nope", nil)
	if status != http.StatusNotFound || str(sub(res, "error"), "code") != "not_found" {
		t.Fatalf("expected JSON 404, got %d %v", status, res)
	}
}

func TestThreadsAndMessages(t *testing.T) {
	b, a := setup(t)
	created := a.must(http.StatusCreated, "POST", "/api/v1/threads", map[string]any{"external_id": "claude-code:s1", "title": "proj", "agent": "Claude Code", "meta": map[string]any{"cwd": "/tmp"}})
	thread := sub(created, "thread")
	id := str(thread, "id")
	if created["created"] != true || id == "" {
		t.Fatalf("unexpected create: %v", created)
	}
	again := a.must(http.StatusOK, "POST", "/api/v1/threads", map[string]any{"external_id": "claude-code:s1", "title": "ignored"})
	if again["created"] != false || str(sub(again, "thread"), "id") != id || str(sub(again, "thread"), "title") != "proj" {
		t.Fatalf("expected idempotent create, got %v", again)
	}

	// ext: references resolve, and posting auto-creates unknown ones.
	a.must(http.StatusOK, "GET", "/api/v1/threads/ext:claude-code:s1", nil)
	auto := a.must(http.StatusCreated, "POST", "/api/v1/threads/ext:codex:9/messages", map[string]any{"body": "hi", "title": "auto", "agent": "Codex"})
	if str(sub(auto, "thread"), "title") != "auto" || str(sub(auto, "message"), "sender") != "agent" {
		t.Fatalf("expected auto-created named thread, got %v", auto)
	}
	if status, _ := a.do("GET", "/api/v1/threads/ext:missing", nil); status != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown ext thread on GET, got %d", status)
	}

	// Messages, previews, unread counts and pagination.
	for i := 1; i <= 5; i++ {
		body := fmt.Sprintf("message **%d**", i)
		importance := "normal"
		if i == 5 {
			importance = "important"
		}
		a.must(http.StatusCreated, "POST", "/api/v1/threads/"+id+"/messages", map[string]any{"body": body, "importance": importance})
	}
	got := a.must(http.StatusOK, "GET", "/api/v1/threads/"+id, nil)
	th := sub(got, "thread")
	if th["unread_count"].(float64) != 5 || th["preview"] != "message 5" || th["preview_sender"] != "agent" {
		t.Fatalf("unexpected thread state: %v", th)
	}
	page := a.must(http.StatusOK, "GET", "/api/v1/threads/"+id+"/messages?limit=2", nil)
	msgs := page["messages"].([]any)
	if len(msgs) != 2 || page["has_more"] != true || str(msgs[1].(map[string]any), "body") != "message **5**" {
		t.Fatalf("unexpected newest page: %v", page)
	}
	first := str(msgs[0].(map[string]any), "id")
	older := a.must(http.StatusOK, "GET", "/api/v1/threads/"+id+"/messages?before="+first+"&limit=10", nil)
	if n := len(older["messages"].([]any)); n != 3 || older["has_more"] != false {
		t.Fatalf("expected 3 older messages, got %d (%v)", n, older["has_more"])
	}
	last := str(msgs[1].(map[string]any), "id")
	newer := a.must(http.StatusOK, "GET", "/api/v1/threads/"+id+"/messages?after="+last, nil)
	if n := len(newer["messages"].([]any)); n != 0 {
		t.Fatalf("expected no newer messages, got %d", n)
	}

	// The user replies from the app; the thread becomes read.
	reply := b.must(http.StatusCreated, "POST", "/api/v1/threads/"+id+"/messages", map[string]any{"body": "on it"})
	if str(sub(reply, "message"), "sender") != "user" || sub(reply, "thread")["unread_count"].(float64) != 0 {
		t.Fatalf("unexpected reply: %v", reply)
	}
	newer = a.must(http.StatusOK, "GET", "/api/v1/threads/"+id+"/messages?after="+last+"&sender=user", nil)
	if n := len(newer["messages"].([]any)); n != 1 {
		t.Fatalf("expected the user reply, got %d", n)
	}

	// Validation.
	if status, out := a.do("POST", "/api/v1/threads/"+id+"/messages", map[string]any{"body": "   "}); status != http.StatusUnprocessableEntity || str(sub(out, "error"), "code") != "validation_failed" {
		t.Fatalf("expected validation failure, got %d %v", status, out)
	}
	if status, _ := a.do("POST", "/api/v1/threads/"+id+"/messages", map[string]any{"body": "x", "importance": "urgent"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("expected validation failure for importance, got %d", status)
	}

	// Archive, list filters, delete.
	a.must(http.StatusOK, "PATCH", "/api/v1/threads/"+id, map[string]any{"archived": true, "muted": true})
	active := a.must(http.StatusOK, "GET", "/api/v1/threads", nil)
	for _, it := range active["threads"].([]any) {
		if str(it.(map[string]any), "id") == id {
			t.Fatal("archived thread listed as active")
		}
	}
	archived := a.must(http.StatusOK, "GET", "/api/v1/threads?archived=1", nil)
	if n := len(archived["threads"].([]any)); n != 1 {
		t.Fatalf("expected one archived thread, got %d", n)
	}
	// A new message un-archives.
	a.must(http.StatusCreated, "POST", "/api/v1/threads/"+id+"/messages", map[string]any{"body": "back"})
	if th := sub(a.must(http.StatusOK, "GET", "/api/v1/threads/"+id, nil), "thread"); th["archived_at"] != nil {
		t.Fatalf("expected un-archived thread, got %v", th)
	}
	a.must(http.StatusOK, "DELETE", "/api/v1/threads/ext:codex:9", nil)
	if status, _ := a.do("GET", "/api/v1/threads/ext:codex:9", nil); status != http.StatusNotFound {
		t.Fatalf("expected deleted thread to be gone, got %d", status)
	}
}

func TestQuestionLifecycleWithLongPoll(t *testing.T) {
	b, a := setup(t)
	thread := str(sub(a.must(http.StatusCreated, "POST", "/api/v1/threads", map[string]any{"external_id": "q:1", "title": "questions"}), "thread"), "id")

	// Ask with wait; answer from the app while the request is blocked.
	type result struct {
		status int
		out    map[string]any
		took   time.Duration
	}
	done := make(chan result, 1)
	go func() {
		c := &client{t: t, http: &http.Client{}, token: a.token}
		start := time.Now()
		status, out := c.do("POST", "/api/v1/threads/"+thread+"/questions?wait=20", map[string]any{
			"prompt": "Ship it?", "options": []map[string]any{{"label": "Yes", "description": "go"}, {"label": "No"}},
		})
		done <- result{status, out, time.Since(start)}
	}()

	var pending map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out := b.must(http.StatusOK, "GET", "/api/v1/questions?status=pending", nil)
		if qs := out["questions"].([]any); len(qs) > 0 {
			pending = qs[0].(map[string]any)
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if pending == nil {
		t.Fatal("question never became pending")
	}
	qid := str(pending, "id")
	if status, out := b.do("POST", "/api/v1/questions/"+qid+"/answer", map[string]any{"selected": []string{"Maybe"}}); status != http.StatusUnprocessableEntity {
		t.Fatalf("expected validation failure for unknown option, got %d %v", status, out)
	}
	if status, _ := b.do("POST", "/api/v1/questions/"+qid+"/answer", map[string]any{"selected": []string{"Yes", "No"}}); status != http.StatusUnprocessableEntity {
		t.Fatalf("expected single-select violation, got %d", status)
	}
	answered := b.must(http.StatusOK, "POST", "/api/v1/questions/"+qid+"/answer", map[string]any{"selected": []string{"Yes"}, "text": "carefully"})
	if str(sub(answered, "question"), "status") != "answered" || str(sub(answered, "message"), "body") != "Yes — carefully" {
		t.Fatalf("unexpected answer response: %v", answered)
	}

	r := <-done
	if r.status != http.StatusCreated || str(sub(r.out, "question"), "status") != "answered" {
		t.Fatalf("long-poll did not observe the answer: %d %v", r.status, r.out)
	}
	if r.took > 5*time.Second {
		t.Fatalf("long-poll took too long: %s", r.took)
	}
	ans := sub(sub(r.out, "question"), "answer")
	if sel := ans["selected"].([]any); len(sel) != 1 || sel[0] != "Yes" || ans["text"] != "carefully" {
		t.Fatalf("unexpected answer payload: %v", ans)
	}

	// Answering twice is rejected; cancelling an answered question too.
	if status, out := b.do("POST", "/api/v1/questions/"+qid+"/answer", map[string]any{"selected": []string{"No"}}); status != http.StatusConflict || str(sub(out, "error"), "code") != "already_resolved" {
		t.Fatalf("expected already_resolved, got %d %v", status, out)
	}
	if status, out := a.do("POST", "/api/v1/questions/"+qid+"/cancel", map[string]any{}); status != http.StatusConflict || str(sub(out, "error"), "code") != "already_resolved" {
		t.Fatalf("expected already_resolved cancelling an answered question, got %d %v", status, out)
	}
	if got := a.must(http.StatusOK, "GET", "/api/v1/questions?thread_id=ext:q:1&status=answered", nil); len(got["questions"].([]any)) != 1 {
		t.Fatalf("expected ext: thread filter to work, got %v", got)
	}

	// A timed-out wait returns the still-pending question.
	out := a.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/questions", map[string]any{"prompt": "free text?", "wait": 1})
	if str(sub(out, "question"), "status") != "pending" || sub(out, "question")["allow_freeform"] != true {
		t.Fatalf("expected pending free-form question, got %v", out)
	}
	q2 := str(sub(out, "question"), "id")
	if status, _ := b.do("POST", "/api/v1/questions/"+q2+"/answer", map[string]any{"selected": []string{}}); status != http.StatusUnprocessableEntity {
		t.Fatalf("expected empty answer rejection, got %d", status)
	}
	cancelled := a.must(http.StatusOK, "POST", "/api/v1/questions/"+q2+"/cancel", map[string]any{})
	if str(sub(cancelled, "question"), "status") != "cancelled" {
		t.Fatalf("expected cancelled, got %v", cancelled)
	}

	// Expiry is applied by maintenance.
	out = a.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/questions", map[string]any{"prompt": "quick", "timeout_seconds": 1})
	q3 := str(sub(out, "question"), "id")
	time.Sleep(1100 * time.Millisecond)
	expired, err := testAPI.store.ExpireQuestions(context.Background())
	if err != nil || len(expired) != 1 || expired[0].ID.String() != q3 {
		t.Fatalf("expected one expired question, got %v err=%v", expired, err)
	}
	counts := sub(a.must(http.StatusOK, "GET", "/api/v1/counts", nil), "counts")
	if counts["pending_questions"].(float64) != 0 {
		t.Fatalf("expected no pending questions, got %v", counts)
	}
}

func TestMessageLongPollAndEvents(t *testing.T) {
	b, a := setup(t)
	thread := str(sub(a.must(http.StatusCreated, "POST", "/api/v1/threads", map[string]any{"external_id": "lp:1", "title": "longpoll"}), "thread"), "id")
	seed := a.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "done, waiting"})
	last := str(sub(seed, "message"), "id")

	// Open the event stream before acting so we can assert on ordering.
	req, _ := http.NewRequest("GET", testSrv.URL+"/api/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+a.token)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	events := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "event: ") {
				events <- strings.TrimPrefix(line, "event: ")
			}
		}
		close(events)
	}()
	if ev := <-events; ev != "ready" {
		t.Fatalf("expected ready event first, got %q", ev)
	}

	// A wait without an anchor waits for anything new from now on.
	if out := a.must(http.StatusOK, "GET", "/api/v1/threads/"+thread+"/messages?wait=1", nil); out["timed_out"] != true || len(out["messages"].([]any)) != 0 {
		t.Fatalf("expected an empty timed-out wait, got %v", out)
	}

	type result struct {
		out  map[string]any
		took time.Duration
	}
	done := make(chan result, 1)
	go func() {
		c := &client{t: t, http: &http.Client{}, token: a.token}
		start := time.Now()
		_, out := c.do("GET", "/api/v1/threads/"+thread+"/messages?after="+last+"&sender=user&wait=20", nil)
		done <- result{out, time.Since(start)}
	}()
	time.Sleep(300 * time.Millisecond)
	b.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "ship it"})
	r := <-done
	msgs := r.out["messages"].([]any)
	if len(msgs) != 1 || str(msgs[0].(map[string]any), "body") != "ship it" || r.took > 5*time.Second {
		t.Fatalf("long-poll did not return the reply promptly: %v (%s)", r.out, r.took)
	}

	// Timed-out waits say so.
	out := a.must(http.StatusOK, "GET", "/api/v1/threads/"+thread+"/messages?after="+str(msgs[0].(map[string]any), "id")+"&wait=1", nil)
	if out["timed_out"] != true {
		t.Fatalf("expected timed_out, got %v", out)
	}

	// The stream saw the message events.
	saw := map[string]bool{}
	timeout := time.After(3 * time.Second)
	for len(saw) < 1 {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event stream closed early")
			}
			if ev == "message.created" {
				saw[ev] = true
			}
		case <-timeout:
			t.Fatalf("did not see message.created on the event stream; saw %v", saw)
		}
	}
}

func TestSettingsAndPushEndpoints(t *testing.T) {
	b, a := setup(t)
	out := b.must(http.StatusOK, "PATCH", "/api/v1/settings", map[string]any{"remote_mode": true})
	if sub(out, "settings")["remote_mode"] != true {
		t.Fatalf("expected remote_mode on, got %v", out)
	}
	me := a.must(http.StatusOK, "GET", "/api/v1/me", nil)
	if sub(sub(me, "user"), "settings")["remote_mode"] != true {
		t.Fatalf("agent does not see remote_mode: %v", me)
	}
	b.must(http.StatusOK, "PATCH", "/api/v1/settings", map[string]any{"remote_mode": false})

	// Push is disabled in tests; the API says so cleanly.
	vapid := b.must(http.StatusOK, "GET", "/api/v1/push/vapid", nil)
	if vapid["enabled"] != false {
		t.Fatalf("expected push disabled, got %v", vapid)
	}
	if status, out := b.do("POST", "/api/v1/push/subscriptions", map[string]any{"endpoint": "https://push.example/x", "keys": map[string]any{"p256dh": "a", "auth": "b"}}); status != http.StatusServiceUnavailable || str(sub(out, "error"), "code") != "push_disabled" {
		t.Fatalf("expected push_disabled, got %d %v", status, out)
	}
}

func TestDocumentsAndStatic(t *testing.T) {
	for _, path := range []string{"/AGENTS.md", "/llms.txt", "/api/", "/api/openapi.json", "/install.sh", "/cli/finalechat", "/skill/SKILL.md", "/healthz", "/readyz"} {
		res, err := http.Get(testSrv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", path, res.StatusCode)
		}
		if strings.Contains(string(body), "https://www.finalechat.com") {
			t.Fatalf("%s: base URL was not rewritten for this deployment", path)
		}
	}
	res, err := http.Get(testSrv.URL + "/api")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.Request.URL.Path != "/api/" {
		t.Fatalf("expected /api to redirect to /api/, ended at %s", res.Request.URL.Path)
	}
}
