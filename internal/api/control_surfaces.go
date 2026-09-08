package api

import (
	"net/http"

	"github.com/google/uuid"
)

func (s *Server) handleOpenSettingsSurface(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var in struct {
		RevisionID uuid.UUID `json:"revision_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	lease, err := s.store.OpenSettingsSurface(r.Context(), principalFrom(r.Context()).user.ID, id, in.RevisionID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"lease": lease})
}
