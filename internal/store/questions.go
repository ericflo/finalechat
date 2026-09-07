package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Question statuses.
const (
	QuestionPending   = "pending"
	QuestionAnswered  = "answered"
	QuestionCancelled = "cancelled"
	QuestionExpired   = "expired"
	// QuestionDismissed means the user declined to answer.
	QuestionDismissed = "dismissed"
)

// QuestionOption is one choice offered to the user.
type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// Answer is the user's response.
type Answer struct {
	// Selected holds the labels of the chosen options, in the order chosen.
	Selected []string `json:"selected"`
	// Text is the free-form response, if any.
	Text string `json:"text,omitempty"`
}

// Question is a request for input from the user.
type Question struct {
	ID            uuid.UUID        `json:"id"`
	ThreadID      uuid.UUID        `json:"thread_id"`
	UserID        uuid.UUID        `json:"-"`
	Prompt        string           `json:"prompt"`
	Options       []QuestionOption `json:"options"`
	AllowFreeform bool             `json:"allow_freeform"`
	MultiSelect   bool             `json:"multi_select"`
	Status        string           `json:"status"`
	Answer        *Answer          `json:"answer"`
	Meta          JSON             `json:"meta"`
	CreatedAt     time.Time        `json:"created_at"`
	AnsweredAt    *time.Time       `json:"answered_at"`
	ExpiresAt     *time.Time       `json:"expires_at"`
}

const questionColumns = "q.id, q.thread_id, q.user_id, q.prompt, q.options, q.allow_freeform, q.multi_select, q.status, q.answer, q.meta, q.created_at, q.answered_at, q.expires_at"

func scanQuestion(row pgx.Row) (*Question, error) {
	var q Question
	var options, answer, meta []byte
	if err := row.Scan(&q.ID, &q.ThreadID, &q.UserID, &q.Prompt, &options, &q.AllowFreeform, &q.MultiSelect, &q.Status, &answer, &meta, &q.CreatedAt, &q.AnsweredAt, &q.ExpiresAt); err != nil {
		return nil, translate(err)
	}
	q.Options = []QuestionOption{}
	if len(options) > 0 {
		_ = json.Unmarshal(options, &q.Options)
	}
	if len(answer) > 0 {
		var a Answer
		if err := json.Unmarshal(answer, &a); err == nil {
			if a.Selected == nil {
				a.Selected = []string{}
			}
			q.Answer = &a
		}
	}
	scanJSON(meta, &q.Meta)
	return &q, nil
}

// QuestionInput describes a question to ask.
type QuestionInput struct {
	Prompt        string
	Options       []QuestionOption
	AllowFreeform bool
	MultiSelect   bool
	ExpiresAt     *time.Time
	Meta          JSON
	// ClientKey makes the create idempotent within the thread.
	ClientKey string
	// Activity, when set, becomes the thread's status line (for an agent
	// that keeps working while it waits); otherwise the status is cleared.
	Activity *ActivityInput
}

// CreateQuestion records a question and bumps the thread. created is false
// when in.ClientKey matched an existing question, which is returned instead.
func (s *Store) CreateQuestion(ctx context.Context, userID, threadID uuid.UUID, in QuestionInput) (q *Question, thread *Thread, created bool, err error) {
	options, _ := json.Marshal(in.Options)
	if in.Options == nil {
		options = []byte("[]")
	}
	var clientKey *string
	if in.ClientKey != "" {
		clientKey = &in.ClientKey
	}
	err = s.withTx(ctx, func(tx pgx.Tx) error {
		if clientKey != nil {
			existing, err := scanQuestion(tx.QueryRow(ctx, "SELECT "+questionColumns+" FROM questions q WHERE q.thread_id = $1 AND q.user_id = $2 AND q.client_key = $3", threadID, userID, in.ClientKey))
			if err == nil {
				q = existing
				thread, err = scanThread(tx.QueryRow(ctx, "SELECT "+threadColumns+" FROM threads t WHERE t.id = $1 AND t.user_id = $2", threadID, userID))
				return err
			}
			if err != ErrNotFound {
				return err
			}
		}
		var err error
		q, err = scanQuestion(tx.QueryRow(ctx, `WITH q AS (
				INSERT INTO questions (id, thread_id, user_id, prompt, options, allow_freeform, multi_select, expires_at, meta, client_key)
				SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
				WHERE EXISTS (SELECT 1 FROM threads WHERE id = $2 AND user_id = $3)
				RETURNING *
			) SELECT `+questionColumns+` FROM q`,
			NewID(), threadID, userID, in.Prompt, options, in.AllowFreeform, in.MultiSelect, in.ExpiresAt, in.Meta.value(), clientKey))
		if err != nil {
			return err
		}
		created = true
		activity := activityCleared
		args := []any{threadID, userID, Preview(in.Prompt), q.CreatedAt}
		if in.Activity != nil {
			args = append(args, in.Activity.Text, in.Activity.Kind, in.Activity.TTL, in.Activity.Seq)
			activity = activitySet("$5", "$6", "$7", "$8")
		}
		thread, err = scanThread(tx.QueryRow(ctx, `UPDATE threads t SET preview = $3, preview_sender = 'question', last_activity_at = $4, updated_at = now(), archived_at = NULL, `+activity+`
			WHERE t.id = $1 AND t.user_id = $2 RETURNING `+threadColumns, args...))
		return err
	})
	if err != nil {
		return nil, nil, false, err
	}
	return q, thread, created, nil
}

// GetQuestion loads a question the user owns.
func (s *Store) GetQuestion(ctx context.Context, userID, id uuid.UUID) (*Question, error) {
	return scanQuestion(s.pool.QueryRow(ctx, "SELECT "+questionColumns+" FROM questions q WHERE q.id = $1 AND q.user_id = $2", id, userID))
}

// ListQuestions lists questions, optionally filtered by thread and status,
// newest first.
func (s *Store) ListQuestions(ctx context.Context, userID uuid.UUID, threadID *uuid.UUID, status string, limit int) ([]*Question, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{userID, limit}
	where := "q.user_id = $1"
	if threadID != nil {
		args = append(args, *threadID)
		where += " AND q.thread_id = $" + itoa(len(args))
	}
	if status != "" {
		args = append(args, status)
		where += " AND q.status = $" + itoa(len(args))
	}
	rows, err := s.pool.Query(ctx, "SELECT "+questionColumns+" FROM questions q WHERE "+where+" ORDER BY q.created_at DESC, q.id DESC LIMIT $2", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Question{}
	for rows.Next() {
		q, err := scanQuestion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// ListThreadQuestions returns the newest 500 questions in a thread in
// ascending order so the app can interleave them with messages.
func (s *Store) ListThreadQuestions(ctx context.Context, userID, threadID uuid.UUID) ([]*Question, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+questionColumns+" FROM questions q WHERE q.user_id = $1 AND q.thread_id = $2 ORDER BY q.created_at DESC, q.id DESC LIMIT 500", userID, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Question{}
	for rows.Next() {
		q, err := scanQuestion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// AnswerQuestion records the user's answer and, in the same transaction, the
// transcript message that carries it (so agents polling messages see it) and
// the thread bump. It fails with ErrInvalidState if the question is no longer
// pending. The thread's read marker is advanced because answering implies the
// user has seen the thread.
func (s *Store) AnswerQuestion(ctx context.Context, userID, id uuid.UUID, a Answer, origin string) (*Question, *Message, *Thread, error) {
	if a.Selected == nil {
		a.Selected = []string{}
	}
	a.Text = strings.TrimSpace(a.Text)
	raw, _ := json.Marshal(a)
	var q *Question
	var msg *Message
	var thread *Thread
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		q, err = scanQuestion(tx.QueryRow(ctx, `UPDATE questions q SET status = 'answered', answer = $3, answered_at = now()
			WHERE q.id = $1 AND q.user_id = $2 AND q.status = 'pending' RETURNING `+questionColumns, id, userID, raw))
		if err == ErrNotFound {
			// Distinguish missing from already resolved.
			var status string
			if scanErr := tx.QueryRow(ctx, "SELECT status FROM questions WHERE id = $1 AND user_id = $2", id, userID).Scan(&status); scanErr == nil {
				return ErrInvalidState
			}
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var body strings.Builder
		if len(a.Selected) > 0 {
			body.WriteString(strings.Join(a.Selected, ", "))
		}
		if a.Text != "" {
			if body.Len() > 0 {
				body.WriteString(" — ")
			}
			body.WriteString(a.Text)
		}
		msg, thread, _, err = createMessageTx(ctx, tx, userID, q.ThreadID, MessageInput{
			Sender: SenderUser, Body: body.String(), Format: "text", Importance: ImportanceNormal, Origin: origin, MarkRead: true,
			Meta: JSON{"question_id": q.ID.String(), "kind": "answer"},
		})
		return err
	})
	if err != nil {
		return nil, nil, nil, err
	}
	if err := s.hydrateAttachments(ctx, userID, []*Message{msg}); err != nil {
		return nil, nil, nil, err
	}
	return q, msg, thread, nil
}

// CancelQuestion withdraws a pending question (the agent no longer needs an
// answer). DismissQuestion is the user's counterpart.
func (s *Store) CancelQuestion(ctx context.Context, userID, id uuid.UUID) (*Question, error) {
	return s.resolveQuestion(ctx, userID, id, QuestionCancelled)
}

// DismissQuestion records that the user declined to answer.
func (s *Store) DismissQuestion(ctx context.Context, userID, id uuid.UUID) (*Question, error) {
	return s.resolveQuestion(ctx, userID, id, QuestionDismissed)
}

func (s *Store) resolveQuestion(ctx context.Context, userID, id uuid.UUID, status string) (*Question, error) {
	q, err := scanQuestion(s.pool.QueryRow(ctx, `UPDATE questions q SET status = $3, answered_at = now()
		WHERE q.id = $1 AND q.user_id = $2 AND q.status = 'pending' RETURNING `+questionColumns, id, userID, status))
	if err == ErrNotFound {
		var current string
		if scanErr := s.pool.QueryRow(ctx, "SELECT status FROM questions WHERE id = $1 AND user_id = $2", id, userID).Scan(&current); scanErr == nil {
			return nil, ErrInvalidState
		}
	}
	return q, err
}

// ExpireQuestions marks overdue pending questions as expired and returns them.
func (s *Store) ExpireQuestions(ctx context.Context) ([]*Question, error) {
	rows, err := s.pool.Query(ctx, `UPDATE questions q SET status = 'expired', answered_at = now()
		WHERE q.status = 'pending' AND q.expires_at IS NOT NULL AND q.expires_at <= now() RETURNING `+questionColumns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Question{}
	for rows.Next() {
		q, err := scanQuestion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}
