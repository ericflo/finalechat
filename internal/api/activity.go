package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
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

// activityBody is the status line as agents send it, standalone or inside a
// message or question.
type activityBody struct {
	Text       string `json:"text"`
	Kind       string `json:"kind"`
	TTLSeconds *int   `json:"ttl_seconds"`
	Seq        int64  `json:"seq"`
}

type activityRequest struct {
	activityBody
}

// parseActivity validates a status line. A blank text yields a nil input,
// which callers treat as "clear", with the seq the clear should record.
func parseActivity(b activityBody) (in *store.ActivityInput, clearSeq int64, err error) {
	if b.Seq < 0 {
		return nil, 0, errValidation("seq must not be negative.")
	}
	text := oneLine(b.Text)
	if text == "" {
		return nil, b.Seq, nil
	}
	if !utf8.ValidString(text) {
		return nil, 0, errValidation("activity text must be valid UTF-8.")
	}
	if utf8.RuneCountInString(text) > store.MaxActivityTextRunes {
		return nil, 0, errValidation("activity text must be at most %d characters; a status is one short line.", store.MaxActivityTextRunes)
	}
	kind := strings.ToLower(strings.TrimSpace(b.Kind))
	switch kind {
	case "":
		kind = store.ActivityWorking
	case store.ActivityThinking, store.ActivityWorking, store.ActivityTyping, store.ActivityWaiting, store.ActivityTool:
	default:
		return nil, 0, errValidation("activity kind must be one of %s.", strings.Join(store.ActivityKinds, ", "))
	}
	ttl := defaultActivityTTL
	if b.TTLSeconds != nil {
		if *b.TTLSeconds < 1 || time.Duration(*b.TTLSeconds)*time.Second > maxActivityTTL {
			return nil, 0, errValidation("ttl_seconds must be between 1 and %d.", int(maxActivityTTL/time.Second))
		}
		ttl = time.Duration(*b.TTLSeconds) * time.Second
	}
	return &store.ActivityInput{Text: text, Kind: kind, TTL: ttl, Seq: b.Seq}, 0, nil
}

// POST /api/v1/threads/{thread}/activity
//
// Sets the agent's status line for the thread: {text, kind, ttl_seconds,
// seq}. The status lapses after ttl_seconds unless refreshed, and is dropped
// as soon as the agent posts a message or a question. An empty text clears
// it. Unlike messages and questions this never creates a thread: a status
// belongs to a conversation that already exists.
func (s *Server) handleSetActivity(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var req activityRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	in, clearSeq, err := parseActivity(req.activityBody)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.limitWrite(p, s.activityLimiter); err != nil {
		writeError(w, err)
		return
	}
	thread, _, err := s.resolveThread(r, false)
	if err != nil {
		writeError(w, err)
		return
	}
	if in == nil {
		had := thread.Activity != nil
		thread, err = s.store.ClearThreadActivity(r.Context(), p.user.ID, thread.ID, clearSeq)
		if err != nil {
			writeError(w, err)
			return
		}
		if had {
			s.publishActivity(r.Context(), p.user.ID.String(), thread)
		}
		writeJSON(w, http.StatusOK, map[string]any{"thread": thread})
		return
	}
	// A repeat of the current status while it still has more than half its
	// life left changes nothing worth writing or announcing.
	if cur := thread.Activity; cur != nil && cur.Text == in.Text && cur.Kind == in.Kind && time.Until(cur.ExpiresAt) > in.TTL/2 {
		writeJSON(w, http.StatusOK, map[string]any{"thread": thread, "applied": false})
		return
	}
	thread, applied, err := s.store.SetThreadActivity(r.Context(), p.user.ID, thread.ID, *in)
	if err != nil {
		writeError(w, err)
		return
	}
	if applied {
		s.publishActivity(r.Context(), p.user.ID.String(), thread)
	}
	writeJSON(w, http.StatusOK, map[string]any{"thread": thread, "applied": applied})
}

// DELETE /api/v1/threads/{thread}/activity
func (s *Server) handleClearActivity(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	thread, _, err := s.resolveThread(r, false)
	if err != nil {
		writeError(w, err)
		return
	}
	// A clear may carry the same monotonic seq as writes (?seq=), so a status
	// write that was already in flight when the clear happened cannot land
	// afterwards and resurrect it.
	var seq int64
	if v := r.URL.Query().Get("seq"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeError(w, errValidation("seq must be a non-negative integer."))
			return
		}
		seq = n
	}
	had := thread.Activity != nil
	thread, err = s.store.ClearThreadActivity(r.Context(), p.user.ID, thread.ID, seq)
	if err != nil {
		writeError(w, err)
		return
	}
	if had {
		s.publishActivity(r.Context(), p.user.ID.String(), thread)
	}
	writeJSON(w, http.StatusOK, map[string]any{"thread": thread})
}

// publishActivity fans a thread's status out with the status inline, so no
// subscriber has to hit the database for what is a very frequent event.
func (s *Server) publishActivity(ctx context.Context, userID string, thread *store.Thread) {
	raw := json.RawMessage("null")
	if thread.Activity != nil {
		if b, err := json.Marshal(thread.Activity); err == nil {
			raw = b
		}
	}
	s.bus.Publish(ctx, bus.Event{Type: bus.ThreadActivity, UserID: userID, ThreadID: thread.ID.String(), Activity: raw})
}

// oneLine collapses a status into a single trimmed line.
func oneLine(s string) string {
	fields := strings.FieldsFunc(s, unicode.IsSpace)
	return strings.Join(fields, " ")
}
