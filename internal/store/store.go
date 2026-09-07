// Package store is Finalechat's persistence layer. It exposes typed methods
// over PostgreSQL and owns every SQL statement in the application.
package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a row does not exist or is not visible to the
// requesting user.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned on unique-constraint violations.
var ErrConflict = errors.New("conflict")

// ErrInvalidState is returned when a state transition is not allowed, for
// example answering a question that has already been answered.
var ErrInvalidState = errors.New("invalid state")

// Store wraps a connection pool.
type Store struct {
	pool *pgxpool.Pool
}

// New creates a store.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Pool exposes the underlying pool for health checks.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// NewID returns a time-ordered UUID (v7).
func NewID() uuid.UUID {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.New()
	}
	return id
}

// JSON is a loosely typed JSON object column.
type JSON map[string]any

// Value renders the object for a jsonb parameter.
func (j JSON) value() []byte {
	if j == nil {
		return []byte("{}")
	}
	b, err := json.Marshal(j)
	if err != nil {
		return []byte("{}")
	}
	return b
}

func scanJSON(raw []byte, into *JSON) {
	if len(raw) == 0 {
		*into = JSON{}
		return
	}
	var m JSON
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		m = JSON{}
	}
	*into = m
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case isUniqueViolation(err):
		return ErrConflict
	}
	return err
}

// Cursor encodes a (timestamp, id) keyset position.
type Cursor struct {
	At time.Time
	ID uuid.UUID
}

// Encode renders the cursor as an opaque string.
func (c Cursor) Encode() string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d|%s", c.At.UnixMicro(), c.ID)))
}

// DecodeCursor parses a cursor produced by Encode.
func DecodeCursor(s string) (Cursor, error) {
	if s == "" {
		return Cursor{}, errors.New("empty cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, err
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return Cursor{}, errors.New("malformed cursor")
	}
	var micros int64
	if _, err := fmt.Sscanf(parts[0], "%d", &micros); err != nil {
		return Cursor{}, err
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return Cursor{}, err
	}
	return Cursor{At: time.UnixMicro(micros).UTC(), ID: id}, nil
}

func (s *Store) withTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, s.pool, fn)
}
