package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/ericflo/finalechat/internal/bus"
)

// GET /api/v1/events
//
// Server-sent events. Each event carries the full current object(s) so a
// client can apply it without a follow-up request:
//
//	event: message.created  data: {"message": {...}, "thread": {...}}
//	event: message.deleted  data: {"message_id": "...", "thread": {...}}
//	event: question.*       data: {"question": {...}, "thread": {...}}
//	event: thread.*         data: {"thread": {...}} or {"thread_id": "..."} when deleted
//	event: thread.activity  data: {"thread_id": "...", "activity": {...} | null}
//	event: ping             data: {"at": "..."} every 20 seconds
//	event: ready            data: {"counts": {...}}
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, errInternal)
		return
	}
	events, cancel := s.bus.Subscribe(p.user.ID.String())
	defer cancel()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	send := func(name string, payload any) bool {
		data, err := json.Marshal(payload)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	counts, _ := s.store.GetCounts(r.Context(), p.user.ID)
	if !send("ready", map[string]any{"counts": counts, "at": time.Now().UTC()}) {
		return
	}

	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.shutdown:
			// Ask clients to reconnect against the next replica.
			_, _ = fmt.Fprint(w, "event: reconnect\ndata: {}\n\n")
			flusher.Flush()
			return
		case <-heartbeat.C:
			// A named event (not a comment) so the client can watch for liveness.
			if !send("ping", map[string]any{"at": time.Now().UTC()}) {
				return
			}
		case ev, ok := <-events:
			if !ok {
				return
			}
			payload, err := s.expandEvent(r, ev)
			if err != nil {
				s.log.Warn("expand event", "type", ev.Type, "err", err)
				continue
			}
			if payload == nil {
				continue
			}
			if !send(ev.Type, payload) {
				return
			}
		}
	}
}

// expandEvent loads the objects referenced by an event.
func (s *Server) expandEvent(r *http.Request, ev bus.Event) (map[string]any, error) {
	p := principalFrom(r.Context())
	ctx := r.Context()
	out := map[string]any{"at": ev.At}
	if ev.Type == bus.ConnectorUpdated || ev.Type == bus.ResourceUpdated || ev.Type == bus.CommandUpdated {
		out["connector_id"] = ev.ConnectorID
		out["resource_id"] = ev.ResourceID
		out["command_id"] = ev.CommandID
		return out, nil
	}
	if ev.Type == bus.SettingsUpdated {
		user, err := s.store.GetUser(ctx, p.user.ID)
		if err != nil {
			return nil, err
		}
		out["settings"] = user.Settings
		return out, nil
	}
	if ev.Type == bus.ThreadActivity {
		// Frequent and self-contained: the status travels in the event, so no
		// database work per subscriber.
		out["thread_id"] = ev.ThreadID
		if len(ev.Activity) > 0 {
			out["activity"] = ev.Activity
		} else {
			out["activity"] = nil
		}
		return out, nil
	}
	threadID, err := uuid.Parse(ev.ThreadID)
	if err != nil {
		return nil, err
	}
	if ev.Type == bus.ThreadDeleted {
		out["thread_id"] = ev.ThreadID
		counts, _ := s.store.GetCounts(ctx, p.user.ID)
		out["counts"] = counts
		return out, nil
	}
	thread, err := s.store.GetThread(ctx, p.user.ID, threadID)
	if err != nil {
		return nil, err
	}
	out["thread"] = thread
	switch ev.Type {
	case bus.ArtifactUpdated, bus.ArtifactDeleted:
		out["artifact_id"] = ev.ArtifactID
	case bus.MessageDeleted:
		out["message_id"] = ev.MessageID
	case bus.MessageCreated:
		id, err := uuid.Parse(ev.MessageID)
		if err != nil {
			return nil, err
		}
		msg, err := s.store.GetMessage(ctx, p.user.ID, id)
		if err != nil {
			return nil, err
		}
		decorate(msg)
		out["message"] = msg
	case bus.QuestionCreated, bus.QuestionAnswered, bus.QuestionCancelled, bus.QuestionExpired, bus.QuestionDismissed:
		id, err := uuid.Parse(ev.QuestionID)
		if err != nil {
			return nil, err
		}
		q, err := s.store.GetQuestion(ctx, p.user.ID, id)
		if err != nil {
			return nil, err
		}
		out["question"] = q
	}
	counts, _ := s.store.GetCounts(ctx, p.user.ID)
	out["counts"] = counts
	return out, nil
}
