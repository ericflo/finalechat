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

// Message origins: how the message reached the server.
const (
	OriginSession = "session" // the app, signed in with a browser session
	OriginToken   = "token"   // an agent, using an API token
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
	// Origin is "session" when the app posted the message and "token" when
	// an agent did; older rows have "".
	Origin    string    `json:"origin"`
	CreatedAt time.Time `json:"created_at"`
	// Deleted marks a tombstone: the message was removed but its id and
	// position are kept so that pagination anchors keep working. Tombstones
	// are only returned on catch-up pages (after=<id> without a sender
	// filter) so clients can prune them.
	Deleted   bool       `json:"deleted,omitempty"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	// Attachments are the files carried by the message, in upload order.
	Attachments []*Attachment `json:"attachments"`
}

const messageColumns = "m.id, m.thread_id, m.user_id, m.sender, m.body, m.format, m.importance, m.meta, m.origin, m.created_at, m.deleted_at"

func scanMessage(row pgx.Row) (*Message, error) {
	var m Message
	var meta []byte
	if err := row.Scan(&m.ID, &m.ThreadID, &m.UserID, &m.Sender, &m.Body, &m.Format, &m.Importance, &meta, &m.Origin, &m.CreatedAt, &m.DeletedAt); err != nil {
		return nil, translate(err)
	}
	scanJSON(meta, &m.Meta)
	m.Deleted = m.DeletedAt != nil
	return &m, nil
}

// MessageInput describes a message to create.
type MessageInput struct {
	Sender     string
	Body       string
	Format     string
	Importance string
	Meta       JSON
	// Origin records who posted: OriginSession or OriginToken.
	Origin string
	// MarkRead advances the thread's read marker to this message; set for
	// the user's own replies from the app.
	MarkRead bool
	// ClientKey makes the create idempotent within the thread: a repeat with
	// the same key returns the first message instead of a duplicate.
	ClientKey string
	// Activity, when set, becomes the thread's status line in the same
	// transaction; otherwise an agent message clears the status.
	Activity *ActivityInput
	// ClearSeq is the sequence an agent post's implicit clear records (the
	// seq of a blank inline activity), so a status write still in flight
	// cannot land after the message and resurrect the line. Zero: unordered.
	ClearSeq int64
	// AttachmentIDs are pending uploads in the same thread to bind.
	AttachmentIDs []uuid.UUID
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
// reflects the new preview and activity time. created is false when
// in.ClientKey matched an existing message, which is returned instead.
func (s *Store) CreateMessage(ctx context.Context, userID, threadID uuid.UUID, in MessageInput) (msg *Message, thread *Thread, created bool, err error) {
	err = s.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		msg, thread, created, err = createMessageTx(ctx, tx, userID, threadID, in)
		return err
	})
	if err != nil {
		return nil, nil, false, err
	}
	if err := s.hydrateAttachments(ctx, userID, []*Message{msg}); err != nil {
		return nil, nil, false, err
	}
	return msg, thread, created, nil
}

// createMessageTx is CreateMessage inside a caller's transaction.
func createMessageTx(ctx context.Context, tx pgx.Tx, userID, threadID uuid.UUID, in MessageInput) (msg *Message, thread *Thread, created bool, err error) {
	var clientKey *string
	if in.ClientKey != "" {
		clientKey = &in.ClientKey
		existing, err := scanMessage(tx.QueryRow(ctx, "SELECT "+messageColumns+" FROM messages m WHERE m.thread_id = $1 AND m.user_id = $2 AND m.client_key = $3", threadID, userID, in.ClientKey))
		if err == nil {
			thread, err := scanThread(tx.QueryRow(ctx, "SELECT "+threadColumns+" FROM threads t WHERE t.id = $1 AND t.user_id = $2", threadID, userID))
			return existing, thread, false, err
		}
		if err != ErrNotFound {
			return nil, nil, false, err
		}
	}
	msg, err = scanMessage(tx.QueryRow(ctx, `WITH m AS (
			INSERT INTO messages (id, thread_id, user_id, sender, body, format, importance, meta, origin, client_key)
			SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
			WHERE EXISTS (SELECT 1 FROM threads WHERE id = $2 AND user_id = $3)
			ON CONFLICT (thread_id, client_key) WHERE client_key IS NOT NULL DO NOTHING
			RETURNING *
		) SELECT `+messageColumns+` FROM m`,
		NewID(), threadID, userID, in.Sender, in.Body, in.Format, in.Importance, in.Meta.value(), in.Origin, clientKey))
	if err == ErrNotFound && clientKey != nil {
		// A concurrent post with the same key won the insert; DO NOTHING
		// waited for it to commit, so this read sees it.
		existing, err := scanMessage(tx.QueryRow(ctx, "SELECT "+messageColumns+" FROM messages m WHERE m.thread_id = $1 AND m.user_id = $2 AND m.client_key = $3", threadID, userID, in.ClientKey))
		if err != nil {
			return nil, nil, false, err
		}
		thread, err := scanThread(tx.QueryRow(ctx, "SELECT "+threadColumns+" FROM threads t WHERE t.id = $1 AND t.user_id = $2", threadID, userID))
		return existing, thread, false, err
	}
	if err != nil {
		return nil, nil, false, err
	}
	if err := attachToMessage(ctx, tx, userID, threadID, msg.ID, in.AttachmentIDs); err != nil {
		return nil, nil, false, err
	}
	lastRead := "t.last_read_at"
	if in.MarkRead {
		// The user's own reply implies they have read everything before it.
		lastRead = "GREATEST(t.last_read_at, m.created_at)"
	}
	// A plain agent message leaves an archived thread archived; the user's
	// own reply from the app and anything marked important bring it back. A
	// "user" message an agent mirrors from its terminal is not the user
	// coming back to the thread.
	archived := "t.archived_at"
	if (in.Sender == SenderUser && in.Origin == OriginSession) || in.Importance == ImportanceImportant {
		archived = "NULL"
	}
	// An agent message is the outcome its status line announced, so the
	// status goes with it unless the post carries the next one; the user's
	// reply leaves it alone.
	var activity string
	args := []any{threadID, userID, msg.CreatedAt, previewFor(in.Body, len(in.AttachmentIDs)), in.Sender}
	switch {
	case in.Activity != nil:
		args = append(args, in.Activity.Text, in.Activity.Kind, in.Activity.TTL, in.Activity.Seq)
		activity = activitySet("$6", "$7", "$8", "$9")
	case in.Sender == SenderUser:
		activity = "activity_text = t.activity_text"
	default:
		args = append(args, in.ClearSeq)
		activity = activityCleared("$6")
	}
	thread, err = scanThread(tx.QueryRow(ctx, `WITH m AS (SELECT $3::timestamptz AS created_at)
		UPDATE threads t SET preview = $4, preview_sender = $5, last_activity_at = m.created_at, updated_at = now(),
			last_read_at = `+lastRead+`,
			archived_at = `+archived+`, `+activity+`
		FROM m WHERE t.id = $1 AND t.user_id = $2 RETURNING `+threadColumns, args...))
	if err != nil {
		return nil, nil, false, err
	}
	return msg, thread, true, nil
}

// previewFor renders the inbox preview, noting attachments when present.
func previewFor(body string, attachments int) string {
	p := Preview(body)
	if attachments == 0 {
		return p
	}
	label := "📎 Attachment"
	if attachments > 1 {
		label = "📎 " + itoa(attachments) + " attachments"
	}
	if p == "" {
		return label
	}
	return label + " · " + p
}

// hydrateAttachments fills Attachments on each message.
func (s *Store) hydrateAttachments(ctx context.Context, userID uuid.UUID, msgs []*Message) error {
	ids := make([]uuid.UUID, 0, len(msgs))
	for _, m := range msgs {
		m.Attachments = []*Attachment{}
		ids = append(ids, m.ID)
	}
	byMessage, err := s.ListAttachmentsForMessages(ctx, userID, ids)
	if err != nil {
		return err
	}
	for _, m := range msgs {
		if list, ok := byMessage[m.ID]; ok {
			m.Attachments = list
		}
	}
	return nil
}

// FindMessageByClientKey returns the message an idempotency key already
// created in the thread, or ErrNotFound.
func (s *Store) FindMessageByClientKey(ctx context.Context, userID, threadID uuid.UUID, key string) (*Message, error) {
	m, err := scanMessage(s.pool.QueryRow(ctx, "SELECT "+messageColumns+" FROM messages m WHERE m.thread_id = $1 AND m.user_id = $2 AND m.client_key = $3", threadID, userID, key))
	if err != nil {
		return nil, err
	}
	if err := s.hydrateAttachments(ctx, userID, []*Message{m}); err != nil {
		return nil, err
	}
	return m, nil
}

// DeleteMessage turns a message into a tombstone (body and meta gone, id
// and position kept so pagination anchors still resolve), removes its
// attachment rows, and repairs the thread's preview from what remains. The
// removed attachments are returned so the caller can delete their objects.
func (s *Store) DeleteMessage(ctx context.Context, userID, id uuid.UUID) (msg *Message, thread *Thread, attachments []*Attachment, err error) {
	err = s.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		msg, err = scanMessage(tx.QueryRow(ctx, "SELECT "+messageColumns+" FROM messages m WHERE m.id = $1 AND m.user_id = $2 AND m.deleted_at IS NULL", id, userID))
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, "DELETE FROM attachments a WHERE a.message_id = $1 RETURNING "+attachmentColumns, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			a, err := scanAttachment(rows)
			if err != nil {
				rows.Close()
				return err
			}
			attachments = append(attachments, a)
		}
		rows.Close()
		if _, err := tx.Exec(ctx, "UPDATE messages SET deleted_at = now(), body = '', meta = '{}'::jsonb, format = 'text', client_key = NULL WHERE id = $1", id); err != nil {
			return err
		}
		// The preview follows the newest remaining message or question.
		var text, sender string
		var attachmentCount int
		err = tx.QueryRow(ctx, `SELECT text, sender, n FROM (
				SELECT m.body AS text, m.sender, m.created_at, (SELECT count(*) FROM attachments a WHERE a.message_id = m.id)::int AS n
					FROM messages m WHERE m.thread_id = $1 AND m.deleted_at IS NULL
				UNION ALL
				SELECT q.prompt, 'question', q.created_at, 0 FROM questions q WHERE q.thread_id = $1
			) latest ORDER BY created_at DESC LIMIT 1`, msg.ThreadID).Scan(&text, &sender, &attachmentCount)
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
		preview := ""
		if err == nil {
			preview = previewFor(text, attachmentCount)
		}
		thread, err = scanThread(tx.QueryRow(ctx, `UPDATE threads t SET preview = $3, preview_sender = $4, updated_at = now()
			WHERE t.id = $1 AND t.user_id = $2 RETURNING `+threadColumns, msg.ThreadID, userID, preview, sender))
		return err
	})
	if err != nil {
		return nil, nil, nil, err
	}
	msg.Deleted = true
	return msg, thread, attachments, nil
}

// GetMessage loads a message the user owns.
func (s *Store) GetMessage(ctx context.Context, userID, id uuid.UUID) (*Message, error) {
	m, err := scanMessage(s.pool.QueryRow(ctx, "SELECT "+messageColumns+" FROM messages m WHERE m.id = $1 AND m.user_id = $2 AND m.deleted_at IS NULL", id, userID))
	if err != nil {
		return nil, err
	}
	if err := s.hydrateAttachments(ctx, userID, []*Message{m}); err != nil {
		return nil, err
	}
	return m, nil
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
// exist before the returned window (or, with After, whether more newer ones
// do). anchorUnknown is set when After names a message the thread does not
// hold (a thread deleted and recreated under the same external id, say): the
// page then starts from the thread's first message rather than matching
// nothing forever.
func (s *Store) ListMessages(ctx context.Context, userID, threadID uuid.UUID, p MessagePage) (msgs []*Message, hasMore, anchorUnknown bool, err error) {
	if p.Limit <= 0 || p.Limit > 500 {
		p.Limit = 100
	}
	var anchorAt time.Time
	if p.After != nil {
		err = translate(s.pool.QueryRow(ctx, "SELECT created_at FROM messages WHERE id = $1 AND thread_id = $2 AND user_id = $3", *p.After, threadID, userID).Scan(&anchorAt))
		switch {
		case err == ErrNotFound:
			anchorUnknown = true
		case err != nil:
			return nil, false, false, err
		}
	}
	args := []any{threadID, userID, p.Limit + 1}
	where := "m.thread_id = $1 AND m.user_id = $2"
	// A catch-up page (after=<id>, no sender filter) includes tombstones so
	// a client that held the message learns it is gone; everything else
	// shows live messages only.
	if p.After == nil || anchorUnknown || p.Sender != "" {
		where += " AND m.deleted_at IS NULL"
	}
	order := "ORDER BY m.created_at DESC, m.id DESC"
	reverse := true
	if p.After != nil {
		if !anchorUnknown {
			args = append(args, anchorAt, *p.After)
			// Besides what came after the anchor, the page carries the
			// tombstone of any older message deleted since the anchor existed,
			// which is exactly the set a client that holds the anchor may hold.
			where += " AND ((m.created_at, m.id) > ($4::timestamptz, $5::uuid) OR (m.deleted_at IS NOT NULL AND m.deleted_at > $4::timestamptz))"
		}
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
		return nil, false, false, err
	}
	defer rows.Close()
	out := []*Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, false, false, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, false, err
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
	if err := s.hydrateAttachments(ctx, userID, out); err != nil {
		return nil, false, false, err
	}
	return out, hasMore, anchorUnknown, nil
}
