package api

import (
	"context"
	"encoding/json"
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
}

// POST /api/v1/threads/{thread}/messages
func (s *Server) handleCreateMessage(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var req messageRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		writeError(w, errValidation("body is required."))
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
	thread, created, err := s.resolveThread(r, true)
	if err != nil {
		writeError(w, err)
		return
	}
	if created && (req.Title != "" || req.Agent != "") {
		patch := store.ThreadPatch{}
		if req.Title != "" {
			patch.Title = &req.Title
		}
		if req.Agent != "" {
			patch.Agent = &req.Agent
		}
		if t, err := s.store.UpdateThread(r.Context(), p.user.ID, thread.ID, patch); err == nil {
			thread = t
		}
	}
	msg, thread, err := s.store.CreateMessage(r.Context(), p.user.ID, thread.ID, store.MessageInput{
		Sender: req.Sender, Body: req.Body, Format: req.Format, Importance: req.Importance, Meta: meta,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	s.bus.Publish(r.Context(), bus.Event{Type: bus.MessageCreated, UserID: p.user.ID.String(), ThreadID: thread.ID.String(), MessageID: msg.ID.String()})
	s.notifyMessage(r.Context(), p.user, thread, msg, req.Notify.ptr())
	writeJSON(w, http.StatusCreated, map[string]any{"message": msg, "thread": thread})
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
	counts, _ := s.store.GetCounts(ctx, user.ID)
	s.push.Send(user.ID, push.Notification{
		Type:      "message",
		Title:     threadTitle(thread),
		Body:      store.Preview(msg.Body),
		URL:       s.cfg.BaseURL + "/t/" + thread.ID.String(),
		Tag:       "thread:" + thread.ID.String(),
		ThreadID:  thread.ID.String(),
		MessageID: msg.ID.String(),
		Important: msg.Importance == store.ImportanceImportant,
		Badge:     counts.PendingQuestions + counts.UnreadThreads,
	})
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
	if wait > 0 && page.After == nil {
		writeError(w, errValidation("wait requires the after parameter so the server knows what you have already seen."))
		return
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
		msgs, hasMore, err := s.store.ListMessages(r.Context(), p.user.ID, thread.ID, page)
		if err != nil {
			writeError(w, err)
			return
		}
		if len(msgs) > 0 || wait == 0 || time.Now().After(deadline) {
			writeJSON(w, http.StatusOK, map[string]any{"messages": msgs, "has_more": hasMore, "thread_id": thread.ID})
			return
		}
		if !waitForEvent(r.Context(), events, deadline, func(ev bus.Event) bool {
			return ev.Type == bus.MessageCreated && ev.ThreadID == thread.ID.String()
		}) {
			writeJSON(w, http.StatusOK, map[string]any{"messages": []any{}, "has_more": false, "thread_id": thread.ID, "timed_out": true})
			return
		}
	}
}

// waitForEvent blocks until an event matching pred arrives, the deadline
// passes, or the request is cancelled. It returns true when an event matched.
func waitForEvent(ctx context.Context, events <-chan bus.Event, deadline time.Time, pred func(bus.Event) bool) bool {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return false
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
	writeJSON(w, http.StatusOK, map[string]any{"message": msg})
}
