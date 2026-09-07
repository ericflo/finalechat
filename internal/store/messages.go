package store

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Sender values.
const (
	SenderAgent  = "agent"
	SenderUser   = "user"
	SenderSystem = "system"
)

// Importance values.
const (
	ImportanceNormal    = "normal"
	ImportanceImportant = "important"
)

// Message is one entry in a thread.
type Message struct {
	ID         uuid.UUID `json:"id"`
	ThreadID   uuid.UUID `json:"thread_id"`
	UserID     uuid.UUID `json:"-"`
	Sender     string    `json:"sender"`
	Body       string    `json:"body"`
	Format     string    `json:"format"`
	Importance string    `json:"importance"`
	Meta       JSON      `json:"meta"`
	CreatedAt  time.Time `json:"created_at"`
}

const messageColumns = "m.id, m.thread_id, m.user_id, m.sender, m.body, m.format, m.importance, m.meta, m.created_at"

func scanMessage(row pgx.Row) (*Message, error) {
	var m Message
	var meta []byte
	if err := row.Scan(&m.ID, &m.ThreadID, &m.UserID, &m.Sender, &m.Body, &m.Format, &m.Importance, &meta, &m.CreatedAt); err != nil {
		return nil, translate(err)
	}
	scanJSON(meta, &m.Meta)
	return &m, nil
}

// MessageInput describes a message to create.
type MessageInput struct {
	Sender     string
	Body       string
	Format     string
	Importance string
	Meta       JSON
}

// MaxBodyBytes bounds a message body.
const MaxBodyBytes = 256 * 1024

// Preview renders the inbox preview of a body.
func Preview(body string) string {
	body = strings.TrimSpace(body)
	// Collapse whitespace and strip the most common markdown decorations.
	replacer := strings.NewReplacer("\r", " ", "\n", " ", "\t", " ", "**", "", "__", "", "`", "", "# ", "", "## ", "", "### ", "", "> ", "")
	body = replacer.Replace(body)
	for strings.Contains(body, "  ") {
		body = strings.ReplaceAll(body, "  ", " ")
	}
	if utf8.RuneCountInString(body) > 160 {
		runes := []rune(body)
		body = strings.TrimSpace(string(runes[:157])) + "…"
	}
	return body
}

// CreateMessage appends a message and bumps the thread. The returned thread
// reflects the new preview and activity time.
func (s *Store) CreateMessage(ctx context.Context, userID, threadID uuid.UUID, in MessageInput) (*Message, *Thread, error) {
	var msg *Message
	var thread *Thread
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		msg, err = scanMessage(tx.QueryRow(ctx, `WITH m AS (
				INSERT INTO messages (id, thread_id, user_id, sender, body, format, importance, meta)
				SELECT $1, $2, $3, $4, $5, $6, $7, $8
				WHERE EXISTS (SELECT 1 FROM threads WHERE id = $2 AND user_id = $3)
				RETURNING *
			) SELECT `+messageColumns+` FROM m`,
			NewID(), threadID, userID, in.Sender, in.Body, in.Format, in.Importance, in.Meta.value()))
		if err != nil {
			return err
		}
		lastRead := "t.last_read_at"
		if in.Sender == SenderUser {
			// The user's own reply implies they have read everything before it.
			lastRead = "GREATEST(t.last_read_at, m.created_at)"
		}
		thread, err = scanThread(tx.QueryRow(ctx, `WITH m AS (SELECT $3::timestamptz AS created_at)
			UPDATE threads t SET preview = $4, preview_sender = $5, last_activity_at = m.created_at, updated_at = now(),
				last_read_at = `+lastRead+`,
				archived_at = NULL
			FROM m WHERE t.id = $1 AND t.user_id = $2 RETURNING `+threadColumns,
			threadID, userID, msg.CreatedAt, Preview(in.Body), in.Sender))
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return msg, thread, nil
}

// GetMessage loads a message the user owns.
func (s *Store) GetMessage(ctx context.Context, userID, id uuid.UUID) (*Message, error) {
	return scanMessage(s.pool.QueryRow(ctx, "SELECT "+messageColumns+" FROM messages m WHERE m.id = $1 AND m.user_id = $2", id, userID))
}

// MessagePage selects a window of a thread's messages.
type MessagePage struct {
	// After returns messages created after this message id (ascending).
	After *uuid.UUID
	// AfterTime returns messages created after this instant (ascending); used
	// by waits that have no message to anchor on.
	AfterTime *time.Time
	// Before returns messages created before this message id (ascending order,
	// newest window first when combined with Limit).
	Before *uuid.UUID
	// Limit caps the page size.
	Limit int
	// Sender, when set, filters by sender.
	Sender string
}

// ListMessages returns messages in ascending order. Without a cursor it
// returns the newest Limit messages. hasMore reports whether older messages
// exist before the returned window (or, with After, whether more newer ones do).
func (s *Store) ListMessages(ctx context.Context, userID, threadID uuid.UUID, p MessagePage) (msgs []*Message, hasMore bool, err error) {
	if p.Limit <= 0 || p.Limit > 500 {
		p.Limit = 100
	}
	args := []any{threadID, userID, p.Limit + 1}
	where := "m.thread_id = $1 AND m.user_id = $2"
	order := "ORDER BY m.created_at DESC, m.id DESC"
	reverse := true
	if p.After != nil {
		args = append(args, *p.After)
		where += " AND (m.created_at, m.id) > (SELECT c.created_at, c.id FROM messages c WHERE c.id = $4)"
		order = "ORDER BY m.created_at ASC, m.id ASC"
		reverse = false
	} else if p.AfterTime != nil {
		args = append(args, *p.AfterTime)
		where += " AND m.created_at > $4"
		order = "ORDER BY m.created_at ASC, m.id ASC"
		reverse = false
	} else if p.Before != nil {
		args = append(args, *p.Before)
		where += " AND (m.created_at, m.id) < (SELECT c.created_at, c.id FROM messages c WHERE c.id = $4)"
	}
	if p.Sender != "" {
		args = append(args, p.Sender)
		where += " AND m.sender = $" + itoa(len(args))
	}
	rows, err := s.pool.Query(ctx, "SELECT "+messageColumns+" FROM messages m WHERE "+where+" "+order+" LIMIT $3", args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []*Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(out) > p.Limit {
		out = out[:p.Limit]
		hasMore = true
	}
	if reverse {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out, hasMore, nil
}
