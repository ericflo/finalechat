package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SettingsSurfaceLease belongs to the trusted browser host. Its identifier is
// never given to the artifact script and does not replace browser authentication.
type SettingsSurfaceLease struct {
	ID         uuid.UUID `json:"id"`
	ResourceID uuid.UUID `json:"resource_id"`
	Generation string    `json:"generation"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func (s *Store) OpenSettingsSurface(ctx context.Context, userID, artifactID, revisionID uuid.UUID) (*SettingsSurfaceLease, error) {
	var out SettingsSurfaceLease
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 7318))", userID.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM settings_surface_leases WHERE user_id=$1 AND expires_at<=now()", userID); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM settings_surface_leases WHERE user_id=$1", userID).Scan(&count); err != nil {
			return err
		}
		if count >= 32 {
			return ErrInvalidState
		}
		// The one INSERT observes a current, bound revision and active resource.
		// Historical documents cannot acquire an editing lease. After issuance,
		// commands still require the same owner, binding, generation and fresh
		// settings version, but publication alone need not interrupt the editor.
		err := tx.QueryRow(ctx, `INSERT INTO settings_surface_leases(id,user_id,resource_id,artifact_id,revision_id,generation)
		SELECT $4,$1,r.id,a.id,v.id,r.generation FROM artifacts a
		JOIN artifact_revisions v ON v.id=a.current_revision_id AND v.artifact_id=a.id
		JOIN settings_bindings b ON b.artifact_id=a.id AND b.revision_id=v.id
		JOIN settings_resources r ON r.id=b.resource_id AND r.user_id=a.user_id AND r.generation=b.generation
		JOIN connectors c ON c.id=r.connector_id AND c.state='active'
		WHERE a.id=$2 AND a.user_id=$1 AND v.id=$3 AND coalesce(v.manifest->>'settings_entrypoint','')<>''
		RETURNING id,resource_id,generation,expires_at`, userID, artifactID, revisionID, uuid.New()).Scan(&out.ID, &out.ResourceID, &out.Generation, &out.ExpiresAt)
		if err == pgx.ErrNoRows {
			return ErrConflict
		}
		return err
	})
	return &out, err
}
