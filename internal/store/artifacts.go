package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/ericflo/finalechat/internal/artifact"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrQuota = errors.New("artifact storage quota exceeded")
var ErrUploadBusy = errors.New("artifact blob upload in progress")

type Artifact struct {
	ID                uuid.UUID  `json:"id"`
	UserID            uuid.UUID  `json:"-"`
	ThreadID          uuid.UUID  `json:"thread_id"`
	Key               string     `json:"key"`
	Title             string     `json:"title"`
	CurrentRevisionID *uuid.UUID `json:"current_revision_id"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

const artifactColumns = "id, user_id, thread_id, external_key, title, current_revision_id, created_at, updated_at"

func scanArtifact(row pgx.Row) (*Artifact, error) {
	var a Artifact
	err := row.Scan(&a.ID, &a.UserID, &a.ThreadID, &a.Key, &a.Title, &a.CurrentRevisionID, &a.CreatedAt, &a.UpdatedAt)
	return &a, translate(err)
}

func (s *Store) UpsertArtifact(ctx context.Context, userID, threadID uuid.UUID, key, title string) (*Artifact, error) {
	return scanArtifact(s.pool.QueryRow(ctx, `INSERT INTO artifacts(id,user_id,thread_id,external_key,title)
		SELECT $1,$2,$3,$4,$5 WHERE EXISTS(SELECT 1 FROM threads WHERE id=$3 AND user_id=$2)
		ON CONFLICT(thread_id,external_key) DO UPDATE SET title=EXCLUDED.title,updated_at=now()
		WHERE artifacts.user_id=EXCLUDED.user_id RETURNING `+artifactColumns, NewID(), userID, threadID, key, title))
}

func (s *Store) GetArtifact(ctx context.Context, userID, id uuid.UUID) (*Artifact, error) {
	return scanArtifact(s.pool.QueryRow(ctx, "SELECT "+artifactColumns+" FROM artifacts WHERE user_id=$1 AND id=$2", userID, id))
}

func (s *Store) ListArtifacts(ctx context.Context, userID, threadID uuid.UUID) ([]*Artifact, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+artifactColumns+" FROM artifacts WHERE user_id=$1 AND thread_id=$2 ORDER BY created_at,id", userID, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Artifact{}
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

type ArtifactRevision struct {
	ID                 uuid.UUID         `json:"id"`
	ArtifactID         uuid.UUID         `json:"artifact_id"`
	PreviousRevisionID *uuid.UUID        `json:"previous_revision_id"`
	Manifest           artifact.Manifest `json:"manifest"`
	ManifestSHA256     string            `json:"manifest_sha256"`
	ClientKey          string            `json:"client_key"`
	CreatedAt          time.Time         `json:"created_at"`
}

func scanRevision(row pgx.Row) (*ArtifactRevision, error) {
	var r ArtifactRevision
	var raw []byte
	if err := row.Scan(&r.ID, &r.ArtifactID, &r.PreviousRevisionID, &raw, &r.ManifestSHA256, &r.ClientKey, &r.CreatedAt); err != nil {
		return nil, translate(err)
	}
	if err := json.Unmarshal(raw, &r.Manifest); err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) GetArtifactRevision(ctx context.Context, userID, artifactID, revisionID uuid.UUID) (*ArtifactRevision, error) {
	return scanRevision(s.pool.QueryRow(ctx, `SELECT r.id,r.artifact_id,r.previous_revision_id,r.manifest,r.manifest_sha256,r.client_key,r.created_at
		FROM artifact_revisions r JOIN artifacts a ON a.id=r.artifact_id WHERE a.user_id=$1 AND a.id=$2 AND r.id=$3`, userID, artifactID, revisionID))
}

type ArtifactRevisionInfo struct {
	ID             uuid.UUID         `json:"id"`
	ManifestSHA256 string            `json:"manifest_sha256"`
	CreatedAt      time.Time         `json:"created_at"`
	CapturedAt     time.Time         `json:"captured_at"`
	Producer       artifact.Producer `json:"producer"`
	Dataset        JSON              `json:"dataset"`
}

func (s *Store) ListArtifactRevisions(ctx context.Context, userID, artifactID uuid.UUID, before *uuid.UUID) ([]ArtifactRevisionInfo, error) {
	rows, err := s.pool.Query(ctx, `SELECT r.id,r.manifest_sha256,r.created_at,r.manifest->>'captured_at',r.manifest->'producer',r.manifest->'dataset'
		FROM artifact_revisions r JOIN artifacts a ON a.id=r.artifact_id WHERE a.user_id=$1 AND a.id=$2 AND ($3::uuid IS NULL OR r.id<$3) ORDER BY r.id DESC LIMIT 50`, userID, artifactID, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ArtifactRevisionInfo{}
	for rows.Next() {
		var r ArtifactRevisionInfo
		var captured string
		var producer, dataset []byte
		if err := rows.Scan(&r.ID, &r.ManifestSHA256, &r.CreatedAt, &captured, &producer, &dataset); err != nil {
			return nil, err
		}
		r.CapturedAt, _ = time.Parse(time.RFC3339Nano, captured)
		_ = json.Unmarshal(producer, &r.Producer)
		scanJSON(dataset, &r.Dataset)
		out = append(out, r)
	}
	return out, rows.Err()
}

// CommitArtifactRevision serializes publishers and validates every referenced
// blob under a row lock. An idempotent retry is checked before its old parent.
func (s *Store) CommitArtifactRevision(ctx context.Context, userID, artifactID uuid.UUID, previous *uuid.UUID, key string, m artifact.Manifest) (*ArtifactRevision, bool, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, false, err
	}
	digest := artifact.Digest(raw)
	var result *ArtifactRevision
	created := false
	err = s.withTx(ctx, func(tx pgx.Tx) error {
		a, err := scanArtifact(tx.QueryRow(ctx, "SELECT "+artifactColumns+" FROM artifacts WHERE id=$1 AND user_id=$2 FOR UPDATE", artifactID, userID))
		if err != nil {
			return err
		}
		old, err := scanRevision(tx.QueryRow(ctx, `SELECT id,artifact_id,previous_revision_id,manifest,manifest_sha256,client_key,created_at FROM artifact_revisions WHERE artifact_id=$1 AND client_key=$2`, artifactID, key))
		if err == nil {
			if old.ManifestSHA256 != digest {
				return ErrConflict
			}
			result = old
			return nil
		}
		if err != ErrNotFound {
			return err
		}
		if (a.CurrentRevisionID == nil) != (previous == nil) || (previous != nil && *previous != *a.CurrentRevisionID) {
			return ErrConflict
		}
		blobs := m.Blobs()
		hashes := make([]string, 0, len(blobs))
		for h := range blobs {
			hashes = append(hashes, h)
		}
		sort.Strings(hashes)
		for _, h := range hashes {
			var size int64
			var state string
			if err := tx.QueryRow(ctx, `SELECT size,state FROM artifact_blobs WHERE user_id=$1 AND sha256=$2 FOR KEY SHARE`, userID, h).Scan(&size, &state); err != nil {
				return translate(err)
			}
			if state != "ready" || size != blobs[h] {
				return ErrInvalidState
			}
		}
		id := NewID()
		result, err = scanRevision(tx.QueryRow(ctx, `INSERT INTO artifact_revisions(id,artifact_id,previous_revision_id,manifest,manifest_sha256,client_key)
			VALUES($1,$2,$3,$4,$5,$6) RETURNING id,artifact_id,previous_revision_id,manifest,manifest_sha256,client_key,created_at`, id, artifactID, previous, raw, digest, key))
		if err != nil {
			return err
		}
		for _, h := range hashes {
			if _, err := tx.Exec(ctx, "INSERT INTO artifact_revision_blobs(revision_id,user_id,sha256) VALUES($1,$2,$3)", id, userID, h); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, "UPDATE artifacts SET current_revision_id=$2,updated_at=now() WHERE id=$1", artifactID, id)
		created = err == nil
		return err
	})
	return result, created, err
}

type ArtifactBlob struct {
	SHA256      string
	Size        int64
	ObjectKey   string
	ObjectID    string
	State       string
	Reservation uuid.UUID
}

func scanArtifactBlob(row pgx.Row) (*ArtifactBlob, error) {
	var b ArtifactBlob
	err := row.Scan(&b.SHA256, &b.Size, &b.ObjectKey, &b.ObjectID, &b.State, &b.Reservation)
	return &b, translate(err)
}

func (s *Store) GetArtifactBlob(ctx context.Context, userID uuid.UUID, hash string) (*ArtifactBlob, error) {
	return scanArtifactBlob(s.pool.QueryRow(ctx, `SELECT sha256,size,object_key,object_id,state,reservation FROM artifact_blobs WHERE user_id=$1 AND sha256=$2 AND state='ready'`, userID, hash))
}

func (s *Store) MissingArtifactBlobs(ctx context.Context, userID uuid.UUID, hashes []string) ([]string, error) {
	rows, err := s.pool.Query(ctx, "SELECT sha256 FROM artifact_blobs WHERE user_id=$1 AND sha256=ANY($2) AND state='ready'", userID, hashes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		seen[h] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []string{}
	for _, h := range hashes {
		if !seen[h] {
			out = append(out, h)
			seen[h] = true
		}
	}
	return out, nil
}

// ReserveArtifactBlob prevents concurrent duplicate B2 versions and serializes
// quota accounting per user. The caller uploads outside the transaction.
func (s *Store) ReserveArtifactBlob(ctx context.Context, userID, artifactID uuid.UUID, hash string, size int64) (*ArtifactBlob, error) {
	var out *ArtifactBlob
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 7317))", userID.String()); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM artifacts WHERE id=$1 AND user_id=$2)", artifactID, userID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		b, err := scanArtifactBlob(tx.QueryRow(ctx, "SELECT sha256,size,object_key,object_id,state,reservation FROM artifact_blobs WHERE user_id=$1 AND sha256=$2 FOR UPDATE", userID, hash))
		if err == nil {
			if b.Size != size {
				return ErrConflict
			}
			if b.State == "ready" {
				out = b
				return nil
			}
			var stale bool
			if err := tx.QueryRow(ctx, "SELECT updated_at<now()-interval '15 minutes' FROM artifact_blobs WHERE user_id=$1 AND sha256=$2", userID, hash).Scan(&stale); err != nil {
				return err
			}
			if !stale {
				return ErrUploadBusy
			}
			if _, err := tx.Exec(ctx, "DELETE FROM artifact_blobs WHERE user_id=$1 AND sha256=$2", userID, hash); err != nil {
				return err
			}
		} else if err != ErrNotFound {
			return err
		}
		var used int64
		if err := tx.QueryRow(ctx, "SELECT COALESCE(sum(size),0)::bigint FROM artifact_blobs WHERE user_id=$1", userID).Scan(&used); err != nil {
			return err
		}
		if used+size > artifact.MaxAccountBytes {
			return ErrQuota
		}
		id := NewID()
		objectKey := fmt.Sprintf("artifacts/%s/%s/%s", userID, hash, id)
		out, err = scanArtifactBlob(tx.QueryRow(ctx, `INSERT INTO artifact_blobs(user_id,sha256,size,object_key,state,reservation) VALUES($1,$2,$3,$4,'uploading',$5)
			RETURNING sha256,size,object_key,object_id,state,reservation`, userID, hash, size, objectKey, id))
		return err
	})
	return out, err
}

func (s *Store) CompleteArtifactBlob(ctx context.Context, userID uuid.UUID, b *ArtifactBlob, objectID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE artifact_blobs SET state='ready',object_id=$4,updated_at=now() WHERE user_id=$1 AND sha256=$2 AND reservation=$3 AND state='uploading'`, userID, b.SHA256, b.Reservation, objectID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) AbandonArtifactBlob(ctx context.Context, userID uuid.UUID, b *ArtifactBlob) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM artifact_blobs WHERE user_id=$1 AND sha256=$2 AND reservation=$3 AND state='uploading'", userID, b.SHA256, b.Reservation)
	return err
}

func (s *Store) DeleteArtifact(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM artifacts WHERE user_id=$1 AND id=$2", userID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) QueueArtifactObjectDeletion(ctx context.Context, key, id string) error {
	_, err := s.pool.Exec(ctx, "INSERT INTO artifact_object_deletions(object_key,object_id) VALUES($1,$2) ON CONFLICT DO NOTHING", key, id)
	return err
}

// PruneArtifactBlobs only removes unreferenced objects after the upload grace
// period. FK locks keep publication and collection mutually consistent.
func (s *Store) PruneArtifactBlobs(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `WITH victims AS (SELECT b.user_id,b.sha256 FROM artifact_blobs b
		WHERE b.updated_at<now()-interval '24 hours' AND NOT EXISTS(SELECT 1 FROM artifact_revision_blobs r WHERE r.user_id=b.user_id AND r.sha256=b.sha256)
		ORDER BY b.updated_at LIMIT 100 FOR UPDATE SKIP LOCKED)
		DELETE FROM artifact_blobs b USING victims v WHERE b.user_id=v.user_id AND b.sha256=v.sha256`)
	return err
}

type ArtifactObjectDeletion struct {
	Key string
	ID  string
}

func (s *Store) ArtifactObjectDeletions(ctx context.Context) ([]ArtifactObjectDeletion, error) {
	rows, err := s.pool.Query(ctx, "SELECT object_key,object_id FROM artifact_object_deletions WHERE retry_at<=now() ORDER BY retry_at LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ArtifactObjectDeletion{}
	for rows.Next() {
		var d ArtifactObjectDeletion
		if err := rows.Scan(&d.Key, &d.ID); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) FinishArtifactObjectDeletion(ctx context.Context, d ArtifactObjectDeletion, ok bool) error {
	if ok {
		_, err := s.pool.Exec(ctx, "DELETE FROM artifact_object_deletions WHERE object_key=$1 AND object_id=$2", d.Key, d.ID)
		return err
	}
	_, err := s.pool.Exec(ctx, "UPDATE artifact_object_deletions SET attempts=attempts+1,retry_at=now()+interval '5 minutes' WHERE object_key=$1 AND object_id=$2", d.Key, d.ID)
	return err
}
