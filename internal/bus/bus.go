// Package bus is Finalechat's real-time event fabric. Events are published
// through PostgreSQL NOTIFY so that every replica sees every event, and fanned
// out in-process to server-sent-event streams and long-poll waiters.
package bus

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const channel = "finalechat_events"

// Event is the compact envelope carried over NOTIFY. Consumers that need the
// full object load it from the database using the IDs; the payload limit for
// NOTIFY is 8000 bytes so bodies are never carried here.
type Event struct {
	Type       string    `json:"type"`
	UserID     string    `json:"user_id"`
	ThreadID   string    `json:"thread_id,omitempty"`
	MessageID  string    `json:"message_id,omitempty"`
	QuestionID string    `json:"question_id,omitempty"`
	At         time.Time `json:"at"`
}

// Event types.
const (
	ThreadCreated     = "thread.created"
	ThreadUpdated     = "thread.updated"
	ThreadDeleted     = "thread.deleted"
	MessageCreated    = "message.created"
	QuestionCreated   = "question.created"
	QuestionAnswered  = "question.answered"
	QuestionCancelled = "question.cancelled"
	QuestionExpired   = "question.expired"
	SettingsUpdated   = "settings.updated"
)

type subscriber struct {
	userID string
	ch     chan Event
}

// Bus fans events out to subscribers.
type Bus struct {
	pool *pgxpool.Pool
	log  *slog.Logger

	mu   sync.RWMutex
	subs map[*subscriber]struct{}
}

// New creates a bus backed by the pool.
func New(pool *pgxpool.Pool, log *slog.Logger) *Bus {
	return &Bus{pool: pool, log: log, subs: map[*subscriber]struct{}{}}
}

// Publish delivers an event to every replica. It is safe to call from within
// request handlers after the corresponding transaction has committed.
func (b *Bus) Publish(ctx context.Context, ev Event) {
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		b.log.Error("marshal event", "err", err)
		return
	}
	if _, err := b.pool.Exec(ctx, "SELECT pg_notify($1, $2)", channel, string(payload)); err != nil {
		b.log.Error("publish event", "err", err, "type", ev.Type)
		// Fall back to local delivery so at least this replica's clients see it.
		b.dispatch(ev)
	}
}

// Subscribe returns a channel that receives every event for the user. The
// channel is buffered; a slow consumer that fills it is dropped rather than
// allowed to stall the bus, and should reconnect.
func (b *Bus) Subscribe(userID string) (<-chan Event, func()) {
	s := &subscriber{userID: userID, ch: make(chan Event, 64)}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, s)
			b.mu.Unlock()
		})
	}
	return s.ch, cancel
}

func (b *Bus) dispatch(ev Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for s := range b.subs {
		if s.userID != ev.UserID {
			continue
		}
		select {
		case s.ch <- ev:
		default:
			b.log.Warn("dropping event for slow subscriber", "user_id", ev.UserID, "type", ev.Type)
		}
	}
}

// Run listens for notifications until ctx is cancelled, reconnecting with
// backoff when the listening connection fails.
func (b *Bus) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		err := b.listen(ctx)
		if ctx.Err() != nil {
			return
		}
		b.log.Warn("event listener disconnected", "err", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (b *Bus) listen(ctx context.Context) error {
	conn, err := b.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	// Hijack so the pool never hands this connection to a query while it is
	// in LISTEN mode.
	raw := conn.Hijack()
	defer raw.Close(context.Background())

	if _, err := raw.Exec(ctx, "LISTEN "+channel); err != nil {
		return err
	}
	b.log.Info("event listener connected")
	for {
		notification, err := raw.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		var ev Event
		if err := json.Unmarshal([]byte(notification.Payload), &ev); err != nil {
			b.log.Error("decode event", "err", err)
			continue
		}
		b.dispatch(ev)
	}
}

// Ensure pgx is referenced for the hijacked connection type.
var _ = pgx.ErrNoRows
