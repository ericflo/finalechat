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
}

// CreateQuestion records a question and bumps the thread.
func (s *Store) CreateQuestion(ctx context.Context, userID, threadID uuid.UUID, in QuestionInput) (*Question, *Thread, error) {
	options, _ := json.Marshal(in.Options)
	if in.Options == nil {
		options = []byte("[]")
	}
	var q *Question
	var thread *Thread
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		q, err = scanQuestion(tx.QueryRow(ctx, `WITH q AS (
				INSERT INTO questions (id, thread_id, user_id, prompt, options, allow_freeform, multi_select, expires_at, meta)
				SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9
				WHERE EXISTS (SELECT 1 FROM threads WHERE id = $2 AND user_id = $3)
				RETURNING *
			) SELECT `+questionColumns+` FROM q`,
			NewID(), threadID, userID, in.Prompt, options, in.AllowFreeform, in.MultiSelect, in.ExpiresAt, in.Meta.value()))
		if err != nil {
			return err
		}
		thread, err = scanThread(tx.QueryRow(ctx, `UPDATE threads t SET preview = $3, preview_sender = 'question', last_activity_at = $4, updated_at = now(), archived_at = NULL
			WHERE t.id = $1 AND t.user_id = $2 RETURNING `+threadColumns,
			threadID, userID, Preview(in.Prompt), q.CreatedAt))
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return q, thread, nil
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

// ListThreadQuestions returns every question in a thread in ascending order so
// the app can interleave them with messages.
func (s *Store) ListThreadQuestions(ctx context.Context, userID, threadID uuid.UUID) ([]*Question, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+questionColumns+" FROM questions q WHERE q.user_id = $1 AND q.thread_id = $2 ORDER BY q.created_at ASC, q.id ASC LIMIT 500", userID, threadID)
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

// AnswerQuestion records the user's answer. It fails with ErrInvalidState if
// the question is no longer pending. The thread's read marker is advanced
// because answering implies the user has seen the thread.
func (s *Store) AnswerQuestion(ctx context.Context, userID, id uuid.UUID, a Answer) (*Question, error) {
	if a.Selected == nil {
		a.Selected = []string{}
	}
	a.Text = strings.TrimSpace(a.Text)
	raw, _ := json.Marshal(a)
	var q *Question
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
		_, err = tx.Exec(ctx, "UPDATE threads SET last_read_at = GREATEST(last_read_at, now()), updated_at = now() WHERE id = $1", q.ThreadID)
		return err
	})
	return q, err
}

// CancelQuestion withdraws a pending question.
func (s *Store) CancelQuestion(ctx context.Context, userID, id uuid.UUID) (*Question, error) {
	q, err := scanQuestion(s.pool.QueryRow(ctx, `UPDATE questions q SET status = 'cancelled', answered_at = now()
		WHERE q.id = $1 AND q.user_id = $2 AND q.status = 'pending' RETURNING `+questionColumns, id, userID))
	if err == ErrNotFound {
		var status string
		if scanErr := s.pool.QueryRow(ctx, "SELECT status FROM questions WHERE id = $1 AND user_id = $2", id, userID).Scan(&status); scanErr == nil {
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
