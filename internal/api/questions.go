package api

import (
	"context"
	"encoding/json"
	"errors"
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

type questionRequest struct {
	Prompt         string                 `json:"prompt"`
	Options        []store.QuestionOption `json:"options"`
	AllowFreeform  optionalBool           `json:"allow_freeform"`
	MultiSelect    optionalBool           `json:"multi_select"`
	TimeoutSeconds *int                   `json:"timeout_seconds"`
	Wait           *int                   `json:"wait"`
	Meta           json.RawMessage        `json:"meta"`
	Title          string                 `json:"title"`
	Agent          string                 `json:"agent"`
	Description    string                 `json:"description"`
	Summary        string                 `json:"summary"`
	// Activity keeps a status line on the thread while the agent waits (it
	// may keep working); absent, asking clears the status.
	Activity *activityBody `json:"activity"`
	// ClientKey (or the Idempotency-Key header) makes a retried ask return
	// the first question instead of a duplicate.
	ClientKey string `json:"client_key"`
}

// defaultQuestionLifetime bounds a question whose asker set no timeout, so a
// question from an agent that died cannot stay pending forever.
const defaultQuestionLifetime = 7 * 24 * time.Hour

func (req *questionRequest) validate() (store.QuestionInput, error) {
	var in store.QuestionInput
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		return in, errValidation("prompt is required.")
	}
	if len(req.Prompt) > 8000 || !utf8.ValidString(req.Prompt) {
		return in, errValidation("prompt must be valid UTF-8 of at most 8000 bytes.")
	}
	if len(req.Options) > 20 {
		return in, errValidation("At most 20 options are allowed.")
	}
	seen := map[string]bool{}
	options := make([]store.QuestionOption, 0, len(req.Options))
	for i, o := range req.Options {
		o.Label = strings.TrimSpace(o.Label)
		o.Description = strings.TrimSpace(o.Description)
		if o.Label == "" {
			return in, errValidation("options[%d].label is required.", i)
		}
		// Characters, as documented and as the app measures them; bytes
		// would reject a label of 200 accented or CJK characters.
		if !utf8.ValidString(o.Label) || utf8.RuneCountInString(o.Label) > 200 {
			return in, errValidation("options[%d].label must be valid UTF-8 of at most 200 characters.", i)
		}
		if !utf8.ValidString(o.Description) || utf8.RuneCountInString(o.Description) > 1000 {
			return in, errValidation("options[%d].description must be valid UTF-8 of at most 1000 characters.", i)
		}
		if seen[o.Label] {
			return in, errValidation("options[%d].label %q is duplicated.", i, o.Label)
		}
		seen[o.Label] = true
		options = append(options, o)
	}
	in.Prompt = req.Prompt
	in.Options = options
	in.AllowFreeform = true
	if req.AllowFreeform.Set {
		in.AllowFreeform = req.AllowFreeform.Value
	}
	if len(options) == 0 {
		in.AllowFreeform = true
	}
	in.MultiSelect = req.MultiSelect.Set && req.MultiSelect.Value
	lifetime := defaultQuestionLifetime
	if req.TimeoutSeconds != nil {
		if *req.TimeoutSeconds < 1 || *req.TimeoutSeconds > 7*86400 {
			return in, errValidation("timeout_seconds must be between 1 and 604800.")
		}
		lifetime = time.Duration(*req.TimeoutSeconds) * time.Second
	}
	t := time.Now().Add(lifetime)
	in.ExpiresAt = &t
	meta, err := metaFrom(req.Meta)
	if err != nil {
		return in, err
	}
	in.Meta = meta
	if req.Activity != nil {
		if in.Activity, in.ClearSeq, err = parseActivity(*req.Activity); err != nil {
			return in, err
		}
	}
	if len(req.ClientKey) > 200 {
		return in, errValidation("client_key must be at most 200 characters.")
	}
	in.ClientKey = req.ClientKey
	return in, nil
}

// POST /api/v1/threads/{thread}/questions
func (s *Server) handleCreateQuestion(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var req questionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if k := strings.TrimSpace(r.Header.Get("Idempotency-Key")); k != "" {
		req.ClientKey = k
	}
	in, err := req.validate()
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.limitWrite(p, s.questionLimiter); err != nil {
		writeError(w, err)
		return
	}
	waitRaw := r.URL.Query().Get("wait")
	if req.Wait != nil {
		waitRaw = strconv.Itoa(*req.Wait)
	}
	wait, err := parseWait(waitRaw, 0)
	if err != nil {
		writeError(w, err)
		return
	}
	// Title/agent ride along for a freshly auto-created thread; bound them
	// before creating so a rejected name leaves no empty thread behind.
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

	// Subscribe before creating so the answer cannot slip past the wait.
	var events <-chan bus.Event
	var cancel func()
	if wait > 0 {
		events, cancel = s.bus.Subscribe(p.user.ID.String())
		defer cancel()
	}
	q, updatedThread, created, err := s.store.CreateQuestion(r.Context(), p.user.ID, thread.ID, in)
	if err != nil {
		// Like CreateMessage, a failed create hands back no thread; the
		// cleanup still targets the auto-created one above.
		s.failAutoCreated(w, r, err, thread, threadCreated)
		return
	}
	thread = updatedThread
	status := http.StatusOK
	if created {
		status = http.StatusCreated
		s.bus.Publish(context.WithoutCancel(r.Context()), bus.Event{Type: bus.QuestionCreated, UserID: p.user.ID.String(), ThreadID: thread.ID.String(), QuestionID: q.ID.String()})
		s.notifyQuestion(thread, q)
	}

	if wait > 0 {
		q = s.awaitQuestion(r, events, q, wait)
	}
	writeJSON(w, status, map[string]any{"question": q, "thread": thread, "created": created})
}

// awaitQuestion blocks until the question leaves the pending state or the
// wait elapses, returning the latest state either way.
func (s *Server) awaitQuestion(r *http.Request, events <-chan bus.Event, q *store.Question, wait time.Duration) *store.Question {
	p := principalFrom(r.Context())
	deadline := time.Now().Add(wait)
	for q.Status == store.QuestionPending {
		matched := s.waitForEvent(r.Context(), events, deadline, func(ev bus.Event) bool {
			return ev.QuestionID == q.ID.String() && ev.Type != bus.QuestionCreated
		})
		latest, err := s.store.GetQuestion(r.Context(), p.user.ID, q.ID)
		if err == nil {
			q = latest
		}
		if !matched {
			break
		}
	}
	return q
}

func (s *Server) notifyQuestion(thread *store.Thread, q *store.Question) {
	if thread.Muted || !s.push.Enabled() {
		return
	}
	badge := s.badge(context.Background(), thread.UserID)
	// Notification actions fit two or three short choices; a longer list is
	// answered in the app, and a long list would push the payload past the
	// one record a push service accepts.
	var options []string
	if len(q.Options) <= 3 {
		for _, o := range q.Options {
			options = append(options, truncateRunes(o.Label, 60))
		}
	}
	s.push.Send(thread.UserID, push.Notification{
		Type:       "question",
		Title:      threadTitle(thread),
		Body:       store.Preview(q.Prompt),
		URL:        s.cfg.BaseURL + "/t/" + thread.ID.String() + "?q=" + q.ID.String(),
		Tag:        "question:" + q.ID.String(),
		ThreadID:   thread.ID.String(),
		QuestionID: q.ID.String(),
		Options:    options,
		Important:  true,
		Badge:      badge,
	})
}

// truncateRunes cuts s to at most n characters, marking the cut.
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n-1]) + "…"
}

// GET /api/v1/questions/{id}
func (s *Server) handleGetQuestion(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	wait, err := parseWait(r.URL.Query().Get("wait"), 0)
	if err != nil {
		writeError(w, err)
		return
	}
	var events <-chan bus.Event
	var cancel func()
	if wait > 0 {
		events, cancel = s.bus.Subscribe(p.user.ID.String())
		defer cancel()
	}
	q, err := s.store.GetQuestion(r.Context(), p.user.ID, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if wait > 0 && q.Status == store.QuestionPending {
		q = s.awaitQuestion(r, events, q, wait)
	}
	writeJSON(w, http.StatusOK, map[string]any{"question": q})
}

// GET /api/v1/questions
func (s *Server) handleListQuestions(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	qs := r.URL.Query()
	status := qs.Get("status")
	switch status {
	case "", store.QuestionPending, store.QuestionAnswered, store.QuestionCancelled, store.QuestionExpired, store.QuestionDismissed:
	default:
		writeError(w, errValidation("status must be pending, answered, cancelled, expired or dismissed."))
		return
	}
	var threadID *uuid.UUID
	if v := qs.Get("thread_id"); v != "" {
		if strings.HasPrefix(v, "ext:") {
			t, err := s.store.GetThreadByExternalID(r.Context(), p.user.ID, strings.TrimPrefix(v, "ext:"))
			if err != nil {
				writeError(w, err)
				return
			}
			threadID = &t.ID
		} else {
			id, err := uuid.Parse(v)
			if err != nil {
				writeError(w, errValidation("thread_id must be a thread id or ext:<external_id>."))
				return
			}
			threadID = &id
		}
	}
	limit := 0
	if v := qs.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			writeError(w, errValidation("limit must be between 1 and 500."))
			return
		}
		limit = n
	}
	attention := qs.Get("attention") == "true" || qs.Get("attention") == "1"
	questions, err := s.store.ListQuestions(r.Context(), p.user.ID, threadID, status, limit, attention)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"questions": questions})
}

// GET /api/v1/threads/{thread}/questions
func (s *Server) handleListThreadQuestions(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	thread, _, err := s.resolveThread(r, false)
	if err != nil {
		writeError(w, err)
		return
	}
	questions, err := s.store.ListThreadQuestions(r.Context(), p.user.ID, thread.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"questions": questions})
}

type answerRequest struct {
	Selected []string `json:"selected"`
	Text     string   `json:"text"`
	// Meta carries client-supplied message metadata. Only `eagent.*` keys
	// (the eagent capability handshake, v1) are kept, merged onto the
	// transcript message the server synthesizes for the answer.
	Meta json.RawMessage `json:"meta"`
}

// POST /api/v1/questions/{id}/answer
func (s *Server) handleAnswerQuestion(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var req answerRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	q, err := s.store.GetQuestion(r.Context(), p.user.ID, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if q.Status != store.QuestionPending {
		writeError(w, &apiError{Status: http.StatusConflict, Code: "already_resolved", Message: "This question is " + q.Status + "."})
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if len(req.Text) > 32*1024 || !utf8.ValidString(req.Text) {
		writeError(w, errValidation("text must be valid UTF-8 of at most 32 KiB."))
		return
	}
	valid := map[string]bool{}
	for _, o := range q.Options {
		valid[o.Label] = true
	}
	selected := make([]string, 0, len(req.Selected))
	seen := map[string]bool{}
	for _, label := range req.Selected {
		label = strings.TrimSpace(label)
		if !valid[label] {
			writeError(w, errValidation("%q is not one of the offered options.", label))
			return
		}
		if !seen[label] {
			seen[label] = true
			selected = append(selected, label)
		}
	}
	if len(selected) > 1 && !q.MultiSelect {
		writeError(w, errValidation("This question accepts a single selection."))
		return
	}
	if req.Text != "" && !q.AllowFreeform {
		writeError(w, errValidation("This question does not accept free-form text."))
		return
	}
	if len(selected) == 0 && req.Text == "" {
		writeError(w, errValidation("Choose an option or write a reply."))
		return
	}
	clientMeta, err := metaFrom(req.Meta)
	if err != nil {
		writeError(w, err)
		return
	}
	origin := store.OriginToken
	if !p.viaToken() {
		origin = store.OriginSession
	}
	// The answer, its transcript message (so agents that only poll messages
	// still see it) and the thread bump commit together; the events follow.
	answered, msg, thread, err := s.store.AnswerQuestion(r.Context(), p.user.ID, id, store.Answer{Selected: selected, Text: req.Text}, origin, clientMeta)
	if errors.Is(err, store.ErrInvalidState) {
		writeError(w, &apiError{Status: http.StatusConflict, Code: "already_resolved", Message: "This question has already been resolved."})
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	decorate(msg)
	bg := context.WithoutCancel(r.Context())
	s.bus.Publish(bg, bus.Event{Type: bus.QuestionAnswered, UserID: p.user.ID.String(), ThreadID: thread.ID.String(), QuestionID: answered.ID.String()})
	s.bus.Publish(bg, bus.Event{Type: bus.MessageCreated, UserID: p.user.ID.String(), ThreadID: thread.ID.String(), MessageID: msg.ID.String()})
	writeJSON(w, http.StatusOK, map[string]any{"question": answered, "message": msg, "thread": thread})
}

// POST /api/v1/questions/{id}/cancel
//
// The agent withdraws a question it no longer needs answered.
func (s *Server) handleCancelQuestion(w http.ResponseWriter, r *http.Request) {
	s.resolveQuestion(w, r, store.QuestionCancelled)
}

// POST /api/v1/questions/{id}/dismiss
//
// The user declines to answer; the agent sees status "dismissed".
func (s *Server) handleDismissQuestion(w http.ResponseWriter, r *http.Request) {
	s.resolveQuestion(w, r, store.QuestionDismissed)
}

func (s *Server) resolveQuestion(w http.ResponseWriter, r *http.Request, status string) {
	p := principalFrom(r.Context())
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var q *store.Question
	eventType := bus.QuestionCancelled
	if status == store.QuestionDismissed {
		q, err = s.store.DismissQuestion(r.Context(), p.user.ID, id)
		eventType = bus.QuestionDismissed
	} else {
		q, err = s.store.CancelQuestion(r.Context(), p.user.ID, id)
	}
	if errors.Is(err, store.ErrInvalidState) {
		writeError(w, &apiError{Status: http.StatusConflict, Code: "already_resolved", Message: "This question has already been resolved."})
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	s.bus.Publish(context.WithoutCancel(r.Context()), bus.Event{Type: eventType, UserID: p.user.ID.String(), ThreadID: q.ThreadID.String(), QuestionID: q.ID.String()})
	writeJSON(w, http.StatusOK, map[string]any{"question": q})
}
