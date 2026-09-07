package api

import (
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ericflo/finalechat/internal/bus"
	"github.com/ericflo/finalechat/internal/store"
)

// Activity limits.
const (
	defaultActivityTTL = 45 * time.Second
	maxActivityTTL     = 600 * time.Second
)

type activityRequest struct {
	Text       string `json:"text"`
	Kind       string `json:"kind"`
	TTLSeconds *int   `json:"ttl_seconds"`
	// Title and Agent name a freshly auto-created external thread, as on
	// messages.
	Title string `json:"title"`
	Agent string `json:"agent"`
}

// POST /api/v1/threads/{thread}/activity
//
// Sets the agent's status line for the thread: {text, kind, ttl_seconds}.
// The status lapses after ttl_seconds unless refreshed, and is dropped as
// soon as the agent posts a message or a question. An empty text clears it.
func (s *Server) handleSetActivity(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var req activityRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	text := oneLine(req.Text)
	if !utf8.ValidString(text) {
		writeError(w, errValidation("text must be valid UTF-8."))
		return
	}
	if utf8.RuneCountInString(text) > store.MaxActivityTextRunes {
		writeError(w, errValidation("text must be at most %d characters; a status is one short line.", store.MaxActivityTextRunes))
		return
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	switch kind {
	case "":
		kind = store.ActivityWorking
	case store.ActivityThinking, store.ActivityWorking, store.ActivityTyping, store.ActivityWaiting, store.ActivityTool:
	default:
		writeError(w, errValidation("kind must be one of %s.", strings.Join(store.ActivityKinds, ", ")))
		return
	}
	ttl := defaultActivityTTL
	if req.TTLSeconds != nil {
		if *req.TTLSeconds < 1 || time.Duration(*req.TTLSeconds)*time.Second > maxActivityTTL {
			writeError(w, errValidation("ttl_seconds must be between 1 and %d.", int(maxActivityTTL/time.Second)))
			return
		}
		ttl = time.Duration(*req.TTLSeconds) * time.Second
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
	if text == "" {
		thread, err = s.store.ClearThreadActivity(r.Context(), p.user.ID, thread.ID)
	} else {
		thread, err = s.store.SetThreadActivity(r.Context(), p.user.ID, thread.ID, text, kind, ttl)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	s.bus.Publish(r.Context(), bus.Event{Type: bus.ThreadActivity, UserID: p.user.ID.String(), ThreadID: thread.ID.String()})
	writeJSON(w, http.StatusOK, map[string]any{"thread": thread})
}

// DELETE /api/v1/threads/{thread}/activity
func (s *Server) handleClearActivity(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	thread, _, err := s.resolveThread(r, false)
	if err != nil {
		writeError(w, err)
		return
	}
	had := thread.Activity != nil
	thread, err = s.store.ClearThreadActivity(r.Context(), p.user.ID, thread.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	if had {
		s.bus.Publish(r.Context(), bus.Event{Type: bus.ThreadActivity, UserID: p.user.ID.String(), ThreadID: thread.ID.String()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"thread": thread})
}

// oneLine collapses a status into a single trimmed line.
func oneLine(s string) string {
	fields := strings.FieldsFunc(s, unicode.IsSpace)
	return strings.Join(fields, " ")
}
