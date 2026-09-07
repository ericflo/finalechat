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
}

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
		if len(o.Label) > 200 {
			return in, errValidation("options[%d].label must be at most 200 characters.", i)
		}
		if len(o.Description) > 1000 {
			return in, errValidation("options[%d].description must be at most 1000 characters.", i)
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
	if req.TimeoutSeconds != nil {
		if *req.TimeoutSeconds < 1 || *req.TimeoutSeconds > 7*86400 {
			return in, errValidation("timeout_seconds must be between 1 and 604800.")
		}
		t := time.Now().Add(time.Duration(*req.TimeoutSeconds) * time.Second)
		in.ExpiresAt = &t
	}
	meta, err := metaFrom(req.Meta)
	if err != nil {
		return in, err
	}
	in.Meta = meta
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
	in, err := req.validate()
	if err != nil {
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

	// Subscribe before creating so the answer cannot slip past the wait.
	var events <-chan bus.Event
	var cancel func()
	if wait > 0 {
		events, cancel = s.bus.Subscribe(p.user.ID.String())
		defer cancel()
	}
	q, thread, err := s.store.CreateQuestion(r.Context(), p.user.ID, thread.ID, in)
	if err != nil {
		writeError(w, err)
		return
	}
	s.bus.Publish(r.Context(), bus.Event{Type: bus.QuestionCreated, UserID: p.user.ID.String(), ThreadID: thread.ID.String(), QuestionID: q.ID.String()})
	s.notifyQuestion(thread, q)

	if wait > 0 {
		q = s.awaitQuestion(r, events, q, wait)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"question": q, "thread": thread})
}

// awaitQuestion blocks until the question leaves the pending state or the
// wait elapses, returning the latest state either way.
func (s *Server) awaitQuestion(r *http.Request, events <-chan bus.Event, q *store.Question, wait time.Duration) *store.Question {
	p := principalFrom(r.Context())
	deadline := time.Now().Add(wait)
	for q.Status == store.QuestionPending {
		matched := waitForEvent(r.Context(), events, deadline, func(ev bus.Event) bool {
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
	counts, _ := s.store.GetCounts(context.Background(), thread.UserID)
	options := make([]string, 0, len(q.Options))
	for _, o := range q.Options {
		options = append(options, o.Label)
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
		Badge:      counts.PendingQuestions + counts.UnreadThreads,
	})
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
	case "", store.QuestionPending, store.QuestionAnswered, store.QuestionCancelled, store.QuestionExpired:
	default:
		writeError(w, errValidation("status must be pending, answered, cancelled or expired."))
		return
	}
	var threadID *uuid.UUID
	if v := qs.Get("thread_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeError(w, errValidation("thread_id must be a thread id."))
			return
		}
		threadID = &id
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
	questions, err := s.store.ListQuestions(r.Context(), p.user.ID, threadID, status, limit)
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
	answered, err := s.store.AnswerQuestion(r.Context(), p.user.ID, id, store.Answer{Selected: selected, Text: req.Text})
	if err != nil {
		writeError(w, err)
		return
	}
	// Record the answer in the thread as a user message so the transcript reads
	// naturally and agents that only poll messages still see it.
	var body strings.Builder
	if len(selected) > 0 {
		body.WriteString(strings.Join(selected, ", "))
	}
	if req.Text != "" {
		if body.Len() > 0 {
			body.WriteString(" — ")
		}
		body.WriteString(req.Text)
	}
	msg, thread, err := s.store.CreateMessage(r.Context(), p.user.ID, answered.ThreadID, store.MessageInput{
		Sender: store.SenderUser, Body: body.String(), Format: "text", Importance: store.ImportanceNormal,
		Meta: store.JSON{"question_id": answered.ID.String(), "kind": "answer"},
	})
	if err != nil {
		writeError(w, err)
		return
	}
	s.bus.Publish(r.Context(), bus.Event{Type: bus.QuestionAnswered, UserID: p.user.ID.String(), ThreadID: thread.ID.String(), QuestionID: answered.ID.String()})
	s.bus.Publish(r.Context(), bus.Event{Type: bus.MessageCreated, UserID: p.user.ID.String(), ThreadID: thread.ID.String(), MessageID: msg.ID.String()})
	writeJSON(w, http.StatusOK, map[string]any{"question": answered, "message": msg, "thread": thread})
}

// POST /api/v1/questions/{id}/cancel
func (s *Server) handleCancelQuestion(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	q, err := s.store.CancelQuestion(r.Context(), p.user.ID, id)
	if err != nil {
		writeError(w, err)
		return
	}
	s.bus.Publish(r.Context(), bus.Event{Type: bus.QuestionCancelled, UserID: p.user.ID.String(), ThreadID: q.ThreadID.String(), QuestionID: q.ID.String()})
	writeJSON(w, http.StatusOK, map[string]any{"question": q})
}
