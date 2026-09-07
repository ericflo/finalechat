package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Settings are per-user preferences.
type Settings struct {
	// NotifyAllMessages sends a push for every agent message, not only the
	// important ones and questions.
	NotifyAllMessages bool `json:"notify_all_messages"`
	// RemoteMode tells agent integrations that the user is away from the
	// terminal: hooks should block waiting for replies and answers from the
	// app instead of falling through to the terminal.
	RemoteMode bool `json:"remote_mode"`
}

// User is an account.
type User struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Settings    Settings  `json:"settings"`
	CreatedAt   time.Time `json:"created_at"`
}

const userColumns = "id, email, display_name, settings, created_at"

func scanUser(row pgx.Row) (*User, error) {
	var u User
	var settings []byte
	if err := row.Scan(&u.ID, &u.Email, &u.DisplayName, &settings, &u.CreatedAt); err != nil {
		return nil, translate(err)
	}
	if len(settings) > 0 {
		_ = json.Unmarshal(settings, &u.Settings)
	}
	return &u, nil
}

// CountUsers returns the number of accounts.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&n)
	return n, err
}

// CreateUser inserts an account.
func (s *Store) CreateUser(ctx context.Context, email, passwordHash, displayName string) (*User, error) {
	id := NewID()
	row := s.pool.QueryRow(ctx, `INSERT INTO users (id, email, password_hash, display_name)
		VALUES ($1, $2, $3, $4) RETURNING `+userColumns, id, strings.TrimSpace(email), passwordHash, strings.TrimSpace(displayName))
	return scanUser(row)
}

// GetUser loads an account by id.
func (s *Store) GetUser(ctx context.Context, id uuid.UUID) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx, "SELECT "+userColumns+" FROM users WHERE id = $1", id))
}

// GetUserByEmail loads an account and its password hash.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, string, error) {
	var u User
	var settings []byte
	var hash string
	err := s.pool.QueryRow(ctx, "SELECT "+userColumns+", password_hash FROM users WHERE email = $1", strings.TrimSpace(email)).
		Scan(&u.ID, &u.Email, &u.DisplayName, &settings, &u.CreatedAt, &hash)
	if err != nil {
		return nil, "", translate(err)
	}
	if len(settings) > 0 {
		_ = json.Unmarshal(settings, &u.Settings)
	}
	return &u, hash, nil
}

// UpdateUserSettings replaces the settings document.
func (s *Store) UpdateUserSettings(ctx context.Context, id uuid.UUID, settings Settings) (*User, error) {
	raw, _ := json.Marshal(settings)
	return scanUser(s.pool.QueryRow(ctx, `UPDATE users SET settings = $2, updated_at = now() WHERE id = $1 RETURNING `+userColumns, id, raw))
}

// UpdateUserProfile changes the display name.
func (s *Store) UpdateUserProfile(ctx context.Context, id uuid.UUID, displayName string) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx, `UPDATE users SET display_name = $2, updated_at = now() WHERE id = $1 RETURNING `+userColumns, id, strings.TrimSpace(displayName)))
}

// UpdatePassword replaces the password hash and invalidates other sessions.
func (s *Store) UpdatePassword(ctx context.Context, id uuid.UUID, passwordHash string, keepSession []byte) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1", id, passwordHash); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "DELETE FROM sessions WHERE user_id = $1 AND token_hash <> $2", id, keepSession)
		return err
	})
}

// Session is a browser login.
type Session struct {
	TokenHash  []byte
	UserID     uuid.UUID
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// CreateSession records a login.
func (s *Store) CreateSession(ctx context.Context, tokenHash []byte, userID uuid.UUID, userAgent string, ttl time.Duration) (*Session, error) {
	var sess Session
	err := s.pool.QueryRow(ctx, `INSERT INTO sessions (token_hash, user_id, user_agent, expires_at)
		VALUES ($1, $2, $3, now() + $4) RETURNING token_hash, user_id, created_at, last_seen_at, expires_at`,
		tokenHash, userID, truncate(userAgent, 512), ttl).
		Scan(&sess.TokenHash, &sess.UserID, &sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt)
	return &sess, translate(err)
}

// ResolveSession loads the user for a session, sliding its expiry when it was
// last touched more than ten minutes ago.
func (s *Store) ResolveSession(ctx context.Context, tokenHash []byte, ttl time.Duration) (*User, error) {
	var u User
	var settings []byte
	var lastSeen time.Time
	err := s.pool.QueryRow(ctx, `SELECT u.id, u.email, u.display_name, u.settings, u.created_at, s.last_seen_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`, tokenHash).
		Scan(&u.ID, &u.Email, &u.DisplayName, &settings, &u.CreatedAt, &lastSeen)
	if err != nil {
		return nil, translate(err)
	}
	if len(settings) > 0 {
		_ = json.Unmarshal(settings, &u.Settings)
	}
	if time.Since(lastSeen) > 10*time.Minute {
		_, _ = s.pool.Exec(ctx, "UPDATE sessions SET last_seen_at = now(), expires_at = now() + $2 WHERE token_hash = $1", tokenHash, ttl)
	}
	return &u, nil
}

// DeleteSession logs a browser out.
func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM sessions WHERE token_hash = $1", tokenHash)
	return err
}

// DeleteExpiredSessions is periodic housekeeping.
func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, "DELETE FROM sessions WHERE expires_at <= now()")
	return tag.RowsAffected(), err
}

// APIToken is an agent credential. The secret itself is never stored.
type APIToken struct {
	ID         uuid.UUID  `json:"id"`
	UserID     uuid.UUID  `json:"-"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

const tokenColumns = "id, user_id, name, prefix, created_at, last_used_at, revoked_at"

func scanToken(row pgx.Row) (*APIToken, error) {
	var t APIToken
	if err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.Prefix, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt); err != nil {
		return nil, translate(err)
	}
	return &t, nil
}

// CreateAPIToken stores the hash of a freshly minted token.
func (s *Store) CreateAPIToken(ctx context.Context, userID uuid.UUID, name string, tokenHash []byte, prefix string) (*APIToken, error) {
	return scanToken(s.pool.QueryRow(ctx, `INSERT INTO api_tokens (id, user_id, name, token_hash, prefix)
		VALUES ($1, $2, $3, $4, $5) RETURNING `+tokenColumns, NewID(), userID, truncate(strings.TrimSpace(name), 120), tokenHash, prefix))
}

// ListAPITokens lists active tokens, newest first.
func (s *Store) ListAPITokens(ctx context.Context, userID uuid.UUID) ([]*APIToken, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+tokenColumns+" FROM api_tokens WHERE user_id = $1 AND revoked_at IS NULL ORDER BY created_at DESC", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*APIToken{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeAPIToken disables a token.
func (s *Store) RevokeAPIToken(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, "UPDATE api_tokens SET revoked_at = now() WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL", id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ResolveAPIToken authenticates a bearer token and returns its user.
func (s *Store) ResolveAPIToken(ctx context.Context, tokenHash []byte) (*User, *APIToken, error) {
	var u User
	var t APIToken
	var settings []byte
	err := s.pool.QueryRow(ctx, `SELECT u.id, u.email, u.display_name, u.settings, u.created_at,
			t.id, t.user_id, t.name, t.prefix, t.created_at, t.last_used_at, t.revoked_at
		FROM api_tokens t JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = $1 AND t.revoked_at IS NULL`, tokenHash).
		Scan(&u.ID, &u.Email, &u.DisplayName, &settings, &u.CreatedAt,
			&t.ID, &t.UserID, &t.Name, &t.Prefix, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt)
	if err != nil {
		return nil, nil, translate(err)
	}
	if len(settings) > 0 {
		_ = json.Unmarshal(settings, &u.Settings)
	}
	if t.LastUsedAt == nil || time.Since(*t.LastUsedAt) > time.Minute {
		_, _ = s.pool.Exec(ctx, "UPDATE api_tokens SET last_used_at = now() WHERE id = $1", t.ID)
	}
	return &u, &t, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
