package store

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Thread is a conversation between one agent session and the user.
type Thread struct {
	ID               uuid.UUID  `json:"id"`
	UserID           uuid.UUID  `json:"-"`
	ExternalID       *string    `json:"external_id"`
	Title            string     `json:"title"`
	Agent            string     `json:"agent"`
	Meta             JSON       `json:"meta"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	LastActivityAt   time.Time  `json:"last_activity_at"`
	LastReadAt       time.Time  `json:"last_read_at"`
	ArchivedAt       *time.Time `json:"archived_at"`
	Muted            bool       `json:"muted"`
	Preview          string     `json:"preview"`
	PreviewSender    string     `json:"preview_sender"`
	UnreadCount      int        `json:"unread_count"`
	PendingQuestions int        `json:"pending_questions"`
	// Activity is what the agent says it is doing right now, or nil when it
	// has said nothing or the last status has expired.
	Activity *Activity `json:"activity"`
}

// Activity is an agent's ephemeral status line for a thread.
type Activity struct {
	Text string `json:"text"`
	// Kind is one of thinking, working, typing, waiting or tool.
	Kind string `json:"kind"`
	// At is when the status was last set or refreshed.
	At time.Time `json:"at"`
	// Since is when the agent became continuously busy: it survives refreshes
	// and resets only after the status lapsed or was cleared.
	Since time.Time `json:"since"`
	// ExpiresAt is when the status lapses unless refreshed.
	ExpiresAt time.Time `json:"expires_at"`
}

// Activity kinds.
const (
	ActivityThinking = "thinking"
	ActivityWorking  = "working"
	ActivityTyping   = "typing"
	ActivityWaiting  = "waiting"
	ActivityTool     = "tool"
)

// ActivityKinds lists the accepted kinds.
var ActivityKinds = []string{ActivityThinking, ActivityWorking, ActivityTyping, ActivityWaiting, ActivityTool}

// MaxActivityTextRunes bounds a status line.
const MaxActivityTextRunes = 200

// Archived reports whether the thread is archived.
func (t *Thread) Archived() bool { return t.ArchivedAt != nil }

const threadColumns = `t.id, t.user_id, t.external_id, t.title, t.agent, t.meta, t.created_at, t.updated_at,
	t.last_activity_at, t.last_read_at, t.archived_at, t.muted, t.preview, t.preview_sender,
	(SELECT count(*) FROM messages m WHERE m.thread_id = t.id AND m.sender <> 'user' AND m.created_at > t.last_read_at)::int,
	(SELECT count(*) FROM questions q WHERE q.thread_id = t.id AND q.status = 'pending')::int,
	t.activity_text, t.activity_kind, t.activity_at, t.activity_since, t.activity_expires_at,
	(t.activity_expires_at IS NOT NULL AND t.activity_expires_at > now())`

// activityCleared is the SET fragment that drops a thread's status; used when
// the agent posts, since the message is the outcome the status announced.
const activityCleared = "activity_text = '', activity_kind = '', activity_at = NULL, activity_since = NULL, activity_expires_at = NULL"

func scanThread(row pgx.Row) (*Thread, error) {
	var t Thread
	var meta []byte
	var a Activity
	var at, since, expires *time.Time
	var live bool
	if err := row.Scan(&t.ID, &t.UserID, &t.ExternalID, &t.Title, &t.Agent, &meta, &t.CreatedAt, &t.UpdatedAt,
		&t.LastActivityAt, &t.LastReadAt, &t.ArchivedAt, &t.Muted, &t.Preview, &t.PreviewSender,
		&t.UnreadCount, &t.PendingQuestions,
		&a.Text, &a.Kind, &at, &since, &expires, &live); err != nil {
		return nil, translate(err)
	}
	scanJSON(meta, &t.Meta)
	if live && at != nil && expires != nil {
		a.At, a.ExpiresAt = *at, *expires
		a.Since = a.At
		if since != nil {
			a.Since = *since
		}
		t.Activity = &a
	}
	return &t, nil
}

// ThreadInput describes a thread to create.
type ThreadInput struct {
	ExternalID string
	Title      string
	Agent      string
	Meta       JSON
}

// CreateThread inserts a thread. When ExternalID is set and a thread with that
// external id already exists for the user, the existing thread is returned and
// created is false; blank title/agent fields on the existing thread are filled
// from the input so a hook that learns the title later can still set it.
func (s *Store) CreateThread(ctx context.Context, userID uuid.UUID, in ThreadInput) (thread *Thread, created bool, err error) {
	in.Title = truncate(strings.TrimSpace(in.Title), 300)
	in.Agent = truncate(strings.TrimSpace(in.Agent), 120)
	in.ExternalID = truncate(strings.TrimSpace(in.ExternalID), 300)
	var external *string
	if in.ExternalID != "" {
		external = &in.ExternalID
	}
	err = s.withTx(ctx, func(tx pgx.Tx) error {
		if external != nil {
			existing, err := scanThread(tx.QueryRow(ctx, `UPDATE threads t SET
					title = CASE WHEN t.title = '' THEN $3 ELSE t.title END,
					agent = CASE WHEN t.agent = '' THEN $4 ELSE t.agent END,
					meta = t.meta || $5::jsonb,
					updated_at = now()
				WHERE t.user_id = $1 AND t.external_id = $2
				RETURNING `+threadColumns, userID, *external, in.Title, in.Agent, in.Meta.value()))
			if err == nil {
				thread = existing
				return nil
			}
			if err != ErrNotFound {
				return err
			}
		}
		inserted, err := scanThread(tx.QueryRow(ctx, `WITH t AS (
				INSERT INTO threads (id, user_id, external_id, title, agent, meta)
				VALUES ($1, $2, $3, $4, $5, $6) RETURNING *
			) SELECT `+threadColumns+` FROM t`,
			NewID(), userID, external, in.Title, in.Agent, in.Meta.value()))
		if err != nil {
			return err
		}
		thread = inserted
		created = true
		return nil
	})
	if err == ErrConflict && external != nil {
		// Lost a race with a concurrent create for the same external id.
		thread, err = s.GetThreadByExternalID(ctx, userID, *external)
		return thread, false, err
	}
	return thread, created, err
}

// GetThread loads a thread the user owns.
func (s *Store) GetThread(ctx context.Context, userID, id uuid.UUID) (*Thread, error) {
	return scanThread(s.pool.QueryRow(ctx, "SELECT "+threadColumns+" FROM threads t WHERE t.id = $1 AND t.user_id = $2", id, userID))
}

// GetThreadByExternalID loads a thread by its agent-supplied id.
func (s *Store) GetThreadByExternalID(ctx context.Context, userID uuid.UUID, externalID string) (*Thread, error) {
	return scanThread(s.pool.QueryRow(ctx, "SELECT "+threadColumns+" FROM threads t WHERE t.user_id = $1 AND t.external_id = $2", userID, externalID))
}

// ThreadFilter narrows ListThreads.
type ThreadFilter struct {
	// Archived selects archived (true) or active (false) threads.
	Archived bool
	// Limit caps the page size.
	Limit int
	// Cursor continues a previous page.
	Cursor *Cursor
	// Query is a case-insensitive substring match on title and agent.
	Query string
}

// ListThreads pages threads by most recent activity.
func (s *Store) ListThreads(ctx context.Context, userID uuid.UUID, f ThreadFilter) ([]*Thread, *Cursor, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	args := []any{userID, f.Limit + 1}
	where := "t.user_id = $1 AND (t.archived_at IS NULL) = $3"
	args = append(args, !f.Archived)
	if f.Cursor != nil {
		args = append(args, f.Cursor.At, f.Cursor.ID)
		where += " AND (t.last_activity_at, t.id) < ($4, $5)"
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		args = append(args, "%"+q+"%")
		where += " AND (t.title ILIKE $" + itoa(len(args)) + " OR t.agent ILIKE $" + itoa(len(args)) + " OR t.preview ILIKE $" + itoa(len(args)) + ")"
	}
	rows, err := s.pool.Query(ctx, "SELECT "+threadColumns+" FROM threads t WHERE "+where+" ORDER BY t.last_activity_at DESC, t.id DESC LIMIT $2", args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := []*Thread{}
	for rows.Next() {
		t, err := scanThread(rows)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var next *Cursor
	if len(out) > f.Limit {
		out = out[:f.Limit]
		last := out[len(out)-1]
		next = &Cursor{At: last.LastActivityAt, ID: last.ID}
	}
	return out, next, nil
}

// ThreadPatch updates selected fields; nil pointers are left untouched.
type ThreadPatch struct {
	Title    *string
	Agent    *string
	Archived *bool
	Muted    *bool
	Meta     JSON
}

// UpdateThread applies a patch.
func (s *Store) UpdateThread(ctx context.Context, userID, id uuid.UUID, p ThreadPatch) (*Thread, error) {
	sets := []string{"updated_at = now()"}
	args := []any{id, userID}
	add := func(expr string, v any) {
		args = append(args, v)
		sets = append(sets, strings.Replace(expr, "?", "$"+itoa(len(args)), 1))
	}
	if p.Title != nil {
		add("title = ?", truncate(strings.TrimSpace(*p.Title), 300))
	}
	if p.Agent != nil {
		add("agent = ?", truncate(strings.TrimSpace(*p.Agent), 120))
	}
	if p.Archived != nil {
		if *p.Archived {
			sets = append(sets, "archived_at = COALESCE(archived_at, now())")
		} else {
			sets = append(sets, "archived_at = NULL")
		}
	}
	if p.Muted != nil {
		add("muted = ?", *p.Muted)
	}
	if p.Meta != nil {
		add("meta = meta || ?::jsonb", p.Meta.value())
	}
	sql := "UPDATE threads t SET " + strings.Join(sets, ", ") + " WHERE t.id = $1 AND t.user_id = $2 RETURNING " + threadColumns
	return scanThread(s.pool.QueryRow(ctx, sql, args...))
}

// SetThreadActivity records what the agent is doing. A status set while the
// previous one is still live keeps its start time, so the app can say how
// long the agent has been busy.
func (s *Store) SetThreadActivity(ctx context.Context, userID, id uuid.UUID, text, kind string, ttl time.Duration) (*Thread, error) {
	return scanThread(s.pool.QueryRow(ctx, `UPDATE threads t SET
			activity_text = $3, activity_kind = $4, activity_at = now(),
			activity_since = CASE WHEN t.activity_expires_at IS NOT NULL AND t.activity_expires_at > now() AND t.activity_since IS NOT NULL
				THEN t.activity_since ELSE now() END,
			activity_expires_at = now() + $5
		WHERE t.id = $1 AND t.user_id = $2 RETURNING `+threadColumns, id, userID, text, kind, ttl))
}

// ClearThreadActivity drops the status line.
func (s *Store) ClearThreadActivity(ctx context.Context, userID, id uuid.UUID) (*Thread, error) {
	return scanThread(s.pool.QueryRow(ctx, "UPDATE threads t SET "+activityCleared+" WHERE t.id = $1 AND t.user_id = $2 RETURNING "+threadColumns, id, userID))
}

// MarkThreadRead records that the user has seen everything up to now.
func (s *Store) MarkThreadRead(ctx context.Context, userID, id uuid.UUID) (*Thread, error) {
	return scanThread(s.pool.QueryRow(ctx, "UPDATE threads t SET last_read_at = now() WHERE t.id = $1 AND t.user_id = $2 RETURNING "+threadColumns, id, userID))
}

// DeleteThread removes a thread and everything in it.
func (s *Store) DeleteThread(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM threads WHERE id = $1 AND user_id = $2", id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Counts summarises the inbox for badges.
type Counts struct {
	PendingQuestions int `json:"pending_questions"`
	UnreadThreads    int `json:"unread_threads"`
}

// GetCounts computes badge counts for the user.
func (s *Store) GetCounts(ctx context.Context, userID uuid.UUID) (Counts, error) {
	var c Counts
	err := s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM questions WHERE user_id = $1 AND status = 'pending')::int,
		(SELECT count(*) FROM threads t WHERE t.user_id = $1 AND t.archived_at IS NULL AND EXISTS (
			SELECT 1 FROM messages m WHERE m.thread_id = t.id AND m.sender <> 'user' AND m.created_at > t.last_read_at))::int`, userID).
		Scan(&c.PendingQuestions, &c.UnreadThreads)
	return c, err
}

func itoa(n int) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = digits[n%10]
		n /= 10
	}
	return string(b[i:])
}
