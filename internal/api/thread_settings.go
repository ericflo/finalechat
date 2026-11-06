package api

import (
	"net/http"
	"strings"

	"github.com/ericflo/finalechat/internal/auth"
	"github.com/ericflo/finalechat/internal/bus"
	"github.com/ericflo/finalechat/internal/control"
	"github.com/google/uuid"
)

// An account-authenticated integration already represents its owner's local
// agent. Registering its declared controls does not require a second browser
// ceremony. The separate scoped credential still cannot submit user commands.
func (s *Server) handleConnectOwnAgent(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("connector"))
	if err != nil {
		writeError(w, err)
		return
	}
	var in struct {
		Secret string          `json:"secret"`
		Grants []control.Grant `json:"requested_grants"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	if !strings.HasPrefix(in.Secret, "fcc_") || len(in.Secret) > 200 {
		writeError(w, errForbidden)
		return
	}
	if in.Grants != nil {
		if len(in.Grants) == 0 || len(in.Grants) > 50 {
			writeError(w, errValidation("requested_grants must list 1–50 grants"))
			return
		}
		seen := map[string]bool{}
		for _, g := range in.Grants {
			if err := g.Validate(); err != nil {
				writeError(w, errValidation("%v", err))
				return
			}
			if seen[g.Key] {
				writeError(w, errValidation("duplicate resource key"))
				return
			}
			seen[g.Key] = true
		}
	}
	p := principalFrom(r.Context())
	c, err := s.store.ConnectOwnAgent(r.Context(), p.user.ID, id, auth.HashToken(in.Secret), in.Grants)
	if err != nil {
		writeError(w, err)
		return
	}
	s.controlEvent(r.Context(), p.user.ID, id, uuid.Nil, uuid.Nil, bus.ConnectorUpdated)
	writeJSON(w, 200, map[string]any{"connector": c})
}

func (s *Server) handleThreadSettings(w http.ResponseWriter, r *http.Request) {
	t, _, err := s.resolveThread(r, false)
	if err != nil {
		writeError(w, err)
		return
	}
	items, err := s.store.ThreadSettings(r.Context(), t)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"resources": items})
}
