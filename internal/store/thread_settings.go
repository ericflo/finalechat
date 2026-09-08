package store

import (
	"context"

	"github.com/google/uuid"
)

func (s *Store) ConnectOwnAgent(ctx context.Context, owner, id uuid.UUID, hash []byte) (*Connector, error) {
	// Existing active grants are preserved. Disconnection is never undone by
	// a background retry, and another account cannot claim this installation.
	return scanConnector(s.pool.QueryRow(ctx, `UPDATE connectors SET
		grants=CASE WHEN state='pending' THEN requested_grants ELSE grants END,state='active'
		WHERE user_id=$1 AND id=$2 AND token_hash=$3 AND state IN ('pending','active')
		RETURNING `+connectorColumns, owner, id, hash))
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
	rows, err := s.pool.Query(ctx, `SELECT r.id,r.label,r.scope,c.provider,
		COALESCE(c.last_seen_at>now()-interval '90 seconds',false) AND
		(r.scope<>'session' OR r.snapshot->>'runtime_known'='true'),website.artifact_id,website.revision_id
		FROM settings_resources r JOIN connectors c ON c.id=r.connector_id AND c.user_id=r.user_id
		LEFT JOIN LATERAL (SELECT b.artifact_id,b.revision_id FROM settings_bindings b
		JOIN artifacts a ON a.id=b.artifact_id AND a.user_id=r.user_id
		WHERE b.resource_id=r.id AND a.thread_id=$2 AND a.current_revision_id=b.revision_id
		AND b.generation=r.generation ORDER BY b.updated_at DESC LIMIT 1) website ON true
		WHERE r.user_id=$1 AND c.state='active' AND (website.artifact_id IS NOT NULL OR
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
