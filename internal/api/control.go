package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ericflo/finalechat/internal/auth"
	"github.com/ericflo/finalechat/internal/bus"
	"github.com/ericflo/finalechat/internal/control"
	"github.com/ericflo/finalechat/internal/store"
	"github.com/google/uuid"
)

func (s *Server) controlRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/v1/threads/{thread}/settings-resources", s.handleThreadSessionSettings)
	m.HandleFunc("POST /api/v1/connectors", s.handleCreateConnector)
	m.HandleFunc("GET /api/v1/connectors", s.sessionOnly(s.handleListConnectors))
	m.HandleFunc("GET /api/v1/connectors/{connector}", s.sessionOnly(s.handleGetConnector))
	m.HandleFunc("POST /api/v1/connectors/{connector}/approve", s.sessionOnly(s.handleApproveConnector))
	m.HandleFunc("DELETE /api/v1/connectors/{connector}", s.sessionOnly(s.handleRevokeConnector))
	m.HandleFunc("GET /api/v1/settings-resources/{resource}", s.handleGetSettingsResource)
	m.HandleFunc("GET /api/v1/settings-resources/{resource}/audit", s.handleSettingsAudit)
	m.HandleFunc("POST /api/v1/settings-resources/{resource}/commands", s.sessionOnly(s.handleCreateSettingsCommand))
	m.HandleFunc("GET /api/v1/settings-resources/{resource}/draft", s.sessionOnly(s.handleGetSettingsDraft))
	m.HandleFunc("PUT /api/v1/settings-resources/{resource}/draft", s.sessionOnly(s.handleSaveSettingsDraft))
	m.HandleFunc("DELETE /api/v1/settings-resources/{resource}/draft", s.sessionOnly(s.handleDeleteSettingsDraft))
	m.HandleFunc("GET /api/v1/artifacts/{id}/settings-binding", s.handleGetSettingsBinding)
	m.HandleFunc("POST /api/v1/artifacts/{id}/settings-surface", s.sessionOnly(s.handleOpenSettingsSurface))
	m.HandleFunc("GET /api/v1/commands/{command}", s.sessionOnly(s.handleGetSettingsCommand))
	m.HandleFunc("POST /api/v1/commands/{command}/cancel", s.sessionOnly(s.handleCancelSettingsCommand))
}

func (s *Server) handleThreadSessionSettings(w http.ResponseWriter, r *http.Request) {
	thread, _, err := s.resolveThread(r, false)
	if err != nil {
		writeError(w, err)
		return
	}
	resources, err := s.store.ThreadSessionSettings(r.Context(), thread)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"resources": resources})
}

// Connector credentials have their own routing tree. They cannot fall through
// to account, chat, artifact upload, token issuance or browser mutation routes.
func (s *Server) connectorHandler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/v1/connectors/{connector}", s.handleConnectorStatus)
	m.HandleFunc("POST /api/v1/connectors/{connector}/heartbeat", s.handleConnectorHeartbeat)
	m.HandleFunc("PUT /api/v1/connectors/{connector}/resources/{key}", s.handlePublishSettingsResource)
	m.HandleFunc("PUT /api/v1/connectors/{connector}/bindings/{id}", s.handleBindSettingsArtifact)
	m.HandleFunc("POST /api/v1/connectors/{connector}/commands/claim", s.handleClaimSettingsCommand)
	m.HandleFunc("POST /api/v1/commands/{command}/renew", s.handleRenewSettingsCommand)
	m.HandleFunc("POST /api/v1/commands/{command}/result", s.handleCompleteSettingsCommand)
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { writeError(w, errForbidden) })
	return m
}

func (s *Server) controlEvent(ctx context.Context, userID, connectorID, resourceID, commandID uuid.UUID, typ string) {
	e := bus.Event{Type: typ, UserID: userID.String(), ConnectorID: connectorID.String()}
	if resourceID != uuid.Nil {
		e.ResourceID = resourceID.String()
	}
	if commandID != uuid.Nil {
		e.CommandID = commandID.String()
	}
	s.bus.Publish(ctx, e)
}
func (s *Server) handleCreateConnector(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r.Context())
	if err := s.limitWrite(p, s.questionLimiter); err != nil {
		writeError(w, err)
		return
	}
	var in struct {
		Name     string          `json:"name"`
		Provider string          `json:"provider"`
		Grants   []control.Grant `json:"requested_grants"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 200 || in.Provider == "" || len(in.Provider) > 120 || len(in.Grants) == 0 || len(in.Grants) > 50 {
		writeError(w, errValidation("name, provider and 1–50 requested grants are required"))
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
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		writeError(w, err)
		return
	}
	secret := "fcc_" + base64.RawURLEncoding.EncodeToString(random[:])
	c, err := s.store.CreateConnector(r.Context(), p.user.ID, in.Name, in.Provider, auth.HashToken(secret), in.Grants)
	if err != nil {
		writeError(w, err)
		return
	}
	s.controlEvent(r.Context(), p.user.ID, c.ID, uuid.Nil, uuid.Nil, bus.ConnectorUpdated)
	writeJSON(w, 201, map[string]any{"connector": c, "secret": secret, "approval_url": s.cfg.BaseURL + "/settings?connector=" + c.ID.String()})
}
func (s *Server) handleListConnectors(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListConnectors(r.Context(), principalFrom(r.Context()).user.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"connectors": items})
}
func (s *Server) browserConnector(r *http.Request) (*store.Connector, error) {
	id, err := parseUUID(r.PathValue("connector"))
	if err != nil {
		return nil, err
	}
	return s.store.GetConnector(r.Context(), principalFrom(r.Context()).user.ID, id)
}
func (s *Server) handleGetConnector(w http.ResponseWriter, r *http.Request) {
	c, err := s.browserConnector(r)
	if err != nil {
		writeError(w, err)
		return
	}
	resources, err := s.store.ListSettingsResources(r.Context(), c.UserID, c.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"connector": c, "online": c.Online(), "resources": resources})
}
func (s *Server) handleApproveConnector(w http.ResponseWriter, r *http.Request) {
	c, err := s.browserConnector(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var in struct {
		Grants []control.Grant `json:"grants"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	c, err = s.store.ApproveConnector(r.Context(), c.UserID, c.ID, in.Grants)
	if err != nil {
		writeError(w, err)
		return
	}
	s.controlEvent(r.Context(), c.UserID, c.ID, uuid.Nil, uuid.Nil, bus.ConnectorUpdated)
	writeJSON(w, 200, map[string]any{"connector": c})
}
func (s *Server) handleRevokeConnector(w http.ResponseWriter, r *http.Request) {
	c, err := s.browserConnector(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.store.RevokeConnector(r.Context(), c.UserID, c.ID); err != nil {
		writeError(w, err)
		return
	}
	s.controlEvent(r.Context(), c.UserID, c.ID, uuid.Nil, uuid.Nil, bus.ConnectorUpdated)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) ownConnector(r *http.Request, active bool) (*store.Connector, error) {
	c := principalFrom(r.Context()).connector
	if c == nil {
		return nil, errForbidden
	}
	if v := r.PathValue("connector"); v != "" && v != c.ID.String() {
		return nil, errNotFound
	}
	if active && c.State != "active" {
		return nil, errForbidden
	}
	return c, nil
}
func (s *Server) handleConnectorStatus(w http.ResponseWriter, r *http.Request) {
	c, err := s.ownConnector(r, false)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"connector": c, "online": c.Online()})
}
func validInstance(s string) bool {
	return len(s) >= 16 && len(s) <= 200 && !strings.ContainsAny(s, "\r\n\x00")
}
func (s *Server) handleConnectorHeartbeat(w http.ResponseWriter, r *http.Request) {
	c, err := s.ownConnector(r, true)
	if err != nil {
		writeError(w, err)
		return
	}
	var in struct {
		Instance string `json:"instance"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	if !validInstance(in.Instance) {
		writeError(w, errValidation("instance must be a stable random process identity"))
		return
	}
	c, err = s.store.HeartbeatConnector(r.Context(), c.UserID, c.ID, in.Instance)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			err = errConflict
		}
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"connector": c, "online": true})
}
func (s *Server) handlePublishSettingsResource(w http.ResponseWriter, r *http.Request) {
	c, err := s.ownConnector(r, true)
	if err != nil {
		writeError(w, err)
		return
	}
	var in struct {
		Instance   string             `json:"instance"`
		Generation string             `json:"generation"`
		Descriptor control.Descriptor `json:"descriptor"`
		Snapshot   control.Snapshot   `json:"snapshot"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	g, ok := c.Grant(r.PathValue("key"))
	if !ok {
		writeError(w, errForbidden)
		return
	}
	if !validInstance(in.Instance) || len(in.Generation) > 200 {
		writeError(w, errValidation("invalid instance or generation"))
		return
	}
	if err := in.Descriptor.Validate(g); err != nil {
		writeError(w, errValidation("%v", err))
		return
	}
	if err := in.Snapshot.Validate(); err != nil {
		writeError(w, errValidation("%v", err))
		return
	}
	resource, err := s.store.PublishSettingsResource(r.Context(), c.UserID, c.ID, in.Instance, g.Key, in.Generation, in.Descriptor, in.Snapshot)
	if err != nil {
		writeError(w, err)
		return
	}
	s.controlEvent(r.Context(), c.UserID, c.ID, resource.ID, uuid.Nil, bus.ResourceUpdated)
	writeJSON(w, 200, map[string]any{"resource": resource})
}
func (s *Server) handleBindSettingsArtifact(w http.ResponseWriter, r *http.Request) {
	c, err := s.ownConnector(r, true)
	if err != nil {
		writeError(w, err)
		return
	}
	artifactID, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var in struct {
		ResourceID uuid.UUID `json:"resource_id"`
		RevisionID uuid.UUID `json:"revision_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	if err := s.store.BindSettingsArtifact(r.Context(), c.UserID, c.ID, in.ResourceID, artifactID, in.RevisionID); err != nil {
		writeError(w, err)
		return
	}
	s.controlEvent(r.Context(), c.UserID, c.ID, in.ResourceID, uuid.Nil, bus.ResourceUpdated)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) settingsResourceFor(r *http.Request) (*store.SettingsResource, *store.Connector, error) {
	id, err := parseUUID(r.PathValue("resource"))
	if err != nil {
		return nil, nil, err
	}
	resource, err := s.store.GetSettingsResource(r.Context(), principalFrom(r.Context()).user.ID, id)
	if err != nil {
		return nil, nil, err
	}
	c, err := s.store.GetConnector(r.Context(), resource.UserID, resource.ConnectorID)
	return resource, c, err
}
func (s *Server) handleGetSettingsResource(w http.ResponseWriter, r *http.Request) {
	resource, c, err := s.settingsResourceFor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"resource": resource, "connector": c, "online": c.Online()})
}
func (s *Server) handleGetSettingsBinding(w http.ResponseWriter, r *http.Request) {
	a, err := s.artifactFor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	binding, err := s.store.GetSettingsBinding(r.Context(), a.UserID, a.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"binding": binding})
}
func (s *Server) handleSettingsAudit(w http.ResponseWriter, r *http.Request) {
	resource, _, err := s.settingsResourceFor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var before *uuid.UUID
	if v := r.URL.Query().Get("before"); v != "" {
		id, err := parseUUID(v)
		if err != nil {
			writeError(w, err)
			return
		}
		before = &id
	}
	items, err := s.store.SettingsAudit(r.Context(), resource.UserID, resource.ID, before)
	if err != nil {
		writeError(w, err)
		return
	}
	out := map[string]any{"audit": items}
	if len(items) == 100 {
		out["next_before"] = items[99]["id"]
	}
	writeJSON(w, 200, out)
}
func (s *Server) handleCreateSettingsCommand(w http.ResponseWriter, r *http.Request) {
	resource, _, err := s.settingsResourceFor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var in struct {
		ClientKey    string           `json:"client_key"`
		Proposal     control.Proposal `json:"proposal"`
		ArtifactID   *uuid.UUID       `json:"artifact_id"`
		RevisionID   *uuid.UUID       `json:"revision_id"`
		SurfaceLease *uuid.UUID       `json:"surface_lease_id"`
		TTL          int              `json:"ttl_seconds"`
		AllowOffline bool             `json:"send_when_connected"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	if in.ClientKey == "" || len(in.ClientKey) > 200 || ((in.ArtifactID == nil) != (in.RevisionID == nil)) || in.SurfaceLease != nil && in.ArtifactID == nil {
		writeError(w, errValidation("client_key and paired surface identifiers are required"))
		return
	}
	if in.TTL == 0 {
		in.TTL = 300
	}
	if in.TTL < 15 || in.TTL > 86400 {
		writeError(w, errValidation("ttl_seconds must be 15 to 86400"))
		return
	}
	if resource.Scope == "session" && (in.TTL > 300 || in.AllowOffline) {
		writeError(w, errValidation("session commands expire within 300 seconds and cannot wait for a later connection"))
		return
	}
	q, created, err := s.store.CreateSettingsCommand(r.Context(), resource.UserID, resource.ID, in.ArtifactID, in.RevisionID, in.SurfaceLease, in.ClientKey, in.Proposal, time.Now().Add(time.Duration(in.TTL)*time.Second), in.AllowOffline)
	if err != nil {
		writeError(w, err)
		return
	}
	if created {
		s.controlEvent(r.Context(), q.UserID, q.ConnectorID, q.ResourceID, q.ID, bus.CommandUpdated)
	}
	q.ForBrowser()
	status := 200
	if created {
		status = 202
	}
	writeJSON(w, status, map[string]any{"command": q, "created": created})
}
func (s *Server) handleGetSettingsCommand(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("command"))
	if err != nil {
		writeError(w, err)
		return
	}
	q, err := s.store.GetSettingsCommand(r.Context(), principalFrom(r.Context()).user.ID, id)
	if err != nil {
		writeError(w, err)
		return
	}
	q.ForBrowser()
	writeJSON(w, 200, map[string]any{"command": q})
}
func (s *Server) handleCancelSettingsCommand(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("command"))
	if err != nil {
		writeError(w, err)
		return
	}
	q, err := s.store.CancelSettingsCommand(r.Context(), principalFrom(r.Context()).user.ID, id)
	if err != nil {
		writeError(w, err)
		return
	}
	s.controlEvent(r.Context(), q.UserID, q.ConnectorID, q.ResourceID, q.ID, bus.CommandUpdated)
	q.ForBrowser()
	writeJSON(w, 200, map[string]any{"command": q})
}

func (s *Server) handleClaimSettingsCommand(w http.ResponseWriter, r *http.Request) {
	c, err := s.ownConnector(r, true)
	if err != nil {
		writeError(w, err)
		return
	}
	var in struct {
		Instance string `json:"instance"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	if !validInstance(in.Instance) {
		writeError(w, errValidation("invalid instance"))
		return
	}
	wait := 0
	if v := r.URL.Query().Get("wait"); v != "" {
		wait, err = strconv.Atoi(v)
		if err != nil || wait < 0 || wait > 25 {
			writeError(w, errValidation("wait must be 0 to 25 seconds"))
			return
		}
	}
	// Subscribe before the first query. A periodic query also recovers a
	// dropped NOTIFY and makes an expired claim promptly available.
	events, cancel := s.bus.Subscribe(c.UserID.String())
	defer cancel()
	deadline := time.NewTimer(time.Duration(wait) * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(2 * time.Second)
	defer poll.Stop()
	for {
		q, err := s.store.ClaimSettingsCommand(r.Context(), c.UserID, c.ID, in.Instance)
		if err != nil {
			writeError(w, err)
			return
		}
		if q != nil {
			resource, err := s.store.GetSettingsResource(r.Context(), c.UserID, q.ResourceID)
			if err != nil {
				writeError(w, err)
				return
			}
			s.controlEvent(r.Context(), q.UserID, q.ConnectorID, q.ResourceID, q.ID, bus.CommandUpdated)
			writeJSON(w, 200, map[string]any{"command": q, "resource": resource, "reconcile_only": q.Attempts > 1})
			return
		}
		if wait == 0 {
			writeJSON(w, 200, map[string]any{"command": nil})
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-s.shutdown:
			writeJSON(w, 200, map[string]any{"command": nil, "reconnect": true})
			return
		case <-deadline.C:
			writeJSON(w, 200, map[string]any{"command": nil})
			return
		case <-poll.C:
		case <-events:
		}
	}
}

type claimRequest struct {
	Claim    uuid.UUID  `json:"claim_token"`
	Instance string     `json:"instance"`
	Status   string     `json:"status"`
	Result   store.JSON `json:"result"`
}

func (s *Server) handleRenewSettingsCommand(w http.ResponseWriter, r *http.Request) {
	c, err := s.ownConnector(r, true)
	if err != nil {
		writeError(w, err)
		return
	}
	id, err := parseUUID(r.PathValue("command"))
	if err != nil {
		writeError(w, err)
		return
	}
	var in claimRequest
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	q, err := s.store.RenewSettingsCommand(r.Context(), c.UserID, c.ID, id, in.Claim, in.Instance)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"command": q})
}
func (s *Server) handleCompleteSettingsCommand(w http.ResponseWriter, r *http.Request) {
	c, err := s.ownConnector(r, true)
	if err != nil {
		writeError(w, err)
		return
	}
	id, err := parseUUID(r.PathValue("command"))
	if err != nil {
		writeError(w, err)
		return
	}
	var in claimRequest
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	raw, err := json.Marshal(in.Result)
	if err != nil || in.Result == nil || len(raw) > control.MaxCommandBytes {
		writeError(w, errValidation("result must be a bounded object"))
		return
	}
	q, err := s.store.CompleteSettingsCommand(r.Context(), c.UserID, c.ID, id, in.Claim, in.Instance, in.Status, in.Result)
	if err != nil {
		writeError(w, err)
		return
	}
	s.controlEvent(r.Context(), q.UserID, q.ConnectorID, q.ResourceID, q.ID, bus.CommandUpdated)
	q.ForBrowser()
	writeJSON(w, 200, map[string]any{"command": q})
}

func (s *Server) handleSaveSettingsDraft(w http.ResponseWriter, r *http.Request) {
	resource, c, err := s.settingsResourceFor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if resource.Scope == "session" {
		writeError(w, errValidation("runtime settings cannot be saved as offline drafts"))
		return
	}
	var in control.Proposal
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	g, ok := c.Grant(resource.Key)
	if !ok {
		writeError(w, errForbidden)
		return
	}
	if err := in.Validate(resource.Descriptor, g); err != nil {
		writeError(w, errValidation("%v", err))
		return
	}
	if err := s.store.SaveSettingsDraft(r.Context(), resource.UserID, resource.ID, in); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
func (s *Server) handleGetSettingsDraft(w http.ResponseWriter, r *http.Request) {
	resource, _, err := s.settingsResourceFor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	p, err := s.store.GetSettingsDraft(r.Context(), resource.UserID, resource.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, 200, map[string]any{"proposal": nil})
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"proposal": p})
}
func (s *Server) handleDeleteSettingsDraft(w http.ResponseWriter, r *http.Request) {
	resource, _, err := s.settingsResourceFor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.store.DeleteSettingsDraft(r.Context(), resource.UserID, resource.ID); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
