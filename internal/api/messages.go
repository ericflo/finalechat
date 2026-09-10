package api

import (
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/ericflo/finalechat/internal/bus"
	"github.com/ericflo/finalechat/internal/push"
	"github.com/ericflo/finalechat/internal/store"
)

type messageRequest struct {
	Body       string          `json:"body"`
	Format     string          `json:"format"`
	Importance string          `json:"importance"`
	Sender     string          `json:"sender"`
	Notify     optionalBool    `json:"notify"`
	Meta       json.RawMessage `json:"meta"`
	// Title and Agent let a single request name a freshly auto-created
	// external thread.
	Title string `json:"title"`
	Agent string `json:"agent"`
	// Description (alias summary) sets the summary line on a freshly
	// auto-created external thread.
	Description string `json:"description"`
	Summary     string `json:"summary"`
	// Attachments are ids of pending uploads in the same thread.
	Attachments []string `json:"attachments"`
	// Activity sets the thread's status line in the same transaction, for an
	// agent that keeps working after it speaks. Absent, an agent message
	// clears the status.
	Activity *activityBody `json:"activity"`
	// ClientKey (or the Idempotency-Key header) makes a retried post return
	// the first message instead of a duplicate.
	ClientKey string `json:"client_key"`
}

// POST /api/v1/threads/{thread}/messages
//
// JSON: {body, format, importance, sender, notify, meta, title, agent,
// attachments: [ids]}. Or multipart/form-data with the same fields as form
// values plus file parts, which uploads and attaches in one request.
func (s *Server) handleCreateMessage(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var req messageRequest
	var form *multipart.Form
	if isMultipart(r) {
		var err error
		uploadDeadline(w)
		form, err = parseMultipart(r)
		if err != nil {
			writeError(w, err)
			return
		}
		defer form.RemoveAll()
		v := func(name string) string { return strings.TrimSpace(r.FormValue(name)) }
		req.Body, req.Format, req.Importance, req.Sender = v("body"), v("format"), v("importance"), v("sender")
		req.Title, req.Agent = v("title"), v("agent")
		req.Description, req.Summary = v("description"), v("summary")
		if m := v("meta"); m != "" {
			req.Meta = json.RawMessage(m)
		}
		if n := v("notify"); n != "" {
			b, err := strconv.ParseBool(n)
			if err != nil {
				writeError(w, errValidation("notify must be true or false."))
				return
			}
			req.Notify = optionalBool{Set: true, Value: b}
		}
		if ids := v("attachments"); ids != "" {
			req.Attachments = strings.Split(ids, ",")
		}
		if a := v("activity"); a != "" {
			var body activityBody
			if err := json.Unmarshal([]byte(a), &body); err != nil {
				writeError(w, errValidation("activity must be a JSON object."))
				return
			}
			req.Activity = &body
		}
		req.ClientKey = v("client_key")
	} else if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if k := strings.TrimSpace(r.Header.Get("Idempotency-Key")); k != "" {
		req.ClientKey = k
	}
	if len(req.ClientKey) > 200 {
		writeError(w, errValidation("client_key must be at most 200 characters."))
		return
	}
	hasFiles := form != nil && len(form.File) > 0
	if strings.TrimSpace(req.Body) == "" && len(req.Attachments) == 0 && !hasFiles {
		writeError(w, errValidation("body is required unless the message carries attachments."))
		return
	}
	if len(req.Body) > store.MaxBodyBytes {
		writeError(w, errValidation("body must be at most 256 KiB."))
		return
	}
	if !utf8.ValidString(req.Body) {
		writeError(w, errValidation("body must be valid UTF-8."))
		return
	}
	switch req.Format {
	case "":
		req.Format = "markdown"
	case "markdown", "text":
	default:
		writeError(w, errValidation("format must be \"markdown\" or \"text\"."))
		return
	}
	switch req.Importance {
	case "":
		req.Importance = store.ImportanceNormal
	case store.ImportanceNormal, store.ImportanceImportant:
	default:
		writeError(w, errValidation("importance must be \"normal\" or \"important\"."))
		return
	}
	switch req.Sender {
	case "":
		if p.viaToken() {
			req.Sender = store.SenderAgent
		} else {
			req.Sender = store.SenderUser
		}
	case store.SenderAgent, store.SenderUser, store.SenderSystem:
	default:
		writeError(w, errValidation("sender must be \"agent\", \"user\" or \"system\"."))
		return
	}
	meta, err := metaFrom(req.Meta)
	if err != nil {
		writeError(w, err)
		return
	}
	var activity *store.ActivityInput
	var clearSeq int64
	if req.Activity != nil {
		if activity, clearSeq, err = parseActivity(*req.Activity); err != nil {
			writeError(w, err)
			return
		}
	}
	if err := s.limitWrite(p, s.messageLimiter); err != nil {
		writeError(w, err)
		return
	}
	attachmentIDs := make([]uuid.UUID, 0, len(req.Attachments))
	for _, raw := range req.Attachments {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, errValidation("attachments must be attachment ids."))
			return
		}
		attachmentIDs = append(attachmentIDs, id)
	}
	// Everything above is validated before the thread is auto-created so a
	// rejected post leaves no empty "Untitled thread" behind. The checks
	// below need the thread id (uploads) or the database (attachment
	// ownership); their failures roll the fresh thread back instead.
	if len(req.Title) > 300 {
		writeError(w, errValidation("title must be at most 300 characters."))
		return
	}
	if len(req.Agent) > 120 {
		writeError(w, errValidation("agent must be at most 120 characters."))
		return
	}
	if len(req.Description) > 2000 {
		writeError(w, errValidation("description must be at most 2000 characters."))
		return
	}
	if len(req.Summary) > 2000 {
		writeError(w, errValidation("summary must be at most 2000 characters."))
		return
	}
	if len(attachmentIDs) > store.MaxAttachmentsPerMessage {
		writeError(w, errValidation("At most %d attachments per message.", store.MaxAttachmentsPerMessage))
		return
	}
	if hasFiles {
		n := 0
		for _, list := range form.File {
			n += len(list)
		}
		if n > store.MaxAttachmentsPerMessage {
			writeError(w, errValidation("At most %d attachments per message.", store.MaxAttachmentsPerMessage))
			return
		}
	}
	thread, threadCreated, err := s.resolveThread(r, true)
	if err != nil {
		writeError(w, err)
		return
	}
	if threadCreated && (req.Title != "" || req.Agent != "" || req.Description != "" || req.Summary != "") {
		patch := store.ThreadPatch{}
		if req.Title != "" {
			patch.Title = &req.Title
		}
		if req.Agent != "" {
			patch.Agent = &req.Agent
		}
		if desc := threadDescription(req.Description, req.Summary); desc != "" {
			patch.Description = &desc
		}
		if t, err := s.store.UpdateThread(r.Context(), p.user.ID, thread.ID, patch); err == nil {
			thread = t
		}
	}
	// A retried multipart post must not upload its files again: look the key
	// up first. (Two truly concurrent retries can both miss this; the
	// created=false path below cleans up whatever they stored.)
	if req.ClientKey != "" {
		if existing, err := s.store.FindMessageByClientKey(r.Context(), p.user.ID, thread.ID, req.ClientKey); err == nil {
			decorate(existing)
			applied := s.replayActivity(r.Context(), p.user.ID, thread.ID, activity)
			thread, _ = s.store.GetThread(r.Context(), p.user.ID, thread.ID)
			writeJSON(w, http.StatusOK, map[string]any{"message": existing, "thread": thread, "created": false, "applied": applied})
			return
		}
	}
	var uploaded []*store.Attachment
	if hasFiles {
		var err error
		uploaded, err = s.readUploads(r, thread.ID, form)
		if err != nil {
			s.failAutoCreated(w, r, err, thread, threadCreated)
			return
		}
		for _, a := range uploaded {
			attachmentIDs = append(attachmentIDs, a.ID)
		}
	}
	if len(attachmentIDs) > store.MaxAttachmentsPerMessage {
		s.failAutoCreated(w, r, errValidation("At most %d attachments per message.", store.MaxAttachmentsPerMessage), thread, threadCreated)
		return
	}
	origin := store.OriginToken
	if !p.viaToken() {
		origin = store.OriginSession
	}
	msg, updatedThread, created, err := s.store.CreateMessage(r.Context(), p.user.ID, thread.ID, store.MessageInput{
		Sender: req.Sender, Body: req.Body, Format: req.Format, Importance: req.Importance, Meta: meta, AttachmentIDs: attachmentIDs,
		Origin: origin, MarkRead: req.Sender == store.SenderUser && origin == store.OriginSession,
		ClientKey: req.ClientKey, Activity: activity, ClearSeq: clearSeq,
	})
	if errors.Is(err, store.ErrNotFound) && len(attachmentIDs) > 0 {
		// CreateMessage threads the insert and the thread touch through one
		// transaction, so on error it hands back no thread; the cleanup
		// still targets the auto-created one above.
		s.failAutoCreated(w, r, errValidation("One or more attachments are unknown, belong to another thread, or are already attached."), thread, threadCreated)
		return
	}
	if err != nil {
		s.failAutoCreated(w, r, err, thread, threadCreated)
		return
	}
	thread = updatedThread
	decorate(msg)
	if !created {
		// A retry of a post that already landed: nothing new to announce,
		// but the retry's status line still counts, and any files this
		// request stored are orphans.
		if len(uploaded) > 0 {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()
				for _, a := range uploaded {
					_ = s.store.DeleteAttachment(ctx, a.ID)
				}
				s.deleteAttachmentObjects(ctx, uploaded)
			}()
		}
		applied := s.replayActivity(r.Context(), p.user.ID, thread.ID, activity)
		if applied {
			thread, _ = s.store.GetThread(r.Context(), p.user.ID, thread.ID)
		}
		writeJSON(w, http.StatusOK, map[string]any{"message": msg, "thread": thread, "created": false, "applied": applied})
		return
	}
	bg := context.WithoutCancel(r.Context())
	s.bus.Publish(bg, bus.Event{Type: bus.MessageCreated, UserID: p.user.ID.String(), ThreadID: thread.ID.String(), MessageID: msg.ID.String()})
	s.notifyMessage(bg, p.user, thread, msg, req.Notify.ptr())
	writeJSON(w, http.StatusCreated, map[string]any{"message": msg, "thread": thread, "created": true})
}

// replayActivity applies the status line carried by a post whose message
// already existed (an idempotent replay), through the usual ordering gate.
func (s *Server) replayActivity(ctx context.Context, userID, threadID uuid.UUID, in *store.ActivityInput) bool {
	if in == nil {
		return false
	}
	thread, applied, err := s.store.SetThreadActivity(ctx, userID, threadID, *in)
	if err != nil || !applied {
		return false
	}
	s.publishActivity(context.WithoutCancel(ctx), userID.String(), thread)
	return true
}

// notifyMessage applies the notification policy for a new message.
func (s *Server) notifyMessage(ctx context.Context, user *store.User, thread *store.Thread, msg *store.Message, notify *bool) {
	if msg.Sender == store.SenderUser || thread.Muted || !s.push.Enabled() {
		return
	}
	should := msg.Importance == store.ImportanceImportant || user.Settings.NotifyAllMessages
	if notify != nil {
		should = *notify
	}
	if !should {
		return
	}
	badge := s.badge(ctx, user.ID)
	body := store.Preview(msg.Body)
	if len(msg.Attachments) > 0 {
		label := "📎 Attachment"
		if msg.Attachments[0].Kind == store.AttachmentImage {
			label = "📷 Image"
		}
		if body == "" {
			body = label
		} else {
			body = label + " · " + body
		}
	}
	s.push.Send(user.ID, push.Notification{
		Type:      "message",
		Title:     threadTitle(thread),
		Body:      body,
		URL:       s.cfg.BaseURL + "/t/" + thread.ID.String(),
		Tag:       "thread:" + thread.ID.String(),
		ThreadID:  thread.ID.String(),
		MessageID: msg.ID.String(),
		Important: msg.Importance == store.ImportanceImportant,
		Badge:     badge,
	})
}

// badge is the app-icon count to send with a notification, or nil when the
// count is unknown (a nil badge leaves the icon alone; a zero would clear it).
func (s *Server) badge(ctx context.Context, userID uuid.UUID) *int {
	counts, err := s.store.GetCounts(ctx, userID)
	if err != nil {
		s.log.Warn("badge counts", "err", err)
		return nil
	}
	n := counts.Attention
	return &n
}

func threadTitle(t *store.Thread) string {
	switch {
	case t.Title != "" && t.Agent != "":
		return t.Title + " · " + t.Agent
	case t.Title != "":
		return t.Title
	case t.Agent != "":
		return t.Agent
	}
	return "Finalechat"
}

// GET /api/v1/threads/{thread}/messages
func (s *Server) handleListMessages(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	thread, _, err := s.resolveThread(r, false)
	if err != nil {
		writeError(w, err)
		return
	}
	q := r.URL.Query()
	page := store.MessagePage{Sender: q.Get("sender")}
	if page.Sender != "" && page.Sender != store.SenderAgent && page.Sender != store.SenderUser && page.Sender != store.SenderSystem {
		writeError(w, errValidation("sender must be \"agent\", \"user\" or \"system\"."))
		return
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			writeError(w, errValidation("limit must be between 1 and 500."))
			return
		}
		page.Limit = n
	}
	if v := q.Get("after"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeError(w, errValidation("after must be a message id."))
			return
		}
		page.After = &id
	}
	if v := q.Get("before"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeError(w, errValidation("before must be a message id."))
			return
		}
		page.Before = &id
	}
	wait, err := parseWait(q.Get("wait"), 0)
	if err != nil {
		writeError(w, err)
		return
	}
	if v := q.Get("after_time"); v != "" && page.After == nil {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			writeError(w, errValidation("after_time must be an RFC 3339 timestamp."))
			return
		}
		page.AfterTime = &t
		page.Before = nil
	}
	if wait > 0 && page.After == nil && page.AfterTime == nil {
		// No anchor: wait for anything that arrives from now on, by the
		// database's clock so no skew with created_at can hide a message.
		now, err := s.store.Now(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		page.AfterTime = &now
		page.Before = nil
	}

	// Subscribe before querying so a message that lands between the query and
	// the wait is not missed.
	var events <-chan bus.Event
	var cancel func()
	if wait > 0 {
		events, cancel = s.bus.Subscribe(p.user.ID.String())
		defer cancel()
	}
	deadline := time.Now().Add(wait)
	for {
		msgs, hasMore, anchorUnknown, err := s.store.ListMessages(r.Context(), p.user.ID, thread.ID, page)
		if err != nil {
			writeError(w, err)
			return
		}
		if len(msgs) > 0 || wait == 0 || time.Now().After(deadline) {
			decorate(msgs...)
			resp := map[string]any{"messages": msgs, "has_more": hasMore, "thread_id": thread.ID}
			if anchorUnknown {
				// The anchor is not in this thread (deleted and recreated
				// under the same ext: id, say); the page ran from the start.
				resp["anchor_unknown"] = true
			}
			if page.AfterTime != nil && len(msgs) == 0 {
				// Only when nothing came back: once messages exist the caller
				// continues from the last message id, not from this instant.
				resp["waited_from"] = page.AfterTime.UTC()
			}
			writeJSON(w, http.StatusOK, resp)
			return
		}
		if !s.waitForEvent(r.Context(), events, deadline, func(ev bus.Event) bool {
			return ev.Type == bus.MessageCreated && ev.ThreadID == thread.ID.String()
		}) {
			resp := map[string]any{"messages": []any{}, "has_more": false, "thread_id": thread.ID, "timed_out": true}
			if anchorUnknown {
				resp["anchor_unknown"] = true
			}
			if page.AfterTime != nil {
				// Pass this back as after_time so the next wait continues from
				// the same instant instead of re-anchoring on "now".
				resp["waited_from"] = page.AfterTime.UTC()
			}
			writeJSON(w, http.StatusOK, resp)
			return
		}
	}
}

// requeryFloor bounds how long a long-poll trusts the event bus before it
// looks at the database again, so a lost notification costs seconds rather
// than the whole wait.
const requeryFloor = 10 * time.Second

// waitForEvent blocks until an event matching pred arrives or the re-query
// floor elapses (both return true: look again), or until the deadline
// passes, the request is cancelled, or the server is shutting down (false).
func (s *Server) waitForEvent(ctx context.Context, events <-chan bus.Event, deadline time.Time, pred func(bus.Event) bool) bool {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	floor := time.NewTimer(requeryFloor)
	defer floor.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-s.shutdown:
			return false
		case <-timer.C:
			return false
		case <-floor.C:
			return true
		case ev, ok := <-events:
			if !ok {
				return false
			}
			if pred(ev) {
				return true
			}
		}
	}
}

// DELETE /api/v1/messages/{id}
//
// Removes a message (and its attachments) for good: the recovery path for
// output that should never have reached the phone.
func (s *Server) handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.limitWrite(p, s.messageLimiter); err != nil {
		writeError(w, err)
		return
	}
	msg, thread, attachments, err := s.store.DeleteMessage(r.Context(), p.user.ID, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if len(attachments) > 0 {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			s.deleteAttachmentObjects(ctx, attachments)
		}()
	}
	s.bus.Publish(context.WithoutCancel(r.Context()), bus.Event{Type: bus.MessageDeleted, UserID: p.user.ID.String(), ThreadID: thread.ID.String(), MessageID: msg.ID.String()})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "thread": thread})
}

// GET /api/v1/messages/{id}
func (s *Server) handleGetMessage(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	msg, err := s.store.GetMessage(r.Context(), p.user.ID, id)
	if err != nil {
		writeError(w, err)
		return
	}
	decorate(msg)
	writeJSON(w, http.StatusOK, map[string]any{"message": msg})
}
