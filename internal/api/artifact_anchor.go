package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/ericflo/finalechat/internal/artifact"
	"github.com/ericflo/finalechat/internal/store"
)

// Navigation is restricted to the selected artifact's own thread. No native
// log parsing or general metadata search is exposed by the iframe bridge.
func (s *Server) handleArtifactMessage(w http.ResponseWriter, r *http.Request) {
	a, rev, err := s.revisionFor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	raw := r.URL.Query().Get("anchor")
	if len(raw) == 0 || len(raw) > 2048 {
		writeError(w, errValidation("anchor must be JSON within 2 KiB"))
		return
	}
	var anchor artifact.SourceAnchor
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&anchor); err != nil {
		writeError(w, errValidation("invalid source anchor"))
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		writeError(w, errValidation("anchor must contain one JSON object"))
		return
	}
	if err := anchor.Validate(rev.Manifest); err != nil {
		writeError(w, errValidation("%v", err))
		return
	}
	var message *store.Message
	if anchor.MessageID != "" {
		id, parseErr := parseUUID(anchor.MessageID)
		if parseErr != nil {
			writeError(w, parseErr)
			return
		}
		message, err = s.store.GetMessage(r.Context(), a.UserID, id)
		if err == nil && (a.ThreadID == nil || message.ThreadID != *a.ThreadID) {
			err = store.ErrNotFound
		}
	} else {
		if a.ThreadID == nil {
			writeError(w, errNotFound)
			return
		}
		message, err = s.store.FindSourceMessage(r.Context(), a.UserID, *a.ThreadID, anchor)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message_id": message.ID, "thread_id": a.ThreadID})
}
