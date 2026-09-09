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
	(SELECT count(*) FROM messages m WHERE m.thread_id = t.id AND m.sender <> 'user' AND m.deleted_at IS NULL AND m.created_at > t.last_read_at)::int,
	(SELECT count(*) FROM questions q WHERE q.thread_id = t.id AND q.status = 'pending')::int,
	t.activity_text, t.activity_kind, t.activity_at, t.activity_since, t.activity_expires_at,
	(t.activity_expires_at IS NOT NULL AND t.activity_expires_at > now())`

// activityClearedSet is the SET fragment that drops a thread's status.
const activityClearedSet = "activity_text = '', activity_kind = '', activity_at = NULL, activity_since = NULL, activity_expires_at = NULL"

// activityCleared drops a thread's status and records the clear's position:
// seq (a parameter placeholder) raises the watermark a sequenced write must
// exceed, and the clear's time bounds how long that watermark holds. Used by
// explicit clears and by agent posts, since a message is the outcome its
// status announced.
func activityCleared(seq string) string {
	return activityClearedSet + `, activity_cleared_seq = GREATEST(t.activity_cleared_seq, ` + seq + `::bigint),
		activity_cleared_at = CASE WHEN ` + seq + `::bigint > t.activity_cleared_seq THEN now() ELSE t.activity_cleared_at END`
}

// clearedSeqWindow is how long a clear's watermark rejects lower-sequenced
// writes. A straggler is seconds late, never minutes; after this a clear from
// a faster clock (or a wrong unit) can no longer pin the status line shut.
const clearedSeqWindow = "interval '10 minutes'"

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
		args = append(args, "%"+escapeLike(q)+"%")
		n := itoa(len(args))
		where += " AND (t.title ILIKE $" + n + " ESCAPE '\\' OR t.agent ILIKE $" + n + " ESCAPE '\\' OR t.preview ILIKE $" + n + " ESCAPE '\\' OR t.external_id ILIKE $" + n + " ESCAPE '\\')"
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

// ActivityInput describes a status line to set.
type ActivityInput struct {
	Text string
	Kind string
	TTL  time.Duration
	// Seq orders writes from callers that fire them concurrently: a write
	// whose Seq is not above the live status's is ignored. Zero means unordered.
	Seq int64
}

// activitySet is the SET fragment that records a status line; it keeps
// activity_since while the previous status is still live so the app can say
// how long the agent has been busy. Waiting is idle, not busy, so a
// transition into or out of 'waiting' starts a fresh busy period.
// Parameters: text, kind, ttl, seq.
func activitySet(text, kind, ttl, seq string) string {
	return `activity_text = ` + text + `, activity_kind = ` + kind + `, activity_at = now(),
		activity_since = CASE WHEN t.activity_expires_at IS NOT NULL AND t.activity_expires_at > now() AND t.activity_since IS NOT NULL AND t.activity_kind <> 'waiting' AND ` + kind + ` <> 'waiting'
			THEN t.activity_since ELSE now() END,
		activity_expires_at = now() + ` + ttl + `, activity_seq = GREATEST(t.activity_seq, ` + seq + `::bigint)`
}

// SetThreadActivity records what the agent is doing. It returns applied=false
// when a newer status (by Seq) is already live, in which case the returned
// thread carries that status.
func (s *Store) SetThreadActivity(ctx context.Context, userID, id uuid.UUID, in ActivityInput) (thread *Thread, applied bool, err error) {
	// A write is ignored when a newer status is live, or when a clear with a
	// higher sequence happened recently (a straggler must not resurrect it).
	thread, err = scanThread(s.pool.QueryRow(ctx, `UPDATE threads t SET `+activitySet("$3", "$4", "$5", "$6")+`
		WHERE t.id = $1 AND t.user_id = $2
			AND ($6::bigint = 0 OR $6::bigint > t.activity_cleared_seq OR t.activity_cleared_at IS NULL OR t.activity_cleared_at < now() - `+clearedSeqWindow+`)
			AND ($6::bigint = 0 OR t.activity_seq < $6::bigint OR t.activity_expires_at IS NULL OR t.activity_expires_at <= now())
		RETURNING `+threadColumns, id, userID, in.Text, in.Kind, in.TTL, in.Seq))
	if err == nil {
		return thread, true, nil
	}
	if err != ErrNotFound {
		return nil, false, err
	}
	thread, err = s.GetThread(ctx, userID, id)
	return thread, false, err
}

// ClearThreadActivity drops the status line. seq, when non-zero, records
// the clear's position so a later-arriving write with a lower seq is ignored.
func (s *Store) ClearThreadActivity(ctx context.Context, userID, id uuid.UUID, seq int64) (*Thread, error) {
	return scanThread(s.pool.QueryRow(ctx, "UPDATE threads t SET "+activityCleared("$3")+" WHERE t.id = $1 AND t.user_id = $2 RETURNING "+threadColumns, id, userID, seq))
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

// DeleteThreadIfEmpty removes an auto-created thread that never gained
// content (no messages, attachments, questions or artifacts). It reports
// whether a row was removed; a thread that gained content in the meantime
// is left alone, so a concurrent success is never undone by a failed one.
func (s *Store) DeleteThreadIfEmpty(ctx context.Context, userID, id uuid.UUID) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM threads WHERE id = $1 AND user_id = $2
		AND NOT EXISTS (SELECT 1 FROM messages WHERE thread_id = threads.id)
		AND NOT EXISTS (SELECT 1 FROM attachments WHERE thread_id = threads.id)
		AND NOT EXISTS (SELECT 1 FROM questions WHERE thread_id = threads.id)
		AND NOT EXISTS (SELECT 1 FROM artifacts WHERE thread_id = threads.id)`, id, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// Counts summarises the inbox for badges.
type Counts struct {
	// PendingQuestions counts every question waiting for an answer.
	PendingQuestions int `json:"pending_questions"`
	// UnreadThreads counts active, unmuted threads with unread agent messages.
	UnreadThreads int `json:"unread_threads"`
	// Attention counts active, unmuted threads that need the user: a pending
	// question or unread agent messages. It is what the app badge shows.
	Attention int `json:"attention"`
}

// GetCounts computes badge counts for the user.
func (s *Store) GetCounts(ctx context.Context, userID uuid.UUID) (Counts, error) {
	var c Counts
	err := s.pool.QueryRow(ctx, `WITH unread AS (
			SELECT t.id FROM threads t WHERE t.user_id = $1 AND t.archived_at IS NULL AND NOT t.muted AND EXISTS (
				SELECT 1 FROM messages m WHERE m.thread_id = t.id AND m.sender <> 'user' AND m.deleted_at IS NULL AND m.created_at > t.last_read_at)
		), asked AS (
			SELECT DISTINCT q.thread_id AS id FROM questions q JOIN threads t ON t.id = q.thread_id
			WHERE q.user_id = $1 AND q.status = 'pending' AND t.archived_at IS NULL AND NOT t.muted
		)
		SELECT
			(SELECT count(*) FROM questions WHERE user_id = $1 AND status = 'pending')::int,
			(SELECT count(*) FROM unread)::int,
			(SELECT count(*) FROM (SELECT id FROM unread UNION SELECT id FROM asked) x)::int`, userID).
		Scan(&c.PendingQuestions, &c.UnreadThreads, &c.Attention)
	return c, err
}

// escapeLike makes user text literal inside an ILIKE pattern.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Now is the database's clock, for anchors compared against created_at.
func (s *Store) Now(ctx context.Context) (time.Time, error) {
	var t time.Time
	err := s.pool.QueryRow(ctx, "SELECT now()").Scan(&t)
	return t.UTC(), err
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
