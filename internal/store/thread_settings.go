package store

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/ericflo/finalechat/internal/control"
)

// ConnectOwnAgent activates an installation the account itself created.
// Existing active grants are preserved unless the integration presents a new
// grant set, which replaces them: the owner is authenticated and the local
// secret proves the installation, so an integration that has learned a new
// capability can request it without a second pairing. Disconnection is never
// undone by a background retry, and another account cannot claim this
// installation.
func (s *Store) ConnectOwnAgent(ctx context.Context, owner, id uuid.UUID, hash []byte, grants []control.Grant) (*Connector, error) {
	var raw []byte
	if grants != nil {
		raw, _ = json.Marshal(grants)
	}
	return scanConnector(s.pool.QueryRow(ctx, `UPDATE connectors SET
		requested_grants=COALESCE($4::jsonb, requested_grants),
		grants=CASE WHEN state='pending' THEN COALESCE($4::jsonb, requested_grants) ELSE COALESCE($4::jsonb, grants) END,
		state='active'
		WHERE user_id=$1 AND id=$2 AND token_hash=$3 AND state IN ('pending','active')
		RETURNING `+connectorColumns, owner, id, hash, raw))
}

type ThreadSettingsLink struct {
	ID         uuid.UUID  `json:"id"`
	Label      string     `json:"label"`
	Scope      string     `json:"scope"`
	Provider   string     `json:"provider"`
	Available  bool       `json:"available"`
	ArtifactID *uuid.UUID `json:"artifact_id,omitempty"`
	RevisionID *uuid.UUID `json:"revision_id,omitempty"`
}

func (s *Store) ThreadSettings(ctx context.Context, thread *Thread) ([]ThreadSettingsLink, error) {
	out := []ThreadSettingsLink{}
	external := ""
	if thread.ExternalID != nil {
		external = *thread.ExternalID
	}
	// A conversation's own settings website (its archived editor) links a
	// resource to it; the resource's own website, published for the
	// resource itself, is the editor to open when the conversation has none.
	rows, err := s.pool.Query(ctx, `SELECT r.id,r.label,r.scope,c.provider,
		COALESCE(c.last_seen_at>now()-interval '90 seconds',false) AND
		(r.scope<>'session' OR r.snapshot->>'runtime_known'='true'),
		COALESCE(own.artifact_id,site.artifact_id),COALESCE(own.revision_id,site.revision_id)
		FROM settings_resources r JOIN connectors c ON c.id=r.connector_id AND c.user_id=r.user_id
		LEFT JOIN LATERAL (SELECT b.artifact_id,b.revision_id FROM settings_bindings b
		JOIN artifacts a ON a.id=b.artifact_id AND a.user_id=r.user_id
		WHERE b.resource_id=r.id AND a.thread_id=$2 AND a.current_revision_id=b.revision_id
		AND b.generation=r.generation ORDER BY b.updated_at DESC LIMIT 1) own ON true
		LEFT JOIN LATERAL (SELECT b.artifact_id,b.revision_id FROM settings_bindings b
		JOIN artifacts a ON a.id=b.artifact_id AND a.user_id=r.user_id
		WHERE b.resource_id=r.id AND a.resource_id=r.id AND a.current_revision_id=b.revision_id
		AND b.generation=r.generation ORDER BY b.updated_at DESC LIMIT 1) site ON true
		WHERE r.user_id=$1 AND c.state='active' AND (own.artifact_id IS NOT NULL OR
		($3<>'' AND (r.snapshot->'details'->>'thread_external_id'=$3 OR
		(r.snapshot->'details'->'thread_external_ids') ? $3)))
		ORDER BY CASE WHEN r.scope='session' THEN 1 ELSE 0 END,r.updated_at DESC,r.id LIMIT 100`, thread.UserID, thread.ID, external)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item ThreadSettingsLink
		if err := rows.Scan(&item.ID, &item.Label, &item.Scope, &item.Provider, &item.Available, &item.ArtifactID, &item.RevisionID); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
