package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MessengerLink ties an account to one Facebook Messenger conversation.
type MessengerLink struct {
	UserID uuid.UUID `json:"-"`
	PSID   string    `json:"-"`
	PageID string    `json:"-"`
	// PinnedThreadID holds replies on one thread; nil follows the thread
	// that spoke last (LastThreadID).
	PinnedThreadID *uuid.UUID `json:"pinned_thread_id"`
	LastThreadID   *uuid.UUID `json:"last_thread_id"`
	MessageAt      time.Time  `json:"-"`
	MessageID      *uuid.UUID `json:"-"`
	QuestionAt     time.Time  `json:"-"`
	QuestionID     *uuid.UUID `json:"-"`
	LastInboundAt  time.Time  `json:"last_inbound_at"`
	WindowClosedAt *time.Time `json:"window_closed_at"`
	ImportantOnly  bool       `json:"important_only"`
	State          JSON       `json:"-"`
	CreatedAt      time.Time  `json:"linked_at"`
}

// Target is the thread a plain reply goes to, or nil.
func (l *MessengerLink) Target() *uuid.UUID {
	if l.PinnedThreadID != nil {
		return l.PinnedThreadID
	}
	return l.LastThreadID
}

const messengerLinkColumns = "user_id, psid, page_id, pinned_thread_id, last_thread_id, message_at, message_id, question_at, question_id, last_inbound_at, window_closed_at, important_only, state, created_at"

func scanMessengerLink(row pgx.Row) (*MessengerLink, error) {
	var l MessengerLink
	var state []byte
	if err := row.Scan(&l.UserID, &l.PSID, &l.PageID, &l.PinnedThreadID, &l.LastThreadID, &l.MessageAt, &l.MessageID, &l.QuestionAt, &l.QuestionID,
		&l.LastInboundAt, &l.WindowClosedAt, &l.ImportantOnly, &state, &l.CreatedAt); err != nil {
		return nil, translate(err)
	}
	scanJSON(state, &l.State)
	return &l, nil
}

// CreateMessengerLinkCode stores a one-time link code (by hash), replacing
// any earlier code of the user.
func (s *Store) CreateMessengerLinkCode(ctx context.Context, userID uuid.UUID, hash []byte, expires time.Time) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "DELETE FROM messenger_link_codes WHERE user_id = $1 OR expires_at < now()", userID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "INSERT INTO messenger_link_codes (code_hash, user_id, expires_at) VALUES ($1, $2, $3)", hash, userID, expires)
		return translate(err)
	})
}

// RedeemMessengerLinkCode consumes a live code and links its account to the
// Messenger person, replacing whatever either side was linked to before.
// Delivery starts from now: nothing older is replayed.
func (s *Store) RedeemMessengerLinkCode(ctx context.Context, hash []byte, psid, pageID string) (*MessengerLink, error) {
	var link *MessengerLink
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var userID uuid.UUID
		if err := translate(tx.QueryRow(ctx, "DELETE FROM messenger_link_codes WHERE code_hash = $1 AND expires_at > now() RETURNING user_id", hash).Scan(&userID)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM messenger_links WHERE user_id = $1 OR psid = $2", userID, psid); err != nil {
			return err
		}
		var err error
		link, err = scanMessengerLink(tx.QueryRow(ctx, `INSERT INTO messenger_links (user_id, psid, page_id, message_at, question_at)
			VALUES ($1, $2, $3, now(), now()) RETURNING `+messengerLinkColumns, userID, psid, pageID))
		return err
	})
	return link, err
}

// GetMessengerLink returns the user's link or ErrNotFound.
func (s *Store) GetMessengerLink(ctx context.Context, userID uuid.UUID) (*MessengerLink, error) {
	return scanMessengerLink(s.pool.QueryRow(ctx, "SELECT "+messengerLinkColumns+" FROM messenger_links WHERE user_id = $1", userID))
}

// GetMessengerLinkByPSID returns the link of a Messenger person.
func (s *Store) GetMessengerLinkByPSID(ctx context.Context, psid string) (*MessengerLink, error) {
	return scanMessengerLink(s.pool.QueryRow(ctx, "SELECT "+messengerLinkColumns+" FROM messenger_links WHERE psid = $1", psid))
}

// ListMessengerLinks returns every link (the relay's work list).
func (s *Store) ListMessengerLinks(ctx context.Context) ([]*MessengerLink, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+messengerLinkColumns+" FROM messenger_links ORDER BY created_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*MessengerLink{}
	for rows.Next() {
		l, err := scanMessengerLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// DeleteMessengerLink unlinks the account.
func (s *Store) DeleteMessengerLink(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM messenger_links WHERE user_id = $1", userID)
	return err
}

// TouchMessengerInbound records a message from the person, which reopens
// Messenger's 24-hour window.
func (s *Store) TouchMessengerInbound(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, "UPDATE messenger_links SET last_inbound_at = now(), window_closed_at = NULL WHERE user_id = $1", userID)
	return err
}

// CloseMessengerWindow records that Messenger refused a send because the
// 24-hour window lapsed.
func (s *Store) CloseMessengerWindow(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, "UPDATE messenger_links SET window_closed_at = now() WHERE user_id = $1", userID)
	return err
}

// SetMessengerPin pins replies to a thread, or follows the last speaker
// when threadID is nil.
func (s *Store) SetMessengerPin(ctx context.Context, userID uuid.UUID, threadID *uuid.UUID) error {
	_, err := s.pool.Exec(ctx, "UPDATE messenger_links SET pinned_thread_id = $2 WHERE user_id = $1", userID, threadID)
	return err
}

// SetMessengerLastThread records the thread that spoke (or was spoken to) last.
func (s *Store) SetMessengerLastThread(ctx context.Context, userID, threadID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, "UPDATE messenger_links SET last_thread_id = $2 WHERE user_id = $1", userID, threadID)
	return err
}

// SetMessengerImportantOnly switches quiet relaying on or off.
func (s *Store) SetMessengerImportantOnly(ctx context.Context, userID uuid.UUID, on bool) error {
	_, err := s.pool.Exec(ctx, "UPDATE messenger_links SET important_only = $2 WHERE user_id = $1", userID, on)
	return err
}

// SetMessengerState replaces the link's conversation state.
func (s *Store) SetMessengerState(ctx context.Context, userID uuid.UUID, state JSON) error {
	_, err := s.pool.Exec(ctx, "UPDATE messenger_links SET state = $2 WHERE user_id = $1", userID, state.value())
	return err
}

// AdvanceMessengerCursors moves the delivery cursors forward (never back).
func (s *Store) AdvanceMessengerCursors(ctx context.Context, userID uuid.UUID, messageAt *time.Time, messageID *uuid.UUID, questionAt *time.Time, questionID *uuid.UUID) error {
	if messageAt != nil {
		if _, err := s.pool.Exec(ctx, `UPDATE messenger_links SET message_at = $2, message_id = $3
			WHERE user_id = $1 AND (message_at, COALESCE(message_id, '00000000-0000-0000-0000-000000000000'::uuid)) < ($2, $3)`, userID, *messageAt, messageID); err != nil {
			return err
		}
	}
	if questionAt != nil {
		if _, err := s.pool.Exec(ctx, `UPDATE messenger_links SET question_at = $2, question_id = $3
			WHERE user_id = $1 AND (question_at, COALESCE(question_id, '00000000-0000-0000-0000-000000000000'::uuid)) < ($2, $3)`, userID, *questionAt, questionID); err != nil {
			return err
		}
	}
	return nil
}

// MessengerHandle returns the thread's short number, allocating the next
// free one on first use.
func (s *Store) MessengerHandle(ctx context.Context, userID, threadID uuid.UUID) (int, error) {
	for attempt := 0; ; attempt++ {
		var h int
		err := translate(s.pool.QueryRow(ctx, `WITH ins AS (
				INSERT INTO messenger_handles (user_id, thread_id, handle)
				SELECT $1, $2, COALESCE((SELECT max(handle) FROM messenger_handles WHERE user_id = $1), 0) + 1
				ON CONFLICT (user_id, thread_id) DO NOTHING
				RETURNING handle
			) SELECT handle FROM ins
			UNION ALL SELECT handle FROM messenger_handles WHERE user_id = $1 AND thread_id = $2
			LIMIT 1`, userID, threadID).Scan(&h))
		if err == ErrConflict && attempt < 5 {
			// Two threads raced for the same next number.
			continue
		}
		return h, err
	}
}

// MessengerThreadByHandle resolves a short number to its thread.
func (s *Store) MessengerThreadByHandle(ctx context.Context, userID uuid.UUID, handle int) (uuid.UUID, error) {
	var id uuid.UUID
	err := translate(s.pool.QueryRow(ctx, "SELECT thread_id FROM messenger_handles WHERE user_id = $1 AND handle = $2", userID, handle).Scan(&id))
	return id, err
}

// RecordMessengerSent remembers what a page message was about.
func (s *Store) RecordMessengerSent(ctx context.Context, userID uuid.UUID, mid string, threadID, questionID *uuid.UUID) error {
	if mid == "" {
		return nil
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO messenger_sent (mid, user_id, thread_id, question_id) VALUES ($1, $2, $3, $4)
		ON CONFLICT (mid) DO NOTHING`, mid, userID, threadID, questionID)
	return err
}

// LookupMessengerSent returns the thread and question a page message was
// about, for a swipe-reply to it.
func (s *Store) LookupMessengerSent(ctx context.Context, userID uuid.UUID, mid string) (threadID, questionID *uuid.UUID, err error) {
	err = translate(s.pool.QueryRow(ctx, "SELECT thread_id, question_id FROM messenger_sent WHERE user_id = $1 AND mid = $2", userID, mid).Scan(&threadID, &questionID))
	return threadID, questionID, err
}

// MessengerInboundSeen reports whether an inbound message was handled.
func (s *Store) MessengerInboundSeen(ctx context.Context, mid string) (bool, error) {
	var seen bool
	err := s.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM messenger_inbound WHERE mid = $1)", mid).Scan(&seen)
	return seen, err
}

// MarkMessengerInbound records an inbound message as handled.
func (s *Store) MarkMessengerInbound(ctx context.Context, mid string) error {
	_, err := s.pool.Exec(ctx, "INSERT INTO messenger_inbound (mid) VALUES ($1) ON CONFLICT DO NOTHING", mid)
	return err
}

// PruneMessenger drops expired link codes and old delivery records.
func (s *Store) PruneMessenger(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, "DELETE FROM messenger_link_codes WHERE expires_at < now()"); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, "DELETE FROM messenger_inbound WHERE created_at < now() - interval '7 days'"); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, "DELETE FROM messenger_sent WHERE created_at < now() - interval '30 days'")
	return err
}

// MessagesAfter returns the user's messages after the cursor, across
// threads, oldest first. Rows younger than settle are left for the next
// read: a transaction that began earlier may still commit a row with an
// earlier created_at, and the cursor must never step over it.
func (s *Store) MessagesAfter(ctx context.Context, userID uuid.UUID, at time.Time, after *uuid.UUID, limit int, settle time.Duration) ([]*Message, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+messageColumns+` FROM messages m
		WHERE m.user_id = $1 AND (m.created_at, m.id) > ($2, COALESCE($3, '00000000-0000-0000-0000-000000000000'::uuid))
			AND m.created_at < now() - $5::interval
		ORDER BY m.created_at, m.id LIMIT $4`, userID, at, after, limit, settle)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err := s.hydrateAttachments(ctx, userID, out); err != nil {
		return nil, err
	}
	return out, nil
}

// QuestionsAfter returns the user's questions asked after the cursor,
// oldest first, with the same settle rule as MessagesAfter.
func (s *Store) QuestionsAfter(ctx context.Context, userID uuid.UUID, at time.Time, after *uuid.UUID, limit int, settle time.Duration) ([]*Question, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+questionColumns+` FROM questions q
		WHERE q.user_id = $1 AND (q.created_at, q.id) > ($2, COALESCE($3, '00000000-0000-0000-0000-000000000000'::uuid))
			AND q.created_at < now() - $5::interval
		ORDER BY q.created_at, q.id LIMIT $4`, userID, at, after, limit, settle)
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
