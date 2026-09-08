package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/ericflo/finalechat/internal/artifact"
	"github.com/ericflo/finalechat/internal/control"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrConnectorOffline = errors.New("connector is offline")

type Connector struct {
	ID              uuid.UUID       `json:"id"`
	UserID          uuid.UUID       `json:"-"`
	Name            string          `json:"name"`
	Provider        string          `json:"provider"`
	State           string          `json:"state"`
	RequestedGrants []control.Grant `json:"requested_grants"`
	Grants          []control.Grant `json:"grants"`
	CreatedAt       time.Time       `json:"created_at"`
	ExpiresAt       time.Time       `json:"expires_at"`
	LastSeenAt      *time.Time      `json:"last_seen_at"`
	Instance        string          `json:"instance"`
}

const connectorColumns = "id,user_id,name,provider,state,requested_grants,grants,created_at,expires_at,last_seen_at,instance"

func scanConnector(row pgx.Row) (*Connector, error) {
	var c Connector
	var requested, grants []byte
	err := row.Scan(&c.ID, &c.UserID, &c.Name, &c.Provider, &c.State, &requested, &grants, &c.CreatedAt, &c.ExpiresAt, &c.LastSeenAt, &c.Instance)
	if err != nil {
		return nil, translate(err)
	}
	if err = json.Unmarshal(requested, &c.RequestedGrants); err != nil {
		return nil, err
	}
	err = json.Unmarshal(grants, &c.Grants)
	return &c, err
}
func (c Connector) Online() bool {
	return c.State == "active" && c.LastSeenAt != nil && time.Since(*c.LastSeenAt) < 90*time.Second
}
func (c Connector) Grant(key string) (control.Grant, bool) {
	for _, g := range c.Grants {
		if g.Key == key {
			return g, true
		}
	}
	return control.Grant{}, false
}

func (s *Store) CreateConnector(ctx context.Context, userID uuid.UUID, name, provider string, hash []byte, grants []control.Grant) (*Connector, error) {
	raw, _ := json.Marshal(grants)
	return scanConnector(s.pool.QueryRow(ctx, `INSERT INTO connectors(id,user_id,name,provider,token_hash,requested_grants)
		SELECT $1,$2,$3,$4,$5,$6 WHERE (SELECT count(*) FROM connectors WHERE user_id=$2 AND state<>'revoked')<100 RETURNING `+connectorColumns, NewID(), userID, name, provider, hash, raw))
}
func (s *Store) GetConnector(ctx context.Context, userID, id uuid.UUID) (*Connector, error) {
	return scanConnector(s.pool.QueryRow(ctx, "SELECT "+connectorColumns+" FROM connectors WHERE user_id=$1 AND id=$2", userID, id))
}
func (s *Store) ResolveConnector(ctx context.Context, hash []byte) (*Connector, error) {
	return scanConnector(s.pool.QueryRow(ctx, "SELECT "+connectorColumns+" FROM connectors WHERE token_hash=$1 AND state<>'revoked' AND (state='active' OR expires_at>now())", hash))
}
func (s *Store) ListConnectors(ctx context.Context, userID uuid.UUID) ([]*Connector, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+connectorColumns+" FROM connectors WHERE user_id=$1 ORDER BY id DESC LIMIT 100", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Connector{}
	for rows.Next() {
		c, err := scanConnector(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) ApproveConnector(ctx context.Context, userID, id uuid.UUID, grants []control.Grant) (*Connector, error) {
	var out *Connector
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		c, err := scanConnector(tx.QueryRow(ctx, "SELECT "+connectorColumns+" FROM connectors WHERE user_id=$1 AND id=$2 FOR UPDATE", userID, id))
		if err != nil {
			return err
		}
		if c.State != "pending" || time.Now().After(c.ExpiresAt) {
			return ErrInvalidState
		}
		seen := map[string]bool{}
		if len(grants) == 0 {
			return ErrInvalidState
		}
		for _, g := range grants {
			if g.Validate() != nil || seen[g.Key] {
				return ErrInvalidState
			}
			seen[g.Key] = true
			found := false
			for _, want := range c.RequestedGrants {
				if want.Key != g.Key {
					continue
				}
				if want.Label != g.Label || want.Scope != g.Scope {
					return ErrInvalidState
				}
				for _, op := range g.Operations {
					if !slices.Contains(want.Operations, op) {
						return ErrInvalidState
					}
				}
				for _, cl := range g.Classes {
					if !slices.Contains(want.Classes, cl) {
						return ErrInvalidState
					}
				}
				found = true
			}
			if !found {
				return ErrInvalidState
			}
		}
		raw, _ := json.Marshal(grants)
		out, err = scanConnector(tx.QueryRow(ctx, "UPDATE connectors SET state='active',grants=$3 WHERE user_id=$1 AND id=$2 RETURNING "+connectorColumns, userID, id, raw))
		return err
	})
	return out, err
}
func (s *Store) RevokeConnector(ctx context.Context, userID, id uuid.UUID) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "UPDATE connectors SET state='revoked',last_seen_at=NULL WHERE user_id=$1 AND id=$2", userID, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrNotFound
		}
		_, err = tx.Exec(ctx, `WITH changed AS (UPDATE settings_commands SET status=CASE WHEN status='queued' THEN 'cancelled' ELSE 'unknown' END,finished_at=now(),result='{"reason":"connector_revoked"}' WHERE connector_id=$1 AND status IN ('queued','executing') RETURNING *)
	INSERT INTO settings_audit(id,user_id,resource_id,command_id,event,detail) SELECT id,user_id,resource_id,id,'connector.revoked',jsonb_build_object('status',status) FROM changed ON CONFLICT DO NOTHING`, id)
		return err
	})
}
func (s *Store) HeartbeatConnector(ctx context.Context, userID, id uuid.UUID, instance string) (*Connector, error) {
	return scanConnector(s.pool.QueryRow(ctx, `UPDATE connectors SET last_seen_at=now(),instance=$3 WHERE user_id=$1 AND id=$2 AND state='active' AND (instance=$3 OR last_seen_at IS NULL OR last_seen_at<now()-interval '90 seconds') RETURNING `+connectorColumns, userID, id, instance))
}

type SettingsResource struct {
	ID          uuid.UUID          `json:"id"`
	UserID      uuid.UUID          `json:"-"`
	ConnectorID uuid.UUID          `json:"connector_id"`
	Key         string             `json:"key"`
	Label       string             `json:"label"`
	Scope       string             `json:"scope"`
	Generation  string             `json:"generation"`
	Descriptor  control.Descriptor `json:"descriptor"`
	Snapshot    control.Snapshot   `json:"snapshot"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

const resourceColumns = "id,user_id,connector_id,resource_key,label,scope,generation,descriptor,snapshot,updated_at"

func scanResource(row pgx.Row) (*SettingsResource, error) {
	var r SettingsResource
	var d, s []byte
	err := row.Scan(&r.ID, &r.UserID, &r.ConnectorID, &r.Key, &r.Label, &r.Scope, &r.Generation, &d, &s, &r.UpdatedAt)
	if err != nil {
		return nil, translate(err)
	}
	if err = json.Unmarshal(d, &r.Descriptor); err != nil {
		return nil, err
	}
	err = json.Unmarshal(s, &r.Snapshot)
	return &r, err
}
func (s *Store) GetSettingsResource(ctx context.Context, userID, id uuid.UUID) (*SettingsResource, error) {
	return scanResource(s.pool.QueryRow(ctx, "SELECT "+resourceColumns+" FROM settings_resources WHERE user_id=$1 AND id=$2", userID, id))
}
func (s *Store) ListSettingsResources(ctx context.Context, userID, connectorID uuid.UUID) ([]*SettingsResource, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+resourceColumns+" FROM settings_resources WHERE user_id=$1 AND connector_id=$2 ORDER BY resource_key", userID, connectorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SettingsResource{}
	for rows.Next() {
		r, err := scanResource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) PublishSettingsResource(ctx context.Context, userID, connectorID uuid.UUID, instance, key, generation string, d control.Descriptor, snap control.Snapshot) (*SettingsResource, error) {
	var out *SettingsResource
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		c, err := scanConnector(tx.QueryRow(ctx, "SELECT "+connectorColumns+" FROM connectors WHERE user_id=$1 AND id=$2 FOR UPDATE", userID, connectorID))
		if err != nil {
			return err
		}
		if !c.Online() || instance != c.Instance {
			return ErrConnectorOffline
		}
		g, ok := c.Grant(key)
		if !ok {
			return ErrInvalidState
		}
		if err := d.Validate(g); err != nil {
			return err
		}
		if err := snap.Validate(); err != nil {
			return err
		}
		if g.Scope == "session" && generation == "" {
			return ErrInvalidState
		}
		rawD, _ := json.Marshal(d)
		rawS, _ := json.Marshal(snap)
		out, err = scanResource(tx.QueryRow(ctx, `INSERT INTO settings_resources(id,user_id,connector_id,resource_key,label,scope,generation,descriptor,snapshot) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT(connector_id,resource_key) DO UPDATE SET generation=EXCLUDED.generation,descriptor=EXCLUDED.descriptor,snapshot=EXCLUDED.snapshot,updated_at=now() RETURNING `+resourceColumns, NewID(), userID, connectorID, key, g.Label, g.Scope, generation, rawD, rawS))
		return err
	})
	return out, err
}

type SettingsBinding struct {
	ArtifactID uuid.UUID `json:"artifact_id"`
	ResourceID uuid.UUID `json:"resource_id"`
	RevisionID uuid.UUID `json:"revision_id"`
	Generation string    `json:"generation"`
}

func (s *Store) BindSettingsArtifact(ctx context.Context, userID, connectorID, resourceID, artifactID, revisionID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `INSERT INTO settings_bindings(artifact_id,resource_id,revision_id,generation)
	SELECT a.id,r.id,v.id,r.generation FROM settings_resources r JOIN connectors c ON c.id=r.connector_id CROSS JOIN artifacts a JOIN artifact_revisions v ON v.artifact_id=a.id
	WHERE r.id=$1 AND r.connector_id=$2 AND r.user_id=$3 AND a.user_id=$3 AND a.id=$4 AND v.id=$5 AND a.current_revision_id=v.id AND c.state='active' AND coalesce(v.manifest->>'settings_entrypoint','')<>''
	ON CONFLICT(artifact_id) DO UPDATE SET resource_id=EXCLUDED.resource_id,revision_id=EXCLUDED.revision_id,generation=EXCLUDED.generation,updated_at=now()
	WHERE settings_bindings.resource_id=EXCLUDED.resource_id`, resourceID, connectorID, userID, artifactID, revisionID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}
func (s *Store) GetSettingsBinding(ctx context.Context, userID, artifactID uuid.UUID) (*SettingsBinding, error) {
	var b SettingsBinding
	err := s.pool.QueryRow(ctx, `SELECT b.artifact_id,b.resource_id,b.revision_id,b.generation FROM settings_bindings b JOIN artifacts a ON a.id=b.artifact_id WHERE a.user_id=$1 AND a.id=$2`, userID, artifactID).Scan(&b.ArtifactID, &b.ResourceID, &b.RevisionID, &b.Generation)
	return &b, translate(err)
}

type SettingsCommand struct {
	ID             uuid.UUID        `json:"id"`
	UserID         uuid.UUID        `json:"user_id"`
	ConnectorID    uuid.UUID        `json:"connector_id"`
	ResourceID     uuid.UUID        `json:"resource_id"`
	ArtifactID     *uuid.UUID       `json:"artifact_id"`
	RevisionID     *uuid.UUID       `json:"revision_id"`
	ClientKey      string           `json:"client_key"`
	Proposal       control.Proposal `json:"proposal"`
	ProposalSHA256 string           `json:"proposal_sha256"`
	Status         string           `json:"status"`
	CreatedAt      time.Time        `json:"created_at"`
	ExpiresAt      time.Time        `json:"expires_at"`
	ClaimToken     *uuid.UUID       `json:"claim_token,omitempty"`
	ClaimInstance  *string          `json:"claim_instance,omitempty"`
	LeaseUntil     *time.Time       `json:"lease_until,omitempty"`
	Attempts       int              `json:"attempts"`
	Result         JSON             `json:"result"`
	FinishedAt     *time.Time       `json:"finished_at"`
}

const commandColumns = "id,user_id,connector_id,resource_id,artifact_id,revision_id,client_key,proposal,proposal_sha256,status,created_at,expires_at,claim_token,claim_instance,lease_until,attempts,result,finished_at"

func scanCommand(row pgx.Row) (*SettingsCommand, error) {
	var c SettingsCommand
	var p, r []byte
	err := row.Scan(&c.ID, &c.UserID, &c.ConnectorID, &c.ResourceID, &c.ArtifactID, &c.RevisionID, &c.ClientKey, &p, &c.ProposalSHA256, &c.Status, &c.CreatedAt, &c.ExpiresAt, &c.ClaimToken, &c.ClaimInstance, &c.LeaseUntil, &c.Attempts, &r, &c.FinishedAt)
	if err != nil {
		return nil, translate(err)
	}
	if err = json.Unmarshal(p, &c.Proposal); err != nil {
		return nil, err
	}
	scanJSON(r, &c.Result)
	return &c, nil
}
func (c *SettingsCommand) ForBrowser() { c.ClaimToken = nil; c.ClaimInstance = nil }
func (s *Store) GetSettingsCommand(ctx context.Context, userID, id uuid.UUID) (*SettingsCommand, error) {
	return scanCommand(s.pool.QueryRow(ctx, "SELECT "+commandColumns+" FROM settings_commands WHERE user_id=$1 AND id=$2", userID, id))
}

func (s *Store) CreateSettingsCommand(ctx context.Context, userID, resourceID uuid.UUID, artifactID, revisionID, surfaceLeaseID *uuid.UUID, key string, p control.Proposal, expires time.Time, allowOffline bool) (*SettingsCommand, bool, error) {
	raw, _ := json.Marshal(p)
	digest := artifact.Digest(raw)
	var out *SettingsCommand
	created := false
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		// One user idempotency namespace prevents retries from changing targets.
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 7318))", userID.String()); err != nil {
			return err
		}
		old, err := scanCommand(tx.QueryRow(ctx, "SELECT "+commandColumns+" FROM settings_commands WHERE user_id=$1 AND client_key=$2", userID, key))
		if err == nil {
			if old.ResourceID != resourceID || old.ProposalSHA256 != digest || !sameUUID(old.ArtifactID, artifactID) || !sameUUID(old.RevisionID, revisionID) {
				return ErrConflict
			}
			out = old
			return nil
		}
		if err != ErrNotFound {
			return err
		}
		r, err := scanResource(tx.QueryRow(ctx, "SELECT "+resourceColumns+" FROM settings_resources WHERE user_id=$1 AND id=$2", userID, resourceID))
		if err != nil {
			return err
		}
		c, err := scanConnector(tx.QueryRow(ctx, "SELECT "+connectorColumns+" FROM connectors WHERE id=$1 FOR SHARE", r.ConnectorID))
		if err != nil {
			return err
		}
		if c.State != "active" {
			return ErrInvalidState
		}
		r, err = scanResource(tx.QueryRow(ctx, "SELECT "+resourceColumns+" FROM settings_resources WHERE user_id=$1 AND id=$2 FOR SHARE", userID, resourceID))
		if err != nil {
			return err
		}
		g, ok := c.Grant(r.Key)
		if !ok {
			return ErrInvalidState
		}
		if err := p.Validate(r.Descriptor, g); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidState, err)
		}
		if p.ExpectedVersion != r.Snapshot.Version || p.Generation != r.Generation {
			return ErrConflict
		}
		if !c.Online() && (!allowOffline || r.Scope == "session" || p.Operation != "settings.apply") {
			return ErrConnectorOffline
		}
		if artifactID != nil {
			if revisionID == nil {
				return ErrInvalidState
			}
			var valid bool
			var err error
			if surfaceLeaseID == nil {
				err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM settings_bindings b JOIN artifacts a ON a.id=b.artifact_id WHERE b.artifact_id=$1 AND b.resource_id=$2 AND b.revision_id=$3 AND a.current_revision_id=$3 AND b.generation=$4)`, artifactID, resourceID, revisionID, p.Generation).Scan(&valid)
			} else {
				err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM settings_surface_leases l JOIN settings_bindings b ON b.artifact_id=l.artifact_id AND b.resource_id=l.resource_id AND b.generation=l.generation WHERE l.id=$1 AND l.user_id=$2 AND l.artifact_id=$3 AND l.revision_id=$4 AND l.resource_id=$5 AND l.generation=$6 AND l.expires_at>now())`, surfaceLeaseID, userID, artifactID, revisionID, resourceID, p.Generation).Scan(&valid)
			}
			if err != nil {
				return err
			}
			if !valid {
				return ErrConflict
			}
		}
		var pending int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM settings_commands WHERE resource_id=$1 AND status IN ('queued','executing')", resourceID).Scan(&pending); err != nil {
			return err
		}
		if pending >= 20 {
			return ErrInvalidState
		}
		out, err = scanCommand(tx.QueryRow(ctx, `INSERT INTO settings_commands(id,user_id,connector_id,resource_id,artifact_id,revision_id,client_key,proposal,proposal_sha256,status,expires_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'queued',$10) RETURNING `+commandColumns, NewID(), userID, r.ConnectorID, resourceID, artifactID, revisionID, key, raw, digest, expires))
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO settings_audit(id,user_id,resource_id,command_id,event,detail) VALUES($1,$2,$3,$4,'command.created',$5)`, NewID(), userID, resourceID, out.ID, JSON{"proposal_sha256": digest, "operation": p.Operation, "expected_version": p.ExpectedVersion}.value())
		created = err == nil
		return err
	})
	return out, created, err
}
func sameUUID(a, b *uuid.UUID) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// ClaimSettingsCommand redelivers expired claims for reconciliation only:
// attempts>1 NEVER grants permission to repeat an uncertain external effect.
// The local durable journal must establish whether it executed already.
func (s *Store) ClaimSettingsCommand(ctx context.Context, userID, connectorID uuid.UUID, instance string) (*SettingsCommand, error) {
	var out *SettingsCommand
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		c, err := scanConnector(tx.QueryRow(ctx, "SELECT "+connectorColumns+" FROM connectors WHERE user_id=$1 AND id=$2 FOR UPDATE", userID, connectorID))
		if err != nil {
			return err
		}
		if !c.Online() || c.Instance != instance {
			return ErrConnectorOffline
		}
		if err := expireCommands(ctx, tx, connectorID); err != nil {
			return err
		}
		out, err = scanCommand(tx.QueryRow(ctx, `SELECT `+commandColumns+` FROM settings_commands q WHERE connector_id=$1 AND expires_at>now() AND (status='queued' OR (status='executing' AND lease_until<now()))
		AND NOT EXISTS(SELECT 1 FROM settings_commands active WHERE active.resource_id=q.resource_id AND active.id<>q.id AND active.status='executing' AND active.lease_until>=now()) ORDER BY CASE WHEN status='executing' THEN 0 ELSE 1 END,id LIMIT 1 FOR UPDATE SKIP LOCKED`, connectorID))
		if err == ErrNotFound {
			out = nil
			return nil
		}
		if err != nil {
			return err
		}
		out, err = scanCommand(tx.QueryRow(ctx, `UPDATE settings_commands SET status='executing',claim_token=$2,claim_instance=$3,lease_until=now()+interval '60 seconds',attempts=attempts+1 WHERE id=$1 RETURNING `+commandColumns, out.ID, NewID(), instance))
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO settings_audit(id,user_id,resource_id,command_id,event,detail) VALUES($1,$2,$3,$4,'command.claimed',$5)`, NewID(), userID, out.ResourceID, out.ID, JSON{"attempt": out.Attempts}.value())
		return err
	})
	return out, err
}
func expireCommands(ctx context.Context, tx pgx.Tx, connectorID uuid.UUID) error {
	_, err := tx.Exec(ctx, `WITH changed AS (UPDATE settings_commands SET status=CASE WHEN status='queued' THEN 'expired' ELSE 'unknown' END,finished_at=now(),result='{"reason":"deadline_elapsed"}' WHERE connector_id=$1 AND expires_at<=now() AND (status='queued' OR (status='executing' AND lease_until<now())) RETURNING *)
	INSERT INTO settings_audit(id,user_id,resource_id,command_id,event,detail) SELECT id,user_id,resource_id,id,'command.expired',jsonb_build_object('status',status) FROM changed ON CONFLICT DO NOTHING`, connectorID)
	return err
}
func (s *Store) RenewSettingsCommand(ctx context.Context, userID, connectorID, id, claim uuid.UUID, instance string) (*SettingsCommand, error) {
	return scanCommand(s.pool.QueryRow(ctx, `UPDATE settings_commands q SET lease_until=now()+interval '60 seconds' WHERE user_id=$1 AND connector_id=$2 AND id=$3 AND claim_token=$4 AND claim_instance=$5 AND status='executing' AND lease_until>now() AND expires_at>now() AND EXISTS(SELECT 1 FROM connectors c WHERE c.id=q.connector_id AND c.state='active' AND c.instance=$5 AND c.last_seen_at>now()-interval '90 seconds') RETURNING `+commandColumns, userID, connectorID, id, claim, instance))
}

func (s *Store) CompleteSettingsCommand(ctx context.Context, userID, connectorID, id, claim uuid.UUID, instance, status string, result JSON) (*SettingsCommand, error) {
	if !slices.Contains([]string{"succeeded", "rejected", "conflicted", "unknown"}, status) {
		return nil, ErrInvalidState
	}
	var out *SettingsCommand
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		c, err := scanConnector(tx.QueryRow(ctx, "SELECT "+connectorColumns+" FROM connectors WHERE user_id=$1 AND id=$2 FOR SHARE", userID, connectorID))
		if err != nil {
			return err
		}
		if c.State != "active" || c.Instance != instance {
			return ErrInvalidState
		}
		q, err := scanCommand(tx.QueryRow(ctx, "SELECT "+commandColumns+" FROM settings_commands WHERE user_id=$1 AND connector_id=$2 AND id=$3 FOR UPDATE", userID, connectorID, id))
		if err != nil {
			return err
		}
		if q.ClaimToken == nil || *q.ClaimToken != claim || q.ClaimInstance == nil || *q.ClaimInstance != instance {
			return ErrConflict
		}
		if q.Status != "executing" {
			a, _ := json.Marshal(q.Result)
			b, _ := json.Marshal(result)
			if q.Status == status && string(a) == string(b) {
				out = q
				return nil
			}
			return ErrConflict
		}
		if q.LeaseUntil == nil || time.Now().After(*q.LeaseUntil) {
			return ErrConflict
		}
		out, err = scanCommand(tx.QueryRow(ctx, "UPDATE settings_commands SET status=$2,result=$3,finished_at=now() WHERE id=$1 RETURNING "+commandColumns, id, status, result.value()))
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO settings_audit(id,user_id,resource_id,command_id,event,detail) VALUES($1,$2,$3,$4,$5,$6)`, NewID(), userID, out.ResourceID, id, "command."+status, result.value())
		return err
	})
	return out, err
}
func (s *Store) CancelSettingsCommand(ctx context.Context, userID, id uuid.UUID) (*SettingsCommand, error) {
	var out *SettingsCommand
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = scanCommand(tx.QueryRow(ctx, `UPDATE settings_commands SET status='cancelled',finished_at=now(),result='{"reason":"user_cancelled"}' WHERE user_id=$1 AND id=$2 AND status='queued' RETURNING `+commandColumns, userID, id))
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO settings_audit(id,user_id,resource_id,command_id,event,detail) VALUES($1,$2,$3,$4,'command.cancelled','{}')`, NewID(), userID, out.ResourceID, id)
		return err
	})
	return out, err
}
func (s *Store) SettingsAudit(ctx context.Context, userID, resourceID uuid.UUID, before *uuid.UUID) ([]JSON, error) {
	rows, err := s.pool.Query(ctx, `SELECT jsonb_build_object('id',id,'command_id',command_id,'event',event,'detail',detail,'created_at',created_at) FROM settings_audit WHERE user_id=$1 AND resource_id=$2 AND ($3::uuid IS NULL OR id<$3) ORDER BY id DESC LIMIT 100`, userID, resourceID, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []JSON{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item JSON
		scanJSON(raw, &item)
		out = append(out, item)
	}
	return out, rows.Err()
}
func (s *Store) SaveSettingsDraft(ctx context.Context, userID, resourceID uuid.UUID, p control.Proposal) error {
	raw, _ := json.Marshal(p)
	tag, err := s.pool.Exec(ctx, `INSERT INTO settings_drafts(user_id,resource_id,proposal) SELECT $1,$2,$3 WHERE EXISTS(SELECT 1 FROM settings_resources WHERE id=$2 AND user_id=$1) ON CONFLICT(user_id,resource_id) DO UPDATE SET proposal=EXCLUDED.proposal,updated_at=now()`, userID, resourceID, raw)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) GetSettingsDraft(ctx context.Context, userID, resourceID uuid.UUID) (*control.Proposal, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, "SELECT proposal FROM settings_drafts WHERE user_id=$1 AND resource_id=$2", userID, resourceID).Scan(&raw)
	if err != nil {
		return nil, translate(err)
	}
	var p control.Proposal
	err = json.Unmarshal(raw, &p)
	return &p, err
}
func (s *Store) DeleteSettingsDraft(ctx context.Context, userID, resourceID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM settings_drafts WHERE user_id=$1 AND resource_id=$2", userID, resourceID)
	return err
}

func (s *Store) ExpireSettingsCommands(ctx context.Context) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, "SELECT DISTINCT connector_id FROM settings_commands WHERE status IN ('queued','executing') AND expires_at<=now() LIMIT 100")
		if err != nil {
			return err
		}
		var ids []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, id := range ids {
			if err := expireCommands(ctx, tx, id); err != nil {
				return err
			}
		}
		return nil
	})
}
