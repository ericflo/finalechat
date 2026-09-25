package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ericflo/finalechat/internal/auth"
	"github.com/ericflo/finalechat/internal/bus"
	"github.com/ericflo/finalechat/internal/messenger"
	"github.com/ericflo/finalechat/internal/store"
)

// The Messenger connector lets the user drive their threads from Facebook
// Messenger when that is the only app that works (in-flight messaging
// passes). It runs inside the server, not as an agent, because what it
// writes must be indistinguishable from the app: replies are the user's own
// (origin "session", which agents such as eagent require), answers go
// through the answer path, and new sessions are trusted session.start
// commands. Linking is therefore as strong as a browser sign-in: a one-time
// code from Settings, sent from the Messenger account.

const messengerLinkCodeTTL = 15 * time.Minute

// messengerCapsule is the eagent client-capability capsule (v1) carried on
// everything the connector writes, so an agent knows the user is reading
// in a plain-text chat app on a phone.
func (s *Server) messengerCapsule() store.JSON {
	return store.JSON{"eagent.client": map[string]any{
		"app":      "finalechat-messenger/" + s.cfg.Version,
		"device":   "phone",
		"supplies": []string{},
	}}
}

// GET /api/v1/messenger
func (s *Server) handleGetMessenger(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	out := map[string]any{"enabled": s.messenger != nil, "link": nil}
	if s.messenger == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["page_url"] = "https://m.me/" + s.cfg.MessengerPageID
	link, err := s.store.GetMessengerLink(r.Context(), p.user.ID)
	switch {
	case err == nil:
		out["link"] = link
	case !errors.Is(err, store.ErrNotFound):
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

var errMessengerDisabled = &apiError{Status: http.StatusServiceUnavailable, Code: "messenger_disabled", Message: "The Messenger connector is not configured on this server."}

// POST /api/v1/messenger/code
func (s *Server) handleMessengerCode(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if s.messenger == nil {
		writeError(w, errMessengerDisabled)
		return
	}
	code, err := newMessengerCode()
	if err != nil {
		writeError(w, err)
		return
	}
	expires := time.Now().Add(messengerLinkCodeTTL).UTC().Truncate(time.Second)
	if err := s.store.CreateMessengerLinkCode(r.Context(), p.user.ID, auth.HashToken(normalizeMessengerCode(code)), expires); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"code": code, "expires_at": expires, "page_url": "https://m.me/" + s.cfg.MessengerPageID})
}

// DELETE /api/v1/messenger
func (s *Server) handleMessengerUnlink(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if err := s.store.DeleteMessengerLink(r.Context(), p.user.ID); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

const messengerCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// newMessengerCode returns a code like "K7QM-3XPD" (40 bits).
func newMessengerCode() (string, error) {
	var b strings.Builder
	for i := 0; i < 8; i++ {
		if i == 4 {
			b.WriteByte('-')
		}
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(messengerCodeAlphabet))))
		if err != nil {
			return "", err
		}
		b.WriteByte(messengerCodeAlphabet[n.Int64()])
	}
	return b.String(), nil
}

func normalizeMessengerCode(code string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
}

// GET /api/messenger/webhook: Meta's subscription handshake.
func (s *Server) handleMessengerVerify(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if s.messenger == nil || q.Get("hub.mode") != "subscribe" ||
		subtle.ConstantTimeCompare([]byte(q.Get("hub.verify_token")), []byte(s.cfg.MessengerVerifyToken)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, q.Get("hub.challenge"))
}

// POST /api/messenger/webhook: page events, signed with the app secret.
func (s *Server) handleMessengerWebhook(w http.ResponseWriter, r *http.Request) {
	if s.messenger == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !messenger.ValidSignature(s.cfg.MessengerAppSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		http.Error(w, "bad signature", http.StatusForbidden)
		return
	}
	var hook messenger.Webhook
	if err := json.Unmarshal(body, &hook); err != nil || hook.Object != "page" {
		// Acknowledge what we do not understand so Meta does not retry it.
		_, _ = io.WriteString(w, "IGNORED")
		return
	}
	// Meta waits up to 20 seconds; the work is a few queries and replies.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 18*time.Second)
	defer cancel()
	for _, entry := range hook.Entry {
		for _, ev := range entry.Messaging {
			if err := s.messengerEvent(ctx, ev); err != nil {
				s.log.Error("messenger event", "err", err)
				// A failure here is ours (the database); let Meta redeliver.
				http.Error(w, "retry", http.StatusInternalServerError)
				return
			}
		}
	}
	_, _ = io.WriteString(w, "EVENT_RECEIVED")
}

// inbound is one thing the person did in Messenger.
type inbound struct {
	mid     string
	text    string
	payload string
	replyTo string
	files   []inboundFile
}

type inboundFile struct {
	kind    string // image, video, audio, file
	url     string
	sticker bool
}

func (s *Server) messengerEvent(ctx context.Context, ev messenger.Event) error {
	var in inbound
	switch {
	case ev.Message != nil:
		if ev.Message.IsEcho {
			return nil
		}
		in.mid, in.text = ev.Message.MID, strings.TrimSpace(ev.Message.Text)
		for _, a := range ev.Message.Attachments {
			in.files = append(in.files, inboundFile{kind: a.Type, url: a.Payload.URL, sticker: a.Payload.StickerID != 0})
		}
		if ev.Message.QuickReply != nil {
			in.payload = ev.Message.QuickReply.Payload
		}
		if ev.Message.ReplyTo != nil {
			in.replyTo = ev.Message.ReplyTo.MID
		}
	case ev.Postback != nil:
		in.mid, in.payload, in.text = ev.Postback.MID, ev.Postback.Payload, ev.Postback.Title
		if in.mid == "" {
			in.mid = "postback:" + ev.Sender.ID + ":" + strconv.FormatInt(ev.Timestamp, 10)
		}
	default:
		return nil // reads, deliveries, reactions
	}
	psid := ev.Sender.ID
	if psid == "" || in.mid == "" || (ev.Recipient.ID != "" && ev.Recipient.ID != s.cfg.MessengerPageID) {
		return nil
	}
	if seen, err := s.store.MessengerInboundSeen(ctx, in.mid); err != nil || seen {
		return err
	}
	link, err := s.store.GetMessengerLinkByPSID(ctx, psid)
	if errors.Is(err, store.ErrNotFound) {
		s.messengerUnlinked(ctx, psid, in)
		return s.store.MarkMessengerInbound(ctx, in.mid)
	}
	if err != nil {
		return err
	}
	user, err := s.store.GetUser(ctx, link.UserID)
	if err != nil {
		return err
	}
	if err := s.store.TouchMessengerInbound(ctx, link.UserID); err != nil {
		return err
	}
	if link.WindowClosedAt != nil {
		// The page may write again; send what was held.
		s.kickMessenger(link.UserID)
	}
	_ = s.messenger.SenderAction(ctx, psid, "mark_seen")
	c := &mchat{s: s, ctx: ctx, link: link, user: user, in: in}
	if err := c.handle(); err != nil {
		return err
	}
	return s.store.MarkMessengerInbound(ctx, in.mid)
}

var linkCommandRe = regexp.MustCompile(`(?i)^\s*/?link\s+([A-Za-z0-9 -]{4,20})\s*$`)

// messengerUnlinked answers someone the page does not know: a link code
// links them, anything else gets instructions (at most once a minute).
func (s *Server) messengerUnlinked(ctx context.Context, psid string, in inbound) {
	if !s.messengerLinkLimiter.allow("psid:" + psid) {
		return
	}
	if m := linkCommandRe.FindStringSubmatch(in.text); m != nil {
		link, err := s.store.RedeemMessengerLinkCode(ctx, auth.HashToken(normalizeMessengerCode(m[1])), psid, s.cfg.MessengerPageID)
		if err == nil {
			user, _ := s.store.GetUser(ctx, link.UserID)
			who := "your account"
			if user != nil {
				who = user.Email
			}
			s.messengerSay(ctx, psid, "Linked to Finalechat as "+who+". Agent messages and questions now arrive here, and what you write goes to the thread that spoke last.\n\n"+messengerHelp, nil)
			return
		}
		if !errors.Is(err, store.ErrNotFound) {
			s.log.Error("messenger link", "err", err)
		}
		s.messengerSay(ctx, psid, "That code is unknown or has expired. Get a new one in Finalechat → Settings → Messenger.", nil)
		return
	}
	s.messengerSay(ctx, psid, "This is a private Finalechat bridge. To connect it, open Finalechat → Settings → Messenger and send the code shown there, like: link K7QM-3XPD", nil)
}

// messengerSay sends a short reply that is not about any thread.
func (s *Server) messengerSay(ctx context.Context, psid, text string, quick []messenger.QuickReply) string {
	mid, err := s.messenger.SendText(ctx, psid, text, quick, "")
	if err != nil {
		s.log.Warn("messenger reply", "err", err)
	}
	return mid
}

// mchat handles one inbound event from a linked person.
type mchat struct {
	s    *Server
	ctx  context.Context
	link *store.MessengerLink
	user *store.User
	in   inbound
}

const messengerHelp = `Commands:
/ls — threads (#numbers)
/go 3 — send replies to #3 (/go alone: follow whoever spoke last)
/where — current thread, status, open question
/read 3 — recent messages in #3
/new — start an eagent session
/skip — decline the open question
/more — rest of a long message
/quiet, /loud — only questions and important messages, or everything
/pause, /resume — stop or restart the relay (keeps the link)
/remote on|off — Claude Code waits for your replies
/unlink — disconnect this chat
Swipe-reply to a message to answer its thread. Anything else starting with / goes to the agent.`

func (c *mchat) say(text string, quick []messenger.QuickReply) string {
	return c.s.messengerSay(c.ctx, c.link.PSID, text, quick)
}

// sayAbout sends a reply tied to a thread, so a swipe-reply to it routes there.
func (c *mchat) sayAbout(thread uuid.UUID, text string, quick []messenger.QuickReply) {
	if mid := c.say(text, quick); mid != "" {
		_ = c.s.store.RecordMessengerSent(c.ctx, c.link.UserID, mid, &thread, nil)
	}
}

func (c *mchat) handle() error {
	in := c.in
	if in.payload != "" {
		return c.payload(in.payload)
	}
	if len(in.files) > 0 {
		return c.postFiles()
	}
	if in.text == "" {
		return nil
	}
	if strings.HasPrefix(in.text, "/") {
		if done, err := c.command(); done || err != nil {
			return err
		}
	}
	if in.replyTo == "" {
		if done, err := c.newTyped(in.text); done || err != nil {
			return err
		}
	}
	// A session being set up takes the next plain message as its prompt.
	if c.link.PausedAt != nil {
		defer c.say("(The relay is paused: agent replies won't arrive here. /resume turns it back on.)", nil)
	}
	if st := c.newState(); st != nil && st.Step == "prompt" && in.replyTo == "" && !strings.HasPrefix(in.text, "/") {
		return c.startSession(st.Resource, st.Cwd, in.text)
	}
	return c.reply(in.text)
}

// reply routes free text: a swipe-reply goes to that message's thread
// (answering its question if it asked one), anything else to the pinned
// thread or the one that spoke last.
func (c *mchat) reply(text string) error {
	thread, question, err := c.target()
	if thread == nil || err != nil {
		return err
	}
	// An open question in the thread takes the reply as its answer, the way
	// an agent waiting on it expects.
	var q *store.Question
	if question != nil {
		if found, err := c.s.store.GetQuestion(c.ctx, c.user.ID, *question); err == nil && found.Status == store.QuestionPending {
			q = found
		}
	}
	if q == nil {
		q, err = c.pendingQuestion(thread.ID)
		if err != nil {
			return err
		}
	}
	if q != nil {
		if selected, ok := parseOptionNumbers(text, q); ok {
			return c.answer(thread, q, selected, "")
		}
		if q.AllowFreeform {
			return c.answer(thread, q, nil, text)
		}
	}
	return c.post(thread, text)
}

// target resolves where a reply goes: the thread of the message it
// swipe-replies to (and that message's question, if any), else the pinned
// thread or the one that spoke last. A nil thread means the person has
// been told why there is nowhere to go.
func (c *mchat) target() (*store.Thread, *uuid.UUID, error) {
	var target, question *uuid.UUID
	if c.in.replyTo != "" {
		t, q, err := c.s.store.LookupMessengerSent(c.ctx, c.link.UserID, c.in.replyTo)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, nil, err
		}
		target, question = t, q
	}
	if target == nil {
		target = c.link.Target()
	}
	if target == nil {
		c.say("There is no thread to reply to yet. /ls lists threads, /go 2 picks one, /new starts a session.", nil)
		return nil, nil, nil
	}
	thread, err := c.s.store.GetThread(c.ctx, c.user.ID, *target)
	if errors.Is(err, store.ErrNotFound) {
		c.say("That thread no longer exists. /ls lists the current ones.", nil)
		return nil, nil, nil
	}
	return thread, question, err
}

// postFiles relays photos and files the person sent into the target
// thread as one message (with any text as its body), the way the app
// uploads and posts. Stickers are not files; a lone one (the thumbs-up
// button) reads as 👍.
func (c *mchat) postFiles() error {
	var files []inboundFile
	for _, f := range c.in.files {
		if !f.sticker && f.url != "" {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		if c.in.text == "" {
			return c.reply("👍")
		}
		return c.reply(c.in.text)
	}
	if c.s.blobs == nil {
		c.say("This server does not store files, so photos cannot be relayed. Describe it in words instead.", nil)
		return nil
	}
	thread, _, err := c.target()
	if thread == nil || err != nil {
		return err
	}
	var ids []uuid.UUID
	for i, f := range files {
		if i == store.MaxAttachmentsPerMessage {
			break
		}
		data, contentType, err := c.s.messenger.Download(c.ctx, f.url, store.MaxAttachmentBytes)
		if err != nil {
			c.s.log.Warn("messenger: download", "err", err)
			c.say("One file could not be fetched from Messenger (files must be under 10 MB). Try again or describe it.", nil)
			continue
		}
		a, err := c.s.storeAttachment(c.ctx, c.user.ID, thread.ID, contentType, inboundFilename(f, contentType), bytes.NewReader(data))
		if err != nil {
			var ae *apiError
			if errors.As(err, &ae) {
				c.say("A file was not relayed: "+ae.Message, nil)
				continue
			}
			return err
		}
		ids = append(ids, a.ID)
	}
	if len(ids) == 0 && c.in.text == "" {
		return nil
	}
	msg, _, created, err := c.s.store.CreateMessage(c.ctx, c.user.ID, thread.ID, store.MessageInput{
		Sender: store.SenderUser, Body: c.in.text, Format: "text", Importance: store.ImportanceNormal,
		Meta: c.s.messengerCapsule(), Origin: store.OriginSession, MarkRead: true, ClientKey: "messenger:" + c.in.mid, AttachmentIDs: ids,
	})
	if err != nil {
		return err
	}
	if created {
		c.s.bus.Publish(context.WithoutCancel(c.ctx), bus.Event{Type: bus.MessageCreated, UserID: c.user.ID.String(), ThreadID: thread.ID.String(), MessageID: msg.ID.String()})
	}
	c.routed(thread, "")
	return nil
}

// inboundFilename names a file from its CDN link, or by its kind.
func inboundFilename(f inboundFile, contentType string) string {
	if u, err := url.Parse(f.url); err == nil {
		if base := path.Base(u.Path); base != "" && base != "/" && base != "." && strings.Contains(base, ".") {
			return base
		}
	}
	ext := ".bin"
	if exts, _ := mime.ExtensionsByType(contentType); len(exts) > 0 {
		ext = exts[0]
	}
	switch f.kind {
	case "image":
		return "photo" + ext
	case "video":
		return "video" + ext
	case "audio":
		return "voice" + ext
	}
	return "file" + ext
}

// post writes the user's message into the thread, as the app would.
func (c *mchat) post(thread *store.Thread, text string) error {
	if len(text) > store.MaxBodyBytes {
		text = text[:store.MaxBodyBytes]
	}
	msg, _, created, err := c.s.store.CreateMessage(c.ctx, c.user.ID, thread.ID, store.MessageInput{
		Sender: store.SenderUser, Body: text, Format: "text", Importance: store.ImportanceNormal,
		Meta: c.s.messengerCapsule(), Origin: store.OriginSession, MarkRead: true, ClientKey: "messenger:" + c.in.mid,
	})
	if errors.Is(err, store.ErrNotFound) {
		c.say("That thread no longer exists. /ls lists the current ones.", nil)
		return nil
	}
	if err != nil {
		return err
	}
	if created {
		c.s.bus.Publish(context.WithoutCancel(c.ctx), bus.Event{Type: bus.MessageCreated, UserID: c.user.ID.String(), ThreadID: thread.ID.String(), MessageID: msg.ID.String()})
	}
	c.routed(thread, "")
	return nil
}

// answer resolves a question through the same path as the app's answer.
func (c *mchat) answer(thread *store.Thread, q *store.Question, selected []string, text string) error {
	answered, msg, _, err := c.s.store.AnswerQuestion(c.ctx, c.user.ID, q.ID, store.Answer{Selected: selected, Text: text}, store.OriginSession, c.s.messengerCapsule())
	if errors.Is(err, store.ErrInvalidState) {
		c.say("That question was already resolved.", nil)
		return nil
	}
	if err != nil {
		return err
	}
	bg := context.WithoutCancel(c.ctx)
	c.s.bus.Publish(bg, bus.Event{Type: bus.QuestionAnswered, UserID: c.user.ID.String(), ThreadID: thread.ID.String(), QuestionID: answered.ID.String()})
	c.s.bus.Publish(bg, bus.Event{Type: bus.MessageCreated, UserID: c.user.ID.String(), ThreadID: thread.ID.String(), MessageID: msg.ID.String()})
	c.routed(thread, "✓ Answered: "+messenger.Clip(msg.Body, 120))
	return nil
}

// routed records where a reply went and says so when it is not the thread
// the person's previous reply went to: with several sessions talking, the
// one that spoke last may not be the one they meant.
func (c *mchat) routed(thread *store.Thread, note string) {
	previous, _ := c.link.State["replied"].(string)
	moved := previous != thread.ID.String()
	_ = c.s.store.SetMessengerLastThread(c.ctx, c.link.UserID, thread.ID)
	if moved {
		state := store.JSON{}
		for k, v := range c.link.State {
			state[k] = v
		}
		state["replied"] = thread.ID.String()
		c.link.State = state
		_ = c.s.store.SetMessengerState(c.ctx, c.link.UserID, state)
	}
	if moved {
		tag := c.s.messengerTag(c.ctx, c.link.UserID, thread)
		if note == "" {
			note = "→ " + tag
		} else {
			note = tag + " " + note
		}
	}
	if note != "" {
		c.sayAbout(thread.ID, note, nil)
	}
}

func (c *mchat) pendingQuestion(threadID uuid.UUID) (*store.Question, error) {
	qs, err := c.s.store.ListThreadQuestions(c.ctx, c.user.ID, threadID)
	if err != nil {
		return nil, err
	}
	for i := len(qs) - 1; i >= 0; i-- {
		if qs[i].Status == store.QuestionPending {
			return qs[i], nil
		}
	}
	return nil, nil
}

var optionNumbersRe = regexp.MustCompile(`^\s*\d{1,2}(\s*[, ]\s*\d{1,2})*\s*$`)

// parseOptionNumbers reads "2" or "1, 3" as option choices.
func parseOptionNumbers(text string, q *store.Question) ([]string, bool) {
	if len(q.Options) == 0 || !optionNumbersRe.MatchString(text) {
		return nil, false
	}
	var out []string
	seen := map[int]bool{}
	for _, f := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ' ' }) {
		n, err := strconv.Atoi(f)
		if err != nil || n < 1 || n > len(q.Options) {
			return nil, false
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, q.Options[n-1].Label)
		}
	}
	if len(out) == 0 || (len(out) > 1 && !q.MultiSelect) {
		return nil, false
	}
	return out, true
}

// payload handles a quick-reply tap.
func (c *mchat) payload(p string) error {
	kind, rest, _ := strings.Cut(p, ":")
	switch kind {
	case "a", "d":
		qs, idx, _ := strings.Cut(rest, ":")
		id, err := uuid.Parse(qs)
		if err != nil {
			return nil
		}
		q, err := c.s.store.GetQuestion(c.ctx, c.user.ID, id)
		if errors.Is(err, store.ErrNotFound) {
			c.say("That question no longer exists.", nil)
			return nil
		}
		if err != nil {
			return err
		}
		thread, err := c.s.store.GetThread(c.ctx, c.user.ID, q.ThreadID)
		if err != nil {
			return err
		}
		if q.Status != store.QuestionPending {
			c.sayAbout(thread.ID, "That question is already "+q.Status+".", nil)
			return nil
		}
		if kind == "d" {
			return c.dismiss(thread, q)
		}
		n, err := strconv.Atoi(idx)
		if err != nil || n < 0 || n >= len(q.Options) {
			return nil
		}
		return c.answer(thread, q, []string{q.Options[n].Label}, "")
	case "go":
		n, err := strconv.Atoi(rest)
		if err != nil {
			return nil
		}
		return c.goTo(n)
	case "np", "nd":
		return c.newChoice(kind, rest)
	}
	// Unknown payloads (an old button) read as the text they showed.
	if c.in.text != "" {
		return c.reply(c.in.text)
	}
	return nil
}

func (c *mchat) dismiss(thread *store.Thread, q *store.Question) error {
	dq, err := c.s.store.DismissQuestion(c.ctx, c.user.ID, q.ID)
	if errors.Is(err, store.ErrInvalidState) {
		c.say("That question was already resolved.", nil)
		return nil
	}
	if err != nil {
		return err
	}
	c.s.bus.Publish(context.WithoutCancel(c.ctx), bus.Event{Type: bus.QuestionDismissed, UserID: c.user.ID.String(), ThreadID: thread.ID.String(), QuestionID: dq.ID.String()})
	_ = c.s.store.SetMessengerLastThread(c.ctx, c.link.UserID, thread.ID)
	c.sayAbout(thread.ID, c.s.messengerTag(c.ctx, c.link.UserID, thread)+" Skipped; the agent will use its judgement.", nil)
	return nil
}

// remarshal converts a decoded JSON value into a typed one.
func remarshal(from any, into any) error {
	raw, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, into)
}

// messengerTag renders "[#3 title]" for a thread.
func (s *Server) messengerTag(ctx context.Context, userID uuid.UUID, t *store.Thread) string {
	name := t.Title
	if name == "" {
		name = t.Agent
	}
	if name == "" {
		name = "thread"
	}
	h, err := s.store.MessengerHandle(ctx, userID, t.ID)
	if err != nil {
		return "[" + messenger.Clip(name, 40) + "]"
	}
	return "[#" + strconv.Itoa(h) + " " + messenger.Clip(name, 40) + "]"
}
