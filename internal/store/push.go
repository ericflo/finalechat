package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PushSubscription is a browser push endpoint.
type PushSubscription struct {
	ID            uuid.UUID  `json:"id"`
	UserID        uuid.UUID  `json:"-"`
	Endpoint      string     `json:"endpoint"`
	P256DH        string     `json:"-"`
	Auth          string     `json:"-"`
	UserAgent     string     `json:"user_agent"`
	CreatedAt     time.Time  `json:"created_at"`
	LastSuccessAt *time.Time `json:"last_success_at"`
	FailureCount  int        `json:"failure_count"`
}

const pushColumns = "p.id, p.user_id, p.endpoint, p.p256dh, p.auth, p.user_agent, p.created_at, p.last_success_at, p.failure_count"

func scanPush(row pgx.Row) (*PushSubscription, error) {
	var p PushSubscription
	if err := row.Scan(&p.ID, &p.UserID, &p.Endpoint, &p.P256DH, &p.Auth, &p.UserAgent, &p.CreatedAt, &p.LastSuccessAt, &p.FailureCount); err != nil {
		return nil, translate(err)
	}
	return &p, nil
}

// UpsertPushSubscription stores or refreshes a subscription. An endpoint
// already registered by another user is refused with ErrConflict; push
// services hand out fresh endpoints per install, so a genuine move shows up
// as a new endpoint and the old one is removed when the service reports it
// gone.
func (s *Store) UpsertPushSubscription(ctx context.Context, userID uuid.UUID, endpoint, p256dh, auth, userAgent string) (*PushSubscription, error) {
	sub, err := scanPush(s.pool.QueryRow(ctx, `WITH p AS (
			INSERT INTO push_subscriptions (id, user_id, endpoint, p256dh, auth, user_agent)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (endpoint) DO UPDATE SET p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth,
				user_agent = EXCLUDED.user_agent, failure_count = 0
			WHERE push_subscriptions.user_id = EXCLUDED.user_id
			RETURNING *
		) SELECT `+pushColumns+` FROM p`, NewID(), userID, endpoint, p256dh, auth, truncate(userAgent, 512)))
	if err == ErrNotFound {
		return nil, ErrConflict
	}
	return sub, err
}

// DeletePushSubscription removes an endpoint owned by the user.
func (s *Store) DeletePushSubscription(ctx context.Context, userID uuid.UUID, endpoint string) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM push_subscriptions WHERE user_id = $1 AND endpoint = $2", userID, endpoint)
	return err
}

// DeletePushSubscriptionByID removes an endpoint regardless of owner; used
// when a push service reports the subscription gone.
func (s *Store) DeletePushSubscriptionByID(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM push_subscriptions WHERE id = $1", id)
	return err
}

// ListPushSubscriptions lists a user's endpoints.
func (s *Store) ListPushSubscriptions(ctx context.Context, userID uuid.UUID) ([]*PushSubscription, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+pushColumns+" FROM push_subscriptions p WHERE p.user_id = $1 ORDER BY p.created_at DESC", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*PushSubscription{}
	for rows.Next() {
		p, err := scanPush(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RecordPushResult updates delivery bookkeeping.
func (s *Store) RecordPushResult(ctx context.Context, id uuid.UUID, ok bool) error {
	var err error
	if ok {
		_, err = s.pool.Exec(ctx, "UPDATE push_subscriptions SET last_success_at = now(), failure_count = 0 WHERE id = $1", id)
	} else {
		_, err = s.pool.Exec(ctx, "UPDATE push_subscriptions SET failure_count = failure_count + 1 WHERE id = $1", id)
	}
	return err
}
