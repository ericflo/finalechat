package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	pngenc "image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ericflo/finalechat/internal/blob"
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
	testAPI = New(cfg, st, b, sender, blob.NewMemory(), log)
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
	testAPI.cfg.Signup, testAPI.cfg.InviteCode = "invite", "let-me-in"
	defer func() { testAPI.cfg.Signup, testAPI.cfg.InviteCode = "", "" }()
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
	// A plain agent message leaves an archived thread archived (the user put
	// it away); an important one brings it back.
	a.must(http.StatusCreated, "POST", "/api/v1/threads/"+id+"/messages", map[string]any{"body": "still going"})
	if th := sub(a.must(http.StatusOK, "GET", "/api/v1/threads/"+id, nil), "thread"); th["archived_at"] == nil {
		t.Fatalf("a plain message should not un-archive, got %v", th)
	}
	a.must(http.StatusCreated, "POST", "/api/v1/threads/"+id+"/messages", map[string]any{"body": "back", "importance": "important"})
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
	if status, out := b.do("POST", "/api/v1/push/subscriptions", map[string]any{"endpoint": "https://fcm.googleapis.com/fcm/send/x", "keys": map[string]any{"p256dh": "a", "auth": "b"}}); status != http.StatusServiceUnavailable || str(sub(out, "error"), "code") != "push_disabled" {
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
	// Every document a browser can render carries the app's policy, the
	// shell served at /api/ included; API responses do not.
	csp := func(path, accept string) string {
		req, _ := http.NewRequest("GET", testSrv.URL+path, nil)
		req.Header.Set("Accept", accept)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.Header.Get("Content-Security-Policy")
	}
	root := csp("/", "text/html")
	if root == "" || !strings.Contains(root, "default-src 'self'") {
		t.Fatalf("app shell without a CSP: %q", root)
	}
	for _, path := range []string{"/api/", "/t/01a07e1e-0000-7000-8000-000000000000", "/AGENTS.md"} {
		if got := csp(path, "text/html"); got != root {
			t.Errorf("%s: CSP %q differs from the shell's", path, got)
		}
	}
	if got := csp("/api/v1/auth/status", "application/json"); got != "" {
		t.Errorf("API response carries the app CSP: %q", got)
	}
}

// A mirrored terminal prompt (a "user" message posted by an agent) must not
// pull a thread the user archived back into the inbox; only the user's own
// reply from the app does.
func TestMirroredPromptKeepsArchive(t *testing.T) {
	b, a := setup(t)
	thread := str(sub(a.must(http.StatusCreated, "POST", "/api/v1/threads", map[string]any{"external_id": "arch:1", "title": "archive"}), "thread"), "id")
	a.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "hello"})
	b.must(http.StatusOK, "PATCH", "/api/v1/threads/"+thread, map[string]any{"archived": true})
	out := a.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "ls", "sender": "user", "notify": false})
	if sub(out, "thread")["archived_at"] == nil {
		t.Fatalf("a token-origin user message un-archived the thread: %v", out)
	}
	out = b.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "back"})
	if sub(out, "thread")["archived_at"] != nil {
		t.Fatalf("the user's own reply from the app should un-archive: %v", out)
	}
}

// Option labels are bounded in characters, as documented, not bytes.
func TestQuestionOptionLimitsCountCharacters(t *testing.T) {
	_, a := setup(t)
	thread := str(sub(a.must(http.StatusCreated, "POST", "/api/v1/threads", map[string]any{"external_id": "runes:1"}), "thread"), "id")
	ok := strings.Repeat("é", 199) + "…" // 200 characters, 400 bytes
	a.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/questions", map[string]any{"prompt": "Pick", "options": []map[string]any{{"label": ok}}})
	if status, out := a.do("POST", "/api/v1/threads/"+thread+"/questions", map[string]any{"prompt": "Pick", "options": []map[string]any{{"label": strings.Repeat("a", 201)}}}); status != http.StatusUnprocessableEntity {
		t.Fatalf("201 characters should be rejected, got %d %v", status, out)
	}
}

// An anchor the thread no longer holds (a thread deleted from the app and
// recreated by the agent under the same ext: id) pages from the start and
// says so, instead of matching nothing forever.
func TestUnknownAnchorPagesFromTheStart(t *testing.T) {
	b, a := setup(t)
	first := a.must(http.StatusCreated, "POST", "/api/v1/threads/ext:recreate:1/messages", map[string]any{"body": "first life", "title": "recreate"})
	old := str(sub(first, "message"), "id")
	oldThread := str(sub(first, "thread"), "id")
	b.must(http.StatusCreated, "POST", "/api/v1/threads/"+oldThread+"/messages", map[string]any{"body": "old reply"})
	b.must(http.StatusOK, "DELETE", "/api/v1/threads/"+oldThread, nil)
	second := a.must(http.StatusCreated, "POST", "/api/v1/threads/ext:recreate:1/messages", map[string]any{"body": "second life", "title": "recreate"})
	newThread := str(sub(second, "thread"), "id")
	if newThread == oldThread {
		t.Fatal("expected a new thread after the delete")
	}
	b.must(http.StatusCreated, "POST", "/api/v1/threads/"+newThread+"/messages", map[string]any{"body": "fresh reply"})
	out := a.must(http.StatusOK, "GET", "/api/v1/threads/ext:recreate:1/messages?after="+old+"&sender=user", nil)
	page := out["messages"].([]any)
	if out["anchor_unknown"] != true || len(page) != 1 || str(page[0].(map[string]any), "body") != "fresh reply" {
		t.Fatalf("an unknown anchor should page from the start and say so, got %v", out)
	}
	if out := a.must(http.StatusOK, "GET", "/api/v1/threads/ext:recreate:1/messages?after="+str(page[0].(map[string]any), "id")+"&sender=user&wait=1", nil); out["anchor_unknown"] != nil || len(out["messages"].([]any)) != 0 {
		t.Fatalf("a known anchor must not be flagged: %v", out)
	}
}

func TestAttachments(t *testing.T) {
	b, a := setup(t)
	thread := str(sub(a.must(http.StatusCreated, "POST", "/api/v1/threads", map[string]any{"external_id": "att:1", "title": "attachments"}), "thread"), "id")

	// A tiny PNG (2x2) generated in-process so the test needs no fixtures.
	png := makePNG(t, 900, 300)

	// Raw upload with a declared type and filename.
	req, _ := http.NewRequest("POST", testSrv.URL+"/api/v1/threads/"+thread+"/attachments?filename=shot.png", bytes.NewReader(png))
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "image/png")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var up map[string]any
	_ = json.NewDecoder(res.Body).Decode(&up)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("upload: %d %v", res.StatusCode, up)
	}
	att := up["attachments"].([]any)[0].(map[string]any)
	if att["kind"] != "image" || att["width"].(float64) != 900 || att["thumb_width"].(float64) != 640 || str(att, "thumb_url") == "" {
		t.Fatalf("unexpected attachment metadata: %v", att)
	}
	attID := str(att, "id")

	// Attach it to a message with no body.
	msg := a.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"attachments": []string{attID}})
	m := sub(msg, "message")
	if list := m["attachments"].([]any); len(list) != 1 || str(list[0].(map[string]any), "url") != "/api/v1/attachments/"+attID {
		t.Fatalf("message did not carry the attachment: %v", m)
	}
	if !strings.HasPrefix(str(sub(msg, "thread"), "preview"), "📎") {
		t.Fatalf("expected an attachment preview, got %q", str(sub(msg, "thread"), "preview"))
	}
	// Attaching again fails: it is no longer pending.
	if status, _ := a.do("POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "again", "attachments": []string{attID}}); status != http.StatusUnprocessableEntity {
		t.Fatalf("expected re-attach to be rejected, got %d", status)
	}

	// Download original and thumbnail as the app user.
	for _, suffix := range []string{"", "/thumb"} {
		r2, err := b.http.Get(testSrv.URL + "/api/v1/attachments/" + attID + suffix)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(r2.Body)
		r2.Body.Close()
		if r2.StatusCode != 200 || len(data) == 0 {
			t.Fatalf("download%s: %d", suffix, r2.StatusCode)
		}
		want := "image/png"
		if suffix == "/thumb" {
			want = "image/jpeg"
		}
		if ct := r2.Header.Get("Content-Type"); ct != want {
			t.Fatalf("download%s content type %q", suffix, ct)
		}
		if suffix == "" && !bytes.Equal(data, png) {
			t.Fatal("original bytes differ")
		}
	}

	// Multipart message: text plus two files, one image and one text file.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("body", "here is the **screenshot**")
	fw, _ := mw.CreateFormFile("file", "notes.txt")
	_, _ = fw.Write([]byte("hello\n"))
	fw2, _ := mw.CreateFormFile("file", "two.png")
	_, _ = fw2.Write(makePNG(t, 40, 20))
	mw.Close()
	req, _ = http.NewRequest("POST", testSrv.URL+"/api/v1/threads/"+thread+"/messages", &buf)
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("multipart message: %d %v", res.StatusCode, out)
	}
	list := sub(out, "message")["attachments"].([]any)
	if len(list) != 2 {
		t.Fatalf("expected 2 attachments, got %d", len(list))
	}
	kinds := map[string]string{}
	for _, it := range list {
		x := it.(map[string]any)
		kinds[str(x, "filename")] = str(x, "kind") + ":" + str(x, "content_type")
	}
	if kinds["notes.txt"] != "file:text/plain; charset=utf-8" && kinds["notes.txt"] != "file:text/plain" {
		t.Fatalf("unexpected text attachment classification: %v", kinds)
	}
	if kinds["two.png"] != "image:image/png" {
		t.Fatalf("unexpected image classification: %v", kinds)
	}

	// Listing messages hydrates attachments; deleting the thread removes objects.
	page := a.must(http.StatusOK, "GET", "/api/v1/threads/"+thread+"/messages", nil)
	total := 0
	for _, it := range page["messages"].([]any) {
		total += len(it.(map[string]any)["attachments"].([]any))
	}
	if total != 3 {
		t.Fatalf("expected 3 attachments across messages, got %d", total)
	}
	a.must(http.StatusOK, "DELETE", "/api/v1/threads/"+thread, nil)
	if status, _ := b.do("GET", "/api/v1/attachments/"+attID, nil); status != http.StatusNotFound {
		t.Fatalf("expected deleted attachment to 404, got %d", status)
	}
	if _, _, _, err := testAPI.blobs.Get(context.Background(), "a/"+attID+"/shot.png"); err == nil {
		t.Fatal("object should have been deleted from blob storage")
	}
}

func TestOrphanAttachmentSweep(t *testing.T) {
	_, a := setup(t)
	thread := str(sub(a.must(http.StatusCreated, "POST", "/api/v1/threads", map[string]any{"external_id": "orphan:1", "title": "orphans"}), "thread"), "id")
	req, _ := http.NewRequest("POST", testSrv.URL+"/api/v1/threads/"+thread+"/attachments?filename=stray.png", bytes.NewReader(makePNG(t, 8, 8)))
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "image/png")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var up map[string]any
	_ = json.NewDecoder(res.Body).Decode(&up)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("upload: %d %v", res.StatusCode, up)
	}
	id := str(up["attachments"].([]any)[0].(map[string]any), "id")
	// Fresh uploads survive the sweep; backdated ones are removed with their objects.
	testAPI.pruneOrphanAttachments(context.Background())
	if _, err := testAPI.store.GetAttachment(context.Background(), principalUser(t, a), uuidMust(t, id)); err != nil {
		t.Fatalf("fresh upload was swept: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), "UPDATE attachments SET created_at = now() - interval '2 days' WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	testAPI.pruneOrphanAttachments(context.Background())
	if _, err := testAPI.store.GetAttachment(context.Background(), principalUser(t, a), uuidMust(t, id)); err == nil {
		t.Fatal("stale upload was not swept")
	}
	if _, _, _, err := testAPI.blobs.Get(context.Background(), "a/"+id+"/stray.png"); err == nil {
		t.Fatal("stale object was not deleted")
	}
}

func principalUser(t *testing.T, c *client) uuid.UUID {
	t.Helper()
	me := c.must(http.StatusOK, "GET", "/api/v1/me", nil)
	return uuidMust(t, str(sub(me, "user"), "id"))
}

func uuidMust(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := pngenc.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAgentActivity(t *testing.T) {
	b, a := setup(t)

	// Watch the stream so the activity event can be asserted on.
	req, _ := http.NewRequest("GET", testSrv.URL+"/api/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+a.token)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	events := make(chan string, 32)
	go func() {
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		var name string
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "event: ") {
				name = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") && name != "" {
				events <- name + " " + strings.TrimPrefix(line, "data: ")
				name = ""
			}
		}
		close(events)
	}()
	waitEvent := func(want string) map[string]any {
		t.Helper()
		timeout := time.After(3 * time.Second)
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					t.Fatal("event stream closed early")
				}
				name, data, _ := strings.Cut(ev, " ")
				if name == want {
					var payload map[string]any
					_ = json.Unmarshal([]byte(data), &payload)
					return payload
				}
			case <-timeout:
				t.Fatalf("did not see %s on the event stream", want)
			}
		}
	}
	waitEvent("ready")

	// A status belongs to a conversation that exists: unlike messages, this
	// never creates a thread (a stray hook must not litter the inbox).
	if status, _ := a.do("POST", "/api/v1/threads/ext:act-1/activity", map[string]any{"text": "x"}); status != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown thread, got %d", status)
	}
	a.must(http.StatusCreated, "POST", "/api/v1/threads", map[string]any{"external_id": "act-1", "title": "activity", "agent": "eagent"})
	out := a.must(http.StatusOK, "POST", "/api/v1/threads/ext:act-1/activity", map[string]any{
		"text": "  checking out\nthe files you asked for…  ", "kind": "tool", "ttl_seconds": 30,
	})
	thread := sub(out, "thread")
	act := sub(thread, "activity")
	if out["applied"] != true {
		t.Fatalf("expected the status to be applied: %v", out)
	}
	if str(act, "text") != "checking out the files you asked for…" || str(act, "kind") != "tool" {
		t.Fatalf("unexpected activity: %v", act)
	}
	if act["expires_at"] == nil || act["since"] == nil || act["at"] == nil {
		t.Fatalf("activity is missing timestamps: %v", act)
	}
	ev := waitEvent("thread.activity")
	if str(sub(ev, "activity"), "text") != "checking out the files you asked for…" || str(ev, "thread_id") != str(thread, "id") {
		t.Fatalf("event did not carry the status inline: %v", ev)
	}
	if _, has := ev["counts"]; has {
		t.Fatalf("activity events should not carry counts: %v", ev)
	}
	threadID := str(thread, "id")
	since := str(act, "since")

	// An identical repeat with most of its life left is a no-op; a refresh
	// keeps `since` so the app can say how long the agent has been at it.
	if out := a.must(http.StatusOK, "POST", "/api/v1/threads/"+threadID+"/activity", map[string]any{"text": "checking out the files you asked for…", "kind": "tool", "ttl_seconds": 30}); out["applied"] != false {
		t.Fatalf("expected a repeat to be coalesced: %v", out)
	}
	out = a.must(http.StatusOK, "POST", "/api/v1/threads/"+threadID+"/activity", map[string]any{"text": "running tests"})
	act = sub(sub(out, "thread"), "activity")
	if str(act, "since") != since || str(act, "kind") != "working" || str(act, "text") != "running tests" {
		t.Fatalf("refresh changed since or defaults: %v (want since %s)", act, since)
	}

	// Ordering: concurrent writers pass seq; an older one landing late loses.
	a.must(http.StatusOK, "POST", "/api/v1/threads/"+threadID+"/activity", map[string]any{"text": "Running: go test", "seq": 20})
	if out := a.must(http.StatusOK, "POST", "/api/v1/threads/"+threadID+"/activity", map[string]any{"text": "Thinking…", "seq": 10}); out["applied"] != false || str(sub(sub(out, "thread"), "activity"), "text") != "Running: go test" {
		t.Fatalf("a stale seq should be ignored: %v", out)
	}
	if out := a.must(http.StatusOK, "POST", "/api/v1/threads/"+threadID+"/activity", map[string]any{"text": "Editing main.go", "seq": 30}); out["applied"] != true {
		t.Fatalf("a newer seq should apply: %v", out)
	}
	// Once the live status lapses, any seq is accepted again.
	a.must(http.StatusOK, "DELETE", "/api/v1/threads/"+threadID+"/activity", nil)
	if out := a.must(http.StatusOK, "POST", "/api/v1/threads/"+threadID+"/activity", map[string]any{"text": "fresh session", "seq": 1}); out["applied"] != true {
		t.Fatalf("seq should reset after a clear: %v", out)
	}
	// The app sees it on the thread and in the inbox.
	if got := sub(sub(b.must(http.StatusOK, "GET", "/api/v1/threads/"+threadID, nil), "thread"), "activity"); str(got, "text") != "fresh session" {
		t.Fatalf("thread does not show the status: %v", got)
	}

	// Validation.
	if status, out := a.do("POST", "/api/v1/threads/"+threadID+"/activity", map[string]any{"text": "x", "kind": "dancing"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("expected a validation error for a bad kind, got %d %v", status, out)
	}
	if status, _ := a.do("POST", "/api/v1/threads/"+threadID+"/activity", map[string]any{"text": "x", "ttl_seconds": 0}); status != http.StatusUnprocessableEntity {
		t.Fatalf("expected a validation error for ttl 0, got %d", status)
	}
	if status, _ := a.do("POST", "/api/v1/threads/"+threadID+"/activity", map[string]any{"text": strings.Repeat("x", 201)}); status != http.StatusUnprocessableEntity {
		t.Fatalf("expected a validation error for a long status, got %d", status)
	}

	// The user's reply leaves the status alone; the agent's message clears it
	// unless it carries the next status.
	out = b.must(http.StatusCreated, "POST", "/api/v1/threads/"+threadID+"/messages", map[string]any{"body": "take your time"})
	if sub(sub(out, "thread"), "activity") == nil {
		t.Fatalf("a user message should not clear the agent's status: %v", out)
	}
	out = a.must(http.StatusCreated, "POST", "/api/v1/threads/"+threadID+"/messages", map[string]any{"body": "tests pass, starting the backfill", "activity": map[string]any{"text": "Running the backfill", "kind": "tool", "ttl_seconds": 120}})
	if str(sub(sub(out, "thread"), "activity"), "text") != "Running the backfill" {
		t.Fatalf("a message should be able to carry the next status: %v", out)
	}
	out = a.must(http.StatusCreated, "POST", "/api/v1/threads/"+threadID+"/messages", map[string]any{"body": "tests pass"})
	if sub(out, "thread")["activity"] != nil {
		t.Fatalf("an agent message should clear the status: %v", out)
	}
	if got := sub(b.must(http.StatusOK, "GET", "/api/v1/threads/"+threadID, nil), "thread"); got["activity"] != nil {
		t.Fatalf("status still present after the agent posted: %v", got)
	}

	// A question clears it too.
	a.must(http.StatusOK, "POST", "/api/v1/threads/"+threadID+"/activity", map[string]any{"text": "deciding", "kind": "thinking"})
	out = a.must(http.StatusCreated, "POST", "/api/v1/threads/"+threadID+"/questions", map[string]any{"prompt": "Ship?", "options": []map[string]any{{"label": "Yes"}}})
	if sub(out, "thread")["activity"] != nil {
		t.Fatalf("a question should clear the status: %v", out)
	}

	// Explicit clear, twice (the second is a no-op).
	a.must(http.StatusOK, "POST", "/api/v1/threads/"+threadID+"/activity", map[string]any{"text": "typing", "kind": "typing"})
	if got := sub(a.must(http.StatusOK, "DELETE", "/api/v1/threads/"+threadID+"/activity", nil), "thread"); got["activity"] != nil {
		t.Fatalf("DELETE did not clear the status: %v", got)
	}
	a.must(http.StatusOK, "DELETE", "/api/v1/threads/"+threadID+"/activity", nil)
	// Empty text clears as well.
	a.must(http.StatusOK, "POST", "/api/v1/threads/"+threadID+"/activity", map[string]any{"text": "typing"})
	if got := sub(a.must(http.StatusOK, "POST", "/api/v1/threads/"+threadID+"/activity", map[string]any{"text": ""}), "thread"); got["activity"] != nil {
		t.Fatalf("empty text did not clear the status: %v", got)
	}

	// Expiry: a one-second status is gone after a second, and a later status
	// starts a fresh `since`.
	out = a.must(http.StatusOK, "POST", "/api/v1/threads/"+threadID+"/activity", map[string]any{"text": "blink", "ttl_seconds": 1})
	first := str(sub(sub(out, "thread"), "activity"), "since")
	time.Sleep(1200 * time.Millisecond)
	if got := sub(b.must(http.StatusOK, "GET", "/api/v1/threads/"+threadID, nil), "thread"); got["activity"] != nil {
		t.Fatalf("expired status still shown: %v", got)
	}
	out = a.must(http.StatusOK, "POST", "/api/v1/threads/"+threadID+"/activity", map[string]any{"text": "again"})
	if got := str(sub(sub(out, "thread"), "activity"), "since"); got == first {
		t.Fatalf("since should restart after a lapse: %s", got)
	}

	// Waiting is idle, not busy: crossing into or out of it starts a fresh
	// `since`, so the app's busy timer restarts on the next turn instead of
	// resuming from e.g. a long "Waiting for your reply".
	out = a.must(http.StatusOK, "POST", "/api/v1/threads/"+threadID+"/activity", map[string]any{"text": "waiting on you", "kind": "waiting"})
	waitSince := str(sub(sub(out, "thread"), "activity"), "since")
	out = a.must(http.StatusOK, "POST", "/api/v1/threads/"+threadID+"/activity", map[string]any{"text": "thinking again", "kind": "thinking"})
	if got := str(sub(sub(out, "thread"), "activity"), "since"); got == waitSince {
		t.Fatalf("waiting->thinking preserved since: %s", got)
	}
	out = a.must(http.StatusOK, "POST", "/api/v1/threads/"+threadID+"/activity", map[string]any{"text": "still thinking", "kind": "thinking"})
	thinkSince := str(sub(sub(out, "thread"), "activity"), "since")
	out = a.must(http.StatusOK, "POST", "/api/v1/threads/"+threadID+"/activity", map[string]any{"text": "waiting again", "kind": "waiting"})
	if got := str(sub(sub(out, "thread"), "activity"), "since"); got == thinkSince {
		t.Fatalf("thinking->waiting preserved since: %s", got)
	}
	// Busy->busy still preserves (guard against overcorrection).
	out = a.must(http.StatusOK, "POST", "/api/v1/threads/"+threadID+"/activity", map[string]any{"text": "running", "kind": "tool"})
	toolSince := str(sub(sub(out, "thread"), "activity"), "since")
	out = a.must(http.StatusOK, "POST", "/api/v1/threads/"+threadID+"/activity", map[string]any{"text": "pondering", "kind": "thinking"})
	if got := str(sub(sub(out, "thread"), "activity"), "since"); got != toolSince {
		t.Fatalf("tool->thinking reset since: %s (want %s)", got, toolSince)
	}

	// Unknown threads are not created by DELETE.
	if status, _ := a.do("DELETE", "/api/v1/threads/ext:never-made/activity", nil); status != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown thread, got %d", status)
	}
	// Status writes never move the thread in the inbox.
	before := str(sub(a.must(http.StatusOK, "GET", "/api/v1/threads/"+threadID, nil), "thread"), "last_activity_at")
	a.must(http.StatusOK, "POST", "/api/v1/threads/"+threadID+"/activity", map[string]any{"text": "still here"})
	if after := str(sub(a.must(http.StatusOK, "GET", "/api/v1/threads/"+threadID, nil), "thread"), "last_activity_at"); after != before {
		t.Fatalf("activity moved last_activity_at from %s to %s", before, after)
	}
}

func TestIdempotentPosts(t *testing.T) {
	_, a := setup(t)
	thread := str(sub(a.must(http.StatusCreated, "POST", "/api/v1/threads", map[string]any{"external_id": "idem:1", "title": "idem"}), "thread"), "id")
	first := a.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "once", "client_key": "k1"})
	again := a.must(http.StatusOK, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "once", "client_key": "k1"})
	if str(sub(first, "message"), "id") != str(sub(again, "message"), "id") || again["created"] != false {
		t.Fatalf("a repeated client_key should return the first message: %v / %v", first, again)
	}
	// The header form works too, and the key is scoped to the thread.
	viaHeader, _ := a.do("POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "twice"}, "Idempotency-Key", "k1")
	if viaHeader != http.StatusOK {
		t.Fatalf("Idempotency-Key header should dedupe, got %d", viaHeader)
	}
	other := str(sub(a.must(http.StatusCreated, "POST", "/api/v1/threads", map[string]any{"external_id": "idem:2"}), "thread"), "id")
	a.must(http.StatusCreated, "POST", "/api/v1/threads/"+other+"/messages", map[string]any{"body": "elsewhere", "client_key": "k1"})
	msgs := a.must(http.StatusOK, "GET", "/api/v1/threads/"+thread+"/messages", nil)["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("expected exactly one message after retries, got %d", len(msgs))
	}
	// Questions dedupe the same way.
	q1 := a.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/questions", map[string]any{"prompt": "Ship?", "client_key": "q1"})
	q2 := a.must(http.StatusOK, "POST", "/api/v1/threads/"+thread+"/questions", map[string]any{"prompt": "Ship?", "client_key": "q1"})
	if str(sub(q1, "question"), "id") != str(sub(q2, "question"), "id") {
		t.Fatalf("a repeated question client_key should return the first question")
	}
	if n := len(a.must(http.StatusOK, "GET", "/api/v1/questions?status=pending&thread_id="+thread, nil)["questions"].([]any)); n != 1 {
		t.Fatalf("expected one pending question, got %d", n)
	}
	// Questions without a timeout still get a lifetime.
	if sub(q1, "question")["expires_at"] == nil {
		t.Fatalf("expected a default expiry on the question: %v", q1)
	}
}

func TestDismissAndProvenance(t *testing.T) {
	b, a := setup(t)
	thread := str(sub(a.must(http.StatusCreated, "POST", "/api/v1/threads", map[string]any{"external_id": "dismiss:1", "title": "dismiss"}), "thread"), "id")
	q := sub(a.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/questions", map[string]any{"prompt": "Keep going?", "options": []map[string]any{{"label": "Yes"}}}), "question")
	qid := str(q, "id")
	// Counts see the pending question as attention.
	if c := sub(b.must(http.StatusOK, "GET", "/api/v1/counts", nil), "counts"); c["attention"].(float64) < 1 {
		t.Fatalf("expected attention >= 1, got %v", c)
	}
	out := b.must(http.StatusOK, "POST", "/api/v1/questions/"+qid+"/dismiss", nil)
	if str(sub(out, "question"), "status") != "dismissed" {
		t.Fatalf("expected dismissed, got %v", out)
	}
	if status, _ := b.do("POST", "/api/v1/questions/"+qid+"/answer", map[string]any{"selected": []string{"Yes"}}); status != http.StatusConflict {
		t.Fatalf("answering a dismissed question should conflict, got %d", status)
	}
	if got := str(sub(a.must(http.StatusOK, "GET", "/api/v1/questions/"+qid, nil), "question"), "status"); got != "dismissed" {
		t.Fatalf("agent should see dismissed, got %s", got)
	}

	// An agent posting sender=user (a terminal mirror) is recorded with token
	// origin and does not mark the thread read; the app's own reply does.
	a.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "agent says"})
	mirror := sub(a.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "typed at the terminal", "sender": "user"}), "message")
	if str(mirror, "origin") != "token" {
		t.Fatalf("expected token origin on a mirrored message, got %v", mirror)
	}
	if th := sub(a.must(http.StatusOK, "GET", "/api/v1/threads/"+thread, nil), "thread"); th["unread_count"].(float64) < 1 {
		t.Fatalf("a mirrored user message must not mark the thread read: %v", th)
	}
	reply := sub(b.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "from the phone"}), "message")
	if str(reply, "origin") != "session" {
		t.Fatalf("expected session origin on the app's reply, got %v", reply)
	}
	if th := sub(b.must(http.StatusOK, "GET", "/api/v1/threads/"+thread, nil), "thread"); th["unread_count"].(float64) != 0 {
		t.Fatalf("the app's own reply should mark the thread read: %v", th)
	}
	// Muted threads do not count toward the badge.
	a.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "more"})
	b.must(http.StatusOK, "PATCH", "/api/v1/threads/"+thread, map[string]any{"muted": true})
	c := sub(b.must(http.StatusOK, "GET", "/api/v1/counts", nil), "counts")
	for _, other := range b.must(http.StatusOK, "GET", "/api/v1/threads", nil)["threads"].([]any) {
		th := other.(map[string]any)
		if str(th, "id") != thread && th["unread_count"].(float64) > 0 && th["muted"] != true {
			t.Skip("another test left unread threads; badge assertion is not isolated")
		}
	}
	if c["unread_threads"].(float64) != 0 {
		t.Fatalf("muted thread still counted as unread: %v", c)
	}
}

func TestPushEndpointsAreForTheApp(t *testing.T) {
	b, a := setup(t)
	sub := map[string]any{"endpoint": "https://web.push.apple.com/QAbc", "keys": map[string]any{"p256dh": "k", "auth": "a"}}
	if status, out := a.do("POST", "/api/v1/push/subscriptions", sub); status != http.StatusForbidden {
		t.Fatalf("tokens must not enroll devices, got %d %v", status, out)
	}
	if status, out := b.do("POST", "/api/v1/push/subscriptions", map[string]any{"endpoint": "https://evil.example/hook", "keys": map[string]any{"p256dh": "k", "auth": "a"}}); status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown push services must be refused, got %d %v", status, out)
	}
	// Push is disabled in the test server, so a valid host still reports that.
	if status, out := b.do("POST", "/api/v1/push/subscriptions", sub); status != http.StatusServiceUnavailable {
		t.Fatalf("expected push_disabled, got %d %v", status, out)
	}
	// Logging out needs a same-origin request.
	anon := newBrowser(t)
	if status, _ := anon.do("POST", "/api/v1/auth/logout", map[string]any{}, "Sec-Fetch-Site", "cross-site"); status != http.StatusForbidden {
		t.Fatalf("cross-site logout should be refused, got %d", status)
	}
}

func TestUploadedHTMLIsInert(t *testing.T) {
	_, a := setup(t)
	thread := str(sub(a.must(http.StatusCreated, "POST", "/api/v1/threads", map[string]any{"external_id": "html:1"}), "thread"), "id")
	req, _ := http.NewRequest("POST", testSrv.URL+"/api/v1/threads/"+thread+"/attachments?filename=report.html", strings.NewReader("<script>alert(document.cookie)</script>"))
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "text/html")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("upload failed: %d %v", res.StatusCode, out)
	}
	att := out["attachments"].([]any)[0].(map[string]any)
	if str(att, "content_type") != "application/octet-stream" {
		t.Fatalf("html should be stored as an opaque file, got %s", str(att, "content_type"))
	}
	get, _ := http.NewRequest("GET", testSrv.URL+str(att, "url"), nil)
	get.Header.Set("Authorization", "Bearer "+a.token)
	res, err = http.DefaultClient.Do(get)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if cd := res.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Fatalf("expected an attachment disposition, got %q", cd)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Fatalf("expected a sandboxing CSP on attachments, got %q", csp)
	}
}

func TestTokenWritesAreRateLimited(t *testing.T) {
	_, a := setup(t)
	thread := str(sub(a.must(http.StatusCreated, "POST", "/api/v1/threads", map[string]any{"external_id": "rate:1"}), "thread"), "id")
	saved := testAPI.activityLimiter
	testAPI.activityLimiter = newRateLimiter(3)
	defer func() { testAPI.activityLimiter = saved }()
	var last int
	for i := 0; i < 5; i++ {
		last, _ = a.do("POST", "/api/v1/threads/"+thread+"/activity", map[string]any{"text": fmt.Sprintf("step %d", i)})
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("expected the fourth write to be limited, got %d", last)
	}
	req, _ := http.NewRequest("POST", testSrv.URL+"/api/v1/threads/"+thread+"/activity", strings.NewReader(`{"text":"x"}`))
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.Header.Get("Retry-After") == "" || res.Header.Get("X-Request-Id") == "" {
		t.Fatalf("expected Retry-After and X-Request-Id headers, got %v", res.Header)
	}
}

func TestSearchAndAnchorlessWait(t *testing.T) {
	b, a := setup(t)
	a.must(http.StatusCreated, "POST", "/api/v1/threads", map[string]any{"external_id": "search:100%", "title": "progress 100% done"})
	a.must(http.StatusCreated, "POST", "/api/v1/threads", map[string]any{"external_id": "search:other", "title": "my_project"})
	// LIKE metacharacters in the query are literal.
	hits := b.must(http.StatusOK, "GET", "/api/v1/threads?q=100%25", nil)["threads"].([]any)
	if len(hits) != 1 || str(hits[0].(map[string]any), "title") != "progress 100% done" {
		t.Fatalf("expected the literal match only, got %v", hits)
	}
	// external_id is searchable.
	if n := len(b.must(http.StatusOK, "GET", "/api/v1/threads?q=search%3Aother", nil)["threads"].([]any)); n != 1 {
		t.Fatalf("expected one hit on external_id, got %d", n)
	}

	// An anchorless wait reports where it started watching, and a later wait
	// anchored there sees a message posted in between.
	thread := str(sub(a.must(http.StatusCreated, "POST", "/api/v1/threads", map[string]any{"external_id": "anchor:1"}), "thread"), "id")
	out := a.must(http.StatusOK, "GET", "/api/v1/threads/"+thread+"/messages?sender=user&wait=1", nil)
	from := str(out, "waited_from")
	if out["timed_out"] != true || from == "" {
		t.Fatalf("expected a timed-out wait with waited_from, got %v", out)
	}
	b.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "slipped in between"})
	out = a.must(http.StatusOK, "GET", "/api/v1/threads/"+thread+"/messages?sender=user&wait=1&after_time="+url.QueryEscape(from), nil)
	msgs := out["messages"].([]any)
	if len(msgs) != 1 || str(msgs[0].(map[string]any), "body") != "slipped in between" {
		t.Fatalf("a wait anchored at waited_from should return the in-between message, got %v", out)
	}
	if _, has := out["waited_from"]; has {
		t.Fatalf("waited_from must not be echoed once messages are returned: %v", out)
	}
	if status, _ := a.do("GET", "/api/v1/threads/"+thread+"/messages?after_time=yesterday", nil); status != http.StatusUnprocessableEntity {
		t.Fatalf("expected a validation error for a bad after_time, got %d", status)
	}
}

// TestRoutesMatchDocs keeps server.go, docs/openapi.json and docs/API.md in
// step: every registered API route is documented, and nothing documented is
// missing from the router.
func TestRoutesMatchDocs(t *testing.T) {
	routeFiles, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var src []byte
	for _, entry := range routeFiles {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		src = append(src, raw...)
	}
	routeRe := regexp.MustCompile(`HandleFunc\("(GET|POST|PUT|PATCH|DELETE|HEAD) (/api/v1/[^"]+)"`)
	normalize := func(p string) string { return regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(p, "{}") }
	routes := map[string]bool{}
	for _, m := range routeRe.FindAllStringSubmatch(string(src), -1) {
		if m[1] == "HEAD" || (m[1] == "PUT" && m[2] == "/api/v1/threads/{thread}/activity") {
			continue // aliases of documented methods
		}
		routes[m[1]+" "+normalize(strings.TrimPrefix(m[2], "/api/v1"))] = true
	}
	raw, err := os.ReadFile("../../docs/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for path, ops := range doc.Paths {
		for method := range ops {
			if strings.ToUpper(method) == "HEAD" {
				continue
			}
			documented[strings.ToUpper(method)+" "+normalize(path)] = true
		}
	}
	for r := range routes {
		if !documented[r] {
			t.Errorf("route %s is registered but missing from docs/openapi.json", r)
		}
	}
	for d := range documented {
		if !routes[d] {
			t.Errorf("docs/openapi.json documents %s but the router does not register it", d)
		}
	}
	md, err := os.ReadFile("../../docs/API.md")
	if err != nil {
		t.Fatal(err)
	}
	for path := range doc.Paths {
		if !strings.Contains(string(md), " "+path) {
			t.Errorf("docs/API.md does not mention %s", path)
		}
	}

	// Enumerations the app and agents branch on stay in step with the code:
	// every error code the handlers emit, and every question status.
	var full struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name   string `json:"name"`
				Schema struct {
					Enum []string `json:"enum"`
				} `json:"schema"`
			} `json:"parameters"`
		} `json:"paths"`
		Components struct {
			Schemas struct {
				Error struct {
					Properties struct {
						Error struct {
							Properties struct {
								Code struct {
									Enum []string `json:"enum"`
								} `json:"code"`
							} `json:"properties"`
						} `json:"error"`
					} `json:"properties"`
				} `json:"Error"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &full); err != nil {
		t.Fatal(err)
	}
	documentedCodes := map[string]bool{}
	for _, c := range full.Components.Schemas.Error.Properties.Error.Properties.Code.Enum {
		documentedCodes[c] = true
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	codeRe := regexp.MustCompile(`Code:\s*"([a-z_]+)"`)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range codeRe.FindAllStringSubmatch(string(body), -1) {
			if !documentedCodes[m[1]] {
				t.Errorf("%s emits error code %q, which docs/openapi.json's Error enum lacks", e.Name(), m[1])
			}
		}
	}
	want := map[string]bool{store.QuestionPending: true, store.QuestionAnswered: true, store.QuestionCancelled: true, store.QuestionExpired: true, store.QuestionDismissed: true}
	for _, prm := range full.Paths["/questions"]["get"].Parameters {
		if prm.Name != "status" {
			continue
		}
		got := map[string]bool{}
		for _, v := range prm.Schema.Enum {
			got[v] = true
		}
		for v := range want {
			if !got[v] {
				t.Errorf("docs/openapi.json GET /questions status enum lacks %q", v)
			}
		}
		for v := range got {
			if !want[v] {
				t.Errorf("docs/openapi.json GET /questions status enum has %q, which the store does not know", v)
			}
		}
	}
}

func TestDeleteMessage(t *testing.T) {
	b, a := setup(t)
	thread := str(sub(a.must(http.StatusCreated, "POST", "/api/v1/threads", map[string]any{"external_id": "del:1", "title": "delete"}), "thread"), "id")
	a.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "first, harmless"})
	// A message with a file.
	up, _ := http.NewRequest("POST", testSrv.URL+"/api/v1/threads/"+thread+"/attachments?filename=key.txt", strings.NewReader("fc_leaked_token_value"))
	up.Header.Set("Authorization", "Bearer "+a.token)
	up.Header.Set("Content-Type", "text/plain")
	res, err := http.DefaultClient.Do(up)
	if err != nil {
		t.Fatal(err)
	}
	var uploaded map[string]any
	_ = json.NewDecoder(res.Body).Decode(&uploaded)
	res.Body.Close()
	att := uploaded["attachments"].([]any)[0].(map[string]any)
	leaked := sub(a.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "oops: fc_leaked_token_value", "attachments": []string{str(att, "id")}}), "message")
	if p := str(sub(a.must(http.StatusOK, "GET", "/api/v1/threads/"+thread, nil), "thread"), "preview"); !strings.Contains(p, "oops") {
		t.Fatalf("preview should show the newest message, got %q", p)
	}
	out := a.must(http.StatusOK, "DELETE", "/api/v1/messages/"+str(leaked, "id"), nil)
	if p := str(sub(out, "thread"), "preview"); p != "first, harmless" {
		t.Fatalf("preview should fall back to the remaining message, got %q", p)
	}
	if status, _ := b.do("GET", "/api/v1/messages/"+str(leaked, "id"), nil); status != http.StatusNotFound {
		t.Fatalf("deleted message still readable: %d", status)
	}
	if status, _ := b.do("GET", str(att, "url"), nil); status != http.StatusNotFound {
		t.Fatalf("attachment of a deleted message still served: %d", status)
	}
	if n := len(a.must(http.StatusOK, "GET", "/api/v1/threads/"+thread+"/messages", nil)["messages"].([]any)); n != 1 {
		t.Fatalf("expected one message left, got %d", n)
	}
	if status, _ := a.do("DELETE", "/api/v1/messages/"+str(leaked, "id"), nil); status != http.StatusNotFound {
		t.Fatalf("expected 404 on a second delete, got %d", status)
	}

	// The deleted id still works as an anchor, so a device or agent that
	// held it keeps receiving what came after; the device also learns that
	// the anchor itself is gone.
	a.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "after the delete"})
	b.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "user reply after the delete"})
	page := a.must(http.StatusOK, "GET", "/api/v1/threads/"+thread+"/messages?after="+str(leaked, "id"), nil)["messages"].([]any)
	if len(page) != 3 || page[0].(map[string]any)["deleted"] != true || str(page[0].(map[string]any), "id") != str(leaked, "id") || str(page[1].(map[string]any), "body") != "after the delete" {
		t.Fatalf("anchor on a deleted message should page forward and carry its own tombstone, got %v", page)
	}
	agentView := a.must(http.StatusOK, "GET", "/api/v1/threads/"+thread+"/messages?after="+str(leaked, "id")+"&sender=user", nil)["messages"].([]any)
	if len(agentView) != 1 || str(agentView[0].(map[string]any), "body") != "user reply after the delete" {
		t.Fatalf("an agent anchored on a deleted message should see the reply, got %v", agentView)
	}
	// A catch-up page from before the delete carries the tombstone so the
	// client prunes it; filtered and normal pages never show it.
	first := a.must(http.StatusOK, "GET", "/api/v1/threads/"+thread+"/messages", nil)["messages"].([]any)[0].(map[string]any)
	catchup := a.must(http.StatusOK, "GET", "/api/v1/threads/"+thread+"/messages?after="+str(first, "id"), nil)["messages"].([]any)
	var sawTombstone bool
	for _, m := range catchup {
		mm := m.(map[string]any)
		if str(mm, "id") == str(leaked, "id") {
			sawTombstone = true
			if mm["deleted"] != true || str(mm, "body") != "" {
				t.Fatalf("tombstone should be flagged and empty: %v", mm)
			}
		}
	}
	if !sawTombstone {
		t.Fatalf("catch-up page should include the tombstone: %v", catchup)
	}
	for _, m := range a.must(http.StatusOK, "GET", "/api/v1/threads/"+thread+"/messages", nil)["messages"].([]any) {
		if m.(map[string]any)["deleted"] == true {
			t.Fatalf("a normal page must not show tombstones: %v", m)
		}
	}
	// A device anchored on the newest message holds the older ones too, so
	// a catch-up page carries the tombstone of an older message deleted
	// since, not only of messages that came after the anchor.
	all := a.must(http.StatusOK, "GET", "/api/v1/threads/"+thread+"/messages", nil)["messages"].([]any)
	newest := all[len(all)-1].(map[string]any)
	oldest := all[0].(map[string]any)
	a.must(http.StatusOK, "DELETE", "/api/v1/messages/"+str(oldest, "id"), nil)
	catchup = a.must(http.StatusOK, "GET", "/api/v1/threads/"+thread+"/messages?after="+str(newest, "id"), nil)["messages"].([]any)
	if len(catchup) != 1 || str(catchup[0].(map[string]any), "id") != str(oldest, "id") || catchup[0].(map[string]any)["deleted"] != true {
		t.Fatalf("catch-up page should carry the older tombstone and nothing else, got %v", catchup)
	}
	if got := a.must(http.StatusOK, "GET", "/api/v1/threads/"+thread+"/messages?after="+str(newest, "id")+"&sender=user", nil)["messages"].([]any); len(got) != 0 {
		t.Fatalf("a filtered page never shows tombstones, got %v", got)
	}
	// The stored preview is the cleaned survivor, never a raw body.
	a.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "## Heading\n\n**bold** survivor"})
	last := sub(a.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "to be removed"}), "message")
	out = a.must(http.StatusOK, "DELETE", "/api/v1/messages/"+str(last, "id"), nil)
	if p := str(sub(out, "thread"), "preview"); p != "Heading bold survivor" {
		t.Fatalf("preview should be the cleaned survivor, got %q", p)
	}
	if p := str(sub(b.must(http.StatusOK, "GET", "/api/v1/threads/"+thread, nil), "thread"), "preview"); p != "Heading bold survivor" {
		t.Fatalf("stored preview should be cleaned too, got %q", p)
	}
}

func TestConcurrentIdempotentPosts(t *testing.T) {
	_, a := setup(t)
	thread := str(sub(a.must(http.StatusCreated, "POST", "/api/v1/threads", map[string]any{"external_id": "race:1"}), "thread"), "id")
	type res struct {
		status  int
		id      string
		created any
	}
	results := make(chan res, 6)
	for i := 0; i < 6; i++ {
		go func() {
			c := &client{t: t, http: &http.Client{}, token: a.token}
			status, out := c.do("POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "racing", "client_key": "same-key"})
			results <- res{status, str(sub(out, "message"), "id"), out["created"]}
		}()
	}
	var created, replayed int
	ids := map[string]bool{}
	for i := 0; i < 6; i++ {
		r := <-results
		switch r.status {
		case http.StatusCreated:
			created++
		case http.StatusOK:
			replayed++
		default:
			t.Fatalf("unexpected status %d for a racing idempotent post", r.status)
		}
		ids[r.id] = true
	}
	if created != 1 || replayed != 5 || len(ids) != 1 {
		t.Fatalf("expected one create and five replays of one message, got created=%d replayed=%d ids=%d", created, replayed, len(ids))
	}
	// A replay carrying a status line still applies it.
	a.must(http.StatusOK, "DELETE", "/api/v1/threads/"+thread+"/activity", nil)
	out := a.must(http.StatusOK, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "racing", "client_key": "same-key", "activity": map[string]any{"text": "still going"}})
	if out["created"] != false || out["applied"] != true || str(sub(sub(out, "thread"), "activity"), "text") != "still going" {
		t.Fatalf("a replayed post should apply its activity: %v", out)
	}
	// Questions race the same way.
	qs := make(chan int, 4)
	for i := 0; i < 4; i++ {
		go func() {
			c := &client{t: t, http: &http.Client{}, token: a.token}
			status, _ := c.do("POST", "/api/v1/threads/"+thread+"/questions", map[string]any{"prompt": "race?", "client_key": "qk"})
			qs <- status
		}()
	}
	var q201, q200 int
	for i := 0; i < 4; i++ {
		switch <-qs {
		case http.StatusCreated:
			q201++
		case http.StatusOK:
			q200++
		default:
			t.Fatal("unexpected status for a racing question")
		}
	}
	if q201 != 1 || q200 != 3 {
		t.Fatalf("expected one created question and three replays, got %d/%d", q201, q200)
	}
}

func TestAttentionAndClearedSeq(t *testing.T) {
	b, a := setup(t)
	thread := str(sub(a.must(http.StatusCreated, "POST", "/api/v1/threads", map[string]any{"external_id": "att:1", "title": "attention"}), "thread"), "id")
	q := sub(a.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/questions", map[string]any{"prompt": "Still there?"}), "question")
	b.must(http.StatusOK, "PATCH", "/api/v1/threads/"+thread, map[string]any{"archived": true, "muted": true})
	has := func(list []any) bool {
		for _, it := range list {
			if str(it.(map[string]any), "id") == str(q, "id") {
				return true
			}
		}
		return false
	}
	if !has(a.must(http.StatusOK, "GET", "/api/v1/questions?status=pending", nil)["questions"].([]any)) {
		t.Fatal("an agent listing its questions must still see one in an archived thread")
	}
	if has(b.must(http.StatusOK, "GET", "/api/v1/questions?status=pending&attention=true", nil)["questions"].([]any)) {
		t.Fatal("the attention view must leave out questions in archived or muted threads")
	}
	b.must(http.StatusOK, "PATCH", "/api/v1/threads/"+thread, map[string]any{"archived": false, "muted": false})

	// A clear with a sequence beats a straggling write with a lower one.
	a.must(http.StatusOK, "POST", "/api/v1/threads/"+thread+"/activity", map[string]any{"text": "Running tests", "seq": 100})
	a.must(http.StatusOK, "DELETE", "/api/v1/threads/"+thread+"/activity?seq=200", nil)
	out := a.must(http.StatusOK, "POST", "/api/v1/threads/"+thread+"/activity", map[string]any{"text": "Thinking…", "seq": 150})
	if out["applied"] != false || sub(out, "thread")["activity"] != nil {
		t.Fatalf("a write older than the clear must be ignored: %v", out)
	}
	out = a.must(http.StatusOK, "POST", "/api/v1/threads/"+thread+"/activity", map[string]any{"text": "Next step", "seq": 300})
	if out["applied"] != true {
		t.Fatalf("a write newer than the clear must apply: %v", out)
	}
	// Unsequenced callers are unaffected by the clear guard.
	a.must(http.StatusOK, "DELETE", "/api/v1/threads/"+thread+"/activity?seq=400", nil)
	if out := a.must(http.StatusOK, "POST", "/api/v1/threads/"+thread+"/activity", map[string]any{"text": "plain"}); out["applied"] != true {
		t.Fatalf("an unsequenced write should still apply: %v", out)
	}

	// The clear an agent's post implies records a watermark too, when the
	// post carries a blank sequenced activity: the path the hooks use.
	a.must(http.StatusOK, "POST", "/api/v1/threads/"+thread+"/activity", map[string]any{"text": "Running tests", "seq": 500})
	out = a.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/messages", map[string]any{"body": "Tests are green.", "activity": map[string]any{"text": "", "seq": 600}})
	if sub(out, "thread")["activity"] != nil {
		t.Fatalf("an agent post should clear the status: %v", out)
	}
	out = a.must(http.StatusOK, "POST", "/api/v1/threads/"+thread+"/activity", map[string]any{"text": "Thinking…", "seq": 550})
	if out["applied"] != false || sub(out, "thread")["activity"] != nil {
		t.Fatalf("a straggler older than the post's clear must be ignored: %v", out)
	}
	// A question's implicit clear does the same.
	a.must(http.StatusOK, "POST", "/api/v1/threads/"+thread+"/activity", map[string]any{"text": "Deciding", "seq": 700})
	a.must(http.StatusCreated, "POST", "/api/v1/threads/"+thread+"/questions", map[string]any{"prompt": "Ship?", "activity": map[string]any{"text": "", "seq": 800}})
	if out := a.must(http.StatusOK, "POST", "/api/v1/threads/"+thread+"/activity", map[string]any{"text": "late", "seq": 750}); out["applied"] != false {
		t.Fatalf("a straggler older than the question's clear must be ignored: %v", out)
	}
	// The watermark is a straggler guard, not a permanent pin: a clear from
	// a faster clock stops rejecting writes after ten minutes.
	if _, err := testPool.Exec(context.Background(), "UPDATE threads SET activity_cleared_at = now() - interval '11 minutes' WHERE id = $1", thread); err != nil {
		t.Fatal(err)
	}
	if out := a.must(http.StatusOK, "POST", "/api/v1/threads/"+thread+"/activity", map[string]any{"text": "much later", "seq": 760}); out["applied"] != true {
		t.Fatalf("an old clear must not pin the status line shut: %v", out)
	}
}

func TestOpenSignupAndAccountDeletion(t *testing.T) {
	setup(t)
	testAPI.cfg.Signup = "open"
	defer func() { testAPI.cfg.Signup = "" }()
	// Every test registers from the same address; the per-address
	// registration budget is exercised by its own test below.
	limiter := testAPI.registerLimiter
	testAPI.registerLimiter = newRateLimiter(1000)
	defer func() { testAPI.registerLimiter = limiter }()
	anon := newBrowser(t)
	status, out := anon.do("GET", "/api/v1/auth/status", nil)
	if status != 200 || out["signup"] != "open" {
		t.Fatalf("expected open signup, got %d %v", status, out)
	}
	anon.must(http.StatusCreated, "POST", "/api/v1/auth/register", map[string]any{"email": "fourth@example.com", "password": "correct-horse-battery", "display_name": "Fourth"})
	// Registration stays open for the next stranger.
	status, out = anon.do("GET", "/api/v1/auth/status", nil)
	if status != 200 || out["signup"] != "open" || out["authenticated"] != true {
		t.Fatalf("expected open signup and a session, got %d %v", status, out)
	}

	// The new account owns a thread, a message with an attachment, a token
	// and a device; deleting the account removes all of it, including bytes.
	tok := anon.must(http.StatusCreated, "POST", "/api/v1/tokens", map[string]any{"name": "doomed"})
	agentTok := &client{t: t, http: &http.Client{}, token: str(tok, "secret")}
	msg := agentTok.must(http.StatusCreated, "POST", "/api/v1/threads/ext:doomed/messages", map[string]any{"title": "doomed", "body": "hello"})
	threadID := str(sub(msg, "message"), "thread_id")
	png := makePNG(t, 12, 12)
	req, _ := http.NewRequest("POST", testSrv.URL+"/api/v1/threads/"+threadID+"/attachments", bytes.NewReader(png))
	req.Header.Set("Authorization", "Bearer "+agentTok.token)
	req.Header.Set("Content-Type", "image/png")
	req.Header.Set("X-Filename", "shot.png")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var up map[string]any
	_ = json.NewDecoder(res.Body).Decode(&up)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("upload: %d %v", res.StatusCode, up)
	}
	attID := str(up["attachments"].([]any)[0].(map[string]any), "id")
	agentTok.must(http.StatusCreated, "POST", "/api/v1/threads/"+threadID+"/messages", map[string]any{"body": "with file", "attachments": []string{attID}})
	me := anon.must(http.StatusOK, "GET", "/api/v1/me", nil)
	if used, _ := sub(me, "storage")["attachment_bytes"].(float64); int(used) != len(png) {
		t.Fatalf("expected %d attachment bytes in /me, got %v", len(png), sub(me, "storage"))
	}

	// Tokens cannot delete the account; a wrong password cannot either.
	if status, out := agentTok.do("DELETE", "/api/v1/me", map[string]any{"password": "correct-horse-battery"}); status != http.StatusForbidden {
		t.Fatalf("expected token deletion to be forbidden, got %d %v", status, out)
	}
	if status, out := anon.do("DELETE", "/api/v1/me", map[string]any{"password": "wrong-password"}); status != http.StatusForbidden || str(sub(out, "error"), "code") != "invalid_credentials" {
		t.Fatalf("expected invalid_credentials, got %d %v", status, out)
	}
	anon.must(http.StatusOK, "DELETE", "/api/v1/me", map[string]any{"password": "correct-horse-battery"})
	if status, _ := anon.do("GET", "/api/v1/me", nil); status != http.StatusUnauthorized {
		t.Fatalf("expected the session to be gone, got %d", status)
	}
	if status, _ := agentTok.do("GET", "/api/v1/me", nil); status != http.StatusUnauthorized {
		t.Fatalf("expected the token to be gone, got %d", status)
	}
	var n int
	if err := testPool.QueryRow(context.Background(), "SELECT count(*) FROM users WHERE email = 'fourth@example.com'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("expected the account row to be gone, got n=%d err=%v", n, err)
	}
	if err := testPool.QueryRow(context.Background(), "SELECT count(*) FROM attachments WHERE id = $1", attID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("expected the attachment row to be gone, got n=%d err=%v", n, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, _, _, err := testAPI.blobs.Get(context.Background(), "a/"+attID+"/shot.png"); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("attachment bytes should have been deleted with the account")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// The address is free to register again.
	fresh := newBrowser(t)
	fresh.must(http.StatusCreated, "POST", "/api/v1/auth/register", map[string]any{"email": "fourth@example.com", "password": "correct-horse-battery"})
	fresh.must(http.StatusOK, "DELETE", "/api/v1/me", map[string]any{"password": "correct-horse-battery"})
}

func TestRegistrationHasItsOwnRateLimit(t *testing.T) {
	setup(t)
	testAPI.cfg.Signup = "open"
	defer func() { testAPI.cfg.Signup = "" }()
	limiter := testAPI.registerLimiter
	testAPI.registerLimiter = newRateLimiter(2)
	defer func() { testAPI.registerLimiter = limiter }()
	anon := newBrowser(t)
	for i := 0; i < 2; i++ {
		// Validation failures still spend the budget: the address is what is limited.
		if status, _ := anon.do("POST", "/api/v1/auth/register", map[string]any{"email": "not an email", "password": "correct-horse-battery"}); status != http.StatusUnprocessableEntity {
			t.Fatalf("attempt %d: expected validation failure, got %d", i, status)
		}
	}
	if status, out := anon.do("POST", "/api/v1/auth/register", map[string]any{"email": "fifth@example.com", "password": "correct-horse-battery"}); status != http.StatusTooManyRequests || str(sub(out, "error"), "code") != "rate_limited" {
		t.Fatalf("expected rate_limited, got %d %v", status, out)
	}
	// Signing in is budgeted separately and still works.
	if status, _ := anon.do("POST", "/api/v1/auth/login", map[string]any{"email": "eric@example.com", "password": "correct-horse-battery"}); status != http.StatusOK {
		t.Fatalf("expected login to be unaffected, got %d", status)
	}
}

func TestAttachmentQuota(t *testing.T) {
	b, a := setup(t)
	me := b.must(http.StatusOK, "GET", "/api/v1/me", nil)
	used, _ := sub(me, "storage")["attachment_bytes"].(float64)
	if q, _ := sub(me, "storage")["attachment_quota_bytes"].(float64); q != 0 {
		t.Fatalf("expected no quota in the test configuration, got %v", q)
	}
	testAPI.cfg.AttachmentQuotaBytes = int64(used) + 100
	defer func() { testAPI.cfg.AttachmentQuotaBytes = 0 }()
	msg := a.must(http.StatusCreated, "POST", "/api/v1/threads/ext:quota/messages", map[string]any{"title": "quota", "body": "hello"})
	threadID := str(sub(msg, "message"), "thread_id")
	upload := func(name string, body []byte) (int, map[string]any) {
		req, _ := http.NewRequest("POST", testSrv.URL+"/api/v1/threads/"+threadID+"/attachments", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+a.token)
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("X-Filename", name)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	if status, out := upload("one.txt", bytes.Repeat([]byte("a"), 60)); status != http.StatusCreated {
		t.Fatalf("first upload within quota: %d %v", status, out)
	}
	if status, out := upload("two.txt", bytes.Repeat([]byte("b"), 60)); status != http.StatusRequestEntityTooLarge || str(sub(out, "error"), "code") != "storage_quota" {
		t.Fatalf("expected storage_quota, got %d %v", status, out)
	}
	if status, out := upload("three.txt", bytes.Repeat([]byte("c"), 40)); status != http.StatusCreated {
		t.Fatalf("upload that exactly fills the quota: %d %v", status, out)
	}
	me = b.must(http.StatusOK, "GET", "/api/v1/me", nil)
	if got, _ := sub(me, "storage")["attachment_bytes"].(float64); int64(got) != int64(used)+100 {
		t.Fatalf("expected %d bytes used, got %v", int64(used)+100, got)
	}
	if q, _ := sub(me, "storage")["attachment_quota_bytes"].(float64); int64(q) != int64(used)+100 {
		t.Fatalf("expected the quota in /me, got %v", q)
	}
	// Freeing space by deleting the thread makes room again.
	a.must(http.StatusOK, "DELETE", "/api/v1/threads/"+threadID, nil)
	msg = a.must(http.StatusCreated, "POST", "/api/v1/threads/ext:quota2/messages", map[string]any{"title": "quota", "body": "hello"})
	threadID = str(sub(msg, "message"), "thread_id")
	if status, out := upload("four.txt", bytes.Repeat([]byte("d"), 100)); status != http.StatusCreated {
		t.Fatalf("upload after freeing space: %d %v", status, out)
	}
	a.must(http.StatusOK, "DELETE", "/api/v1/threads/"+threadID, nil)
}
