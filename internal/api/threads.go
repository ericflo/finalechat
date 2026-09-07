package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ericflo/finalechat/internal/bus"
	"github.com/ericflo/finalechat/internal/store"
)

func parseUUID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return uuid.UUID{}, &apiError{Status: http.StatusNotFound, Code: "not_found", Message: "Not found: malformed id."}
	}
	return id, nil
}

// resolveThread turns a {thread} path value into a thread. It accepts a UUID
// or `ext:<external_id>`; with autoCreate, a missing external thread is created
// so an agent can post with a single request.
func (s *Server) resolveThread(r *http.Request, autoCreate bool) (*store.Thread, bool, error) {
	p := principalFrom(r.Context())
	ref := strings.TrimSpace(r.PathValue("thread"))
	if strings.HasPrefix(ref, "ext:") {
		ext := strings.TrimPrefix(ref, "ext:")
		if ext == "" {
			return nil, false, errBadRequest("External id is empty.")
		}
		t, err := s.store.GetThreadByExternalID(r.Context(), p.user.ID, ext)
		if err == store.ErrNotFound && autoCreate {
			t, created, err := s.store.CreateThread(r.Context(), p.user.ID, store.ThreadInput{ExternalID: ext})
			if err != nil {
				return nil, false, err
			}
			if created {
				s.bus.Publish(context.WithoutCancel(r.Context()), bus.Event{Type: bus.ThreadCreated, UserID: p.user.ID.String(), ThreadID: t.ID.String()})
			}
			return t, created, nil
		}
		return t, false, err
	}
	id, err := parseUUID(ref)
	if err != nil {
		return nil, false, err
	}
	t, err := s.store.GetThread(r.Context(), p.user.ID, id)
	return t, false, err
}

type threadRequest struct {
	ExternalID string          `json:"external_id"`
	Title      string          `json:"title"`
	Agent      string          `json:"agent"`
	Meta       json.RawMessage `json:"meta"`
}

// POST /api/v1/threads
func (s *Server) handleCreateThread(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	var req threadRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if len(req.Title) > 300 {
		writeError(w, errValidation("title must be at most 300 characters."))
		return
	}
	if len(req.Agent) > 120 {
		writeError(w, errValidation("agent must be at most 120 characters."))
		return
	}
	if len(req.ExternalID) > 300 {
		writeError(w, errValidation("external_id must be at most 300 characters."))
		return
	}
	meta, err := metaFrom(req.Meta)
	if err != nil {
		writeError(w, err)
		return
	}
	thread, created, err := s.store.CreateThread(r.Context(), p.user.ID, store.ThreadInput{
		ExternalID: req.ExternalID, Title: req.Title, Agent: req.Agent, Meta: meta,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
		s.bus.Publish(context.WithoutCancel(r.Context()), bus.Event{Type: bus.ThreadCreated, UserID: p.user.ID.String(), ThreadID: thread.ID.String()})
	} else {
		s.bus.Publish(context.WithoutCancel(r.Context()), bus.Event{Type: bus.ThreadUpdated, UserID: p.user.ID.String(), ThreadID: thread.ID.String()})
	}
	writeJSON(w, status, map[string]any{"thread": thread, "created": created})
}

// GET /api/v1/threads
func (s *Server) handleListThreads(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	q := r.URL.Query()
	f := store.ThreadFilter{Query: q.Get("q")}
	if v := q.Get("archived"); v == "1" || v == "true" {
		f.Archived = true
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			writeError(w, errValidation("limit must be between 1 and 200."))
			return
		}
		f.Limit = n
	}
	if v := q.Get("cursor"); v != "" {
		c, err := store.DecodeCursor(v)
		if err != nil {
			writeError(w, errValidation("cursor is invalid."))
			return
		}
		f.Cursor = &c
	}
	threads, next, err := s.store.ListThreads(r.Context(), p.user.ID, f)
	if err != nil {
		writeError(w, err)
		return
	}
	resp := map[string]any{"threads": threads}
	if next != nil {
		resp["next_cursor"] = next.Encode()
	}
	writeJSON(w, http.StatusOK, resp)
}

// GET /api/v1/threads/{thread}
func (s *Server) handleGetThread(w http.ResponseWriter, r *http.Request) {
	thread, _, err := s.resolveThread(r, false)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"thread": thread})
}

type threadPatchRequest struct {
	Title    optionalString  `json:"title"`
	Agent    optionalString  `json:"agent"`
	Archived optionalBool    `json:"archived"`
	Muted    optionalBool    `json:"muted"`
	Meta     json.RawMessage `json:"meta"`
}

// PATCH /api/v1/threads/{thread}
func (s *Server) handleUpdateThread(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	thread, _, err := s.resolveThread(r, false)
	if err != nil {
		writeError(w, err)
		return
	}
	var req threadPatchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if req.Title.Set && len(req.Title.Value) > 300 {
		writeError(w, errValidation("title must be at most 300 characters."))
		return
	}
	if req.Agent.Set && len(req.Agent.Value) > 120 {
		writeError(w, errValidation("agent must be at most 120 characters."))
		return
	}
	meta, err := metaFrom(req.Meta)
	if err != nil {
		writeError(w, err)
		return
	}
	updated, err := s.store.UpdateThread(r.Context(), p.user.ID, thread.ID, store.ThreadPatch{
		Title: req.Title.ptr(), Agent: req.Agent.ptr(), Archived: req.Archived.ptr(), Muted: req.Muted.ptr(), Meta: meta,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	s.bus.Publish(context.WithoutCancel(r.Context()), bus.Event{Type: bus.ThreadUpdated, UserID: p.user.ID.String(), ThreadID: updated.ID.String()})
	writeJSON(w, http.StatusOK, map[string]any{"thread": updated})
}

// DELETE /api/v1/threads/{thread}
func (s *Server) handleDeleteThread(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	thread, _, err := s.resolveThread(r, false)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.limitWrite(p, s.messageLimiter); err != nil {
		writeError(w, err)
		return
	}
	attachments, err := s.store.ListThreadAttachments(r.Context(), p.user.ID, thread.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.store.DeleteThread(r.Context(), p.user.ID, thread.ID); err != nil {
		writeError(w, err)
		return
	}
	// The rows are gone; the bytes follow in the background so a thread full
	// of screenshots does not hold the request open for hundreds of round
	// trips (a stray object costs storage, not correctness).
	if len(attachments) > 0 {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			s.deleteAttachmentObjects(ctx, attachments)
		}()
	}
	s.bus.Publish(context.WithoutCancel(r.Context()), bus.Event{Type: bus.ThreadDeleted, UserID: p.user.ID.String(), ThreadID: thread.ID.String()})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/v1/threads/{thread}/read
func (s *Server) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	thread, _, err := s.resolveThread(r, false)
	if err != nil {
		writeError(w, err)
		return
	}
	updated, err := s.store.MarkThreadRead(r.Context(), p.user.ID, thread.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	s.bus.Publish(context.WithoutCancel(r.Context()), bus.Event{Type: bus.ThreadUpdated, UserID: p.user.ID.String(), ThreadID: updated.ID.String()})
	writeJSON(w, http.StatusOK, map[string]any{"thread": updated})
}

// GET /api/v1/counts
func (s *Server) handleCounts(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	counts, err := s.store.GetCounts(r.Context(), p.user.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"counts": counts})
}

// parseWait reads a long-poll duration from a query parameter or body value,
// capped so a stuck client cannot hold a connection forever.
func parseWait(raw string, fallback int) (time.Duration, error) {
	if raw == "" {
		return time.Duration(fallback) * time.Second, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, errValidation("wait must be a number of seconds between 0 and 600.")
	}
	if n > 600 {
		n = 600
	}
	return time.Duration(n) * time.Second, nil
}
