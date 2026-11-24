package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Attachment kinds.
const (
	AttachmentImage = "image"
	AttachmentFile  = "file"
)

// MaxAttachmentBytes bounds one upload.
const MaxAttachmentBytes = 25 << 20

// MaxAttachmentsPerMessage bounds how many files one message carries.
const MaxAttachmentsPerMessage = 8

// ErrAttachmentQuota reports that an account's attachment storage is full.
var ErrAttachmentQuota = errors.New("attachment storage quota exceeded")

// Attachment is the metadata of an uploaded file; bytes live in object
// storage under ObjectKey (and, for images, ThumbKey for the thumbnail and
// DisplayKey for the compressed copy the app shows full screen).
type Attachment struct {
	ID          uuid.UUID  `json:"id"`
	ThreadID    uuid.UUID  `json:"thread_id"`
	MessageID   *uuid.UUID `json:"message_id"`
	Kind        string     `json:"kind"`
	ContentType string     `json:"content_type"`
	Filename    string     `json:"filename"`
	Size        int        `json:"size"`
	Width       int        `json:"width,omitempty"`
	Height      int        `json:"height,omitempty"`
	ThumbWidth  int        `json:"thumb_width,omitempty"`
	ThumbHeight int        `json:"thumb_height,omitempty"`
	// The display rendition, when the image has one.
	DisplayWidth  int       `json:"display_width,omitempty"`
	DisplayHeight int       `json:"display_height,omitempty"`
	DisplaySize   int       `json:"display_size,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	ObjectKey     string    `json:"-"`
	ObjectID      string    `json:"-"`
	ThumbKey      *string   `json:"-"`
	ThumbID       string    `json:"-"`
	DisplayKey    *string   `json:"-"`
	DisplayID     string    `json:"-"`
	// URL, ThumbURL and DisplayURL are filled by the API layer.
	URL        string `json:"url"`
	ThumbURL   string `json:"thumb_url,omitempty"`
	DisplayURL string `json:"display_url,omitempty"`
}

// HasThumb reports whether a thumbnail object exists.
func (a *Attachment) HasThumb() bool { return a.ThumbKey != nil && *a.ThumbKey != "" }

// HasDisplay reports whether a display rendition exists.
func (a *Attachment) HasDisplay() bool { return a.DisplayKey != nil && *a.DisplayKey != "" }

const attachmentColumns = "a.id, a.thread_id, a.message_id, a.kind, a.content_type, a.filename, a.size, a.width, a.height, a.thumb_width, a.thumb_height, a.created_at, a.object_key, a.object_id, a.thumb_key, a.thumb_id, a.display_key, a.display_id, a.display_width, a.display_height, a.display_size"

func scanAttachment(row pgx.Row) (*Attachment, error) {
	var a Attachment
	if err := row.Scan(&a.ID, &a.ThreadID, &a.MessageID, &a.Kind, &a.ContentType, &a.Filename, &a.Size, &a.Width, &a.Height, &a.ThumbWidth, &a.ThumbHeight, &a.CreatedAt, &a.ObjectKey, &a.ObjectID, &a.ThumbKey, &a.ThumbID, &a.DisplayKey, &a.DisplayID, &a.DisplayWidth, &a.DisplayHeight, &a.DisplaySize); err != nil {
		return nil, translate(err)
	}
	return &a, nil
}

// AttachmentInput describes an upload whose bytes are already stored.
type AttachmentInput struct {
	ID          uuid.UUID
	Kind        string
	ContentType string
	Filename    string
	Size        int
	Width       int
	Height      int
	ObjectKey   string
	ObjectID    string
	ThumbKey    string
	ThumbID     string
	ThumbWidth  int
	ThumbHeight int
	// Display is the display rendition, if the image got one.
	Display *DisplayInput
}

// DisplayInput describes a stored display rendition.
type DisplayInput struct {
	Key    string
	ID     string
	Width  int
	Height int
	Size   int
}

// CreateAttachment records an upload that is not yet part of a message. A
// positive quota caps the account's total attachment bytes; the check and
// the insert are serialized per account so concurrent uploads cannot pass
// it together.
func (s *Store) CreateAttachment(ctx context.Context, userID, threadID uuid.UUID, in AttachmentInput, quota int64) (*Attachment, error) {
	var thumbKey, displayKey *string
	if in.ThumbKey != "" {
		thumbKey = &in.ThumbKey
	}
	display := DisplayInput{}
	if in.Display != nil {
		display, displayKey = *in.Display, &in.Display.Key
	}
	var out *Attachment
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if quota > 0 {
			if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 7319))", userID.String()); err != nil {
				return err
			}
			var used int64
			if err := tx.QueryRow(ctx, "SELECT COALESCE(sum(size), 0)::bigint FROM attachments WHERE user_id = $1", userID).Scan(&used); err != nil {
				return err
			}
			if used+int64(in.Size) > quota {
				return ErrAttachmentQuota
			}
		}
		a, err := scanAttachment(tx.QueryRow(ctx, `WITH a AS (
				INSERT INTO attachments (id, user_id, thread_id, kind, content_type, filename, size, width, height, object_key, object_id, thumb_key, thumb_id, thumb_width, thumb_height,
					display_key, display_id, display_width, display_height, display_size, display_checked_at)
				SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, now()
				WHERE EXISTS (SELECT 1 FROM threads WHERE id = $3 AND user_id = $2)
				RETURNING *
			) SELECT `+attachmentColumns+` FROM a`,
			in.ID, userID, threadID, in.Kind, in.ContentType, truncate(in.Filename, 255), in.Size, in.Width, in.Height, in.ObjectKey, in.ObjectID, thumbKey, in.ThumbID, in.ThumbWidth, in.ThumbHeight,
			displayKey, display.ID, display.Width, display.Height, display.Size))
		if err != nil {
			return err
		}
		out = a
		return nil
	})
	return out, err
}

// AttachmentBytesUsed sums the sizes of every attachment the account owns.
func (s *Store) AttachmentBytesUsed(ctx context.Context, userID uuid.UUID) (int64, error) {
	var used int64
	err := s.pool.QueryRow(ctx, "SELECT COALESCE(sum(size), 0)::bigint FROM attachments WHERE user_id = $1", userID).Scan(&used)
	return used, err
}

// ListUserAttachments returns every attachment an account owns, so their
// objects can be removed once the account is gone.
func (s *Store) ListUserAttachments(ctx context.Context, userID uuid.UUID) ([]*Attachment, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+attachmentColumns+" FROM attachments a WHERE a.user_id = $1", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Attachment{}
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAttachment loads an attachment the user owns.
func (s *Store) GetAttachment(ctx context.Context, userID, id uuid.UUID) (*Attachment, error) {
	return scanAttachment(s.pool.QueryRow(ctx, "SELECT "+attachmentColumns+" FROM attachments a WHERE a.id = $1 AND a.user_id = $2", id, userID))
}

// ListAttachmentsForMessages returns attachments grouped by message id for a
// set of messages, in upload order.
func (s *Store) ListAttachmentsForMessages(ctx context.Context, userID uuid.UUID, messageIDs []uuid.UUID) (map[uuid.UUID][]*Attachment, error) {
	out := map[uuid.UUID][]*Attachment{}
	if len(messageIDs) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, "SELECT "+attachmentColumns+" FROM attachments a WHERE a.user_id = $1 AND a.message_id = ANY($2) ORDER BY a.created_at, a.id", userID, messageIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		if a.MessageID != nil {
			out[*a.MessageID] = append(out[*a.MessageID], a)
		}
	}
	return out, rows.Err()
}

// ListThreadAttachments returns every attachment in a thread.
func (s *Store) ListThreadAttachments(ctx context.Context, userID, threadID uuid.UUID) ([]*Attachment, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+attachmentColumns+" FROM attachments a WHERE a.user_id = $1 AND a.thread_id = $2", userID, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Attachment{}
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// attachToMessage binds pending uploads in the thread to a message. It fails
// with ErrNotFound if any id is unknown, belongs to another thread, or is
// already attached.
func attachToMessage(ctx context.Context, tx pgx.Tx, userID, threadID, messageID uuid.UUID, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	tag, err := tx.Exec(ctx, "UPDATE attachments SET message_id = $3 WHERE user_id = $1 AND thread_id = $2 AND message_id IS NULL AND id = ANY($4)", userID, threadID, messageID, ids)
	if err != nil {
		return err
	}
	if int(tag.RowsAffected()) != len(ids) {
		return ErrNotFound
	}
	return nil
}

// OrphanAttachments lists uploads never attached to a message.
func (s *Store) OrphanAttachments(ctx context.Context, olderThan time.Duration, limit int) ([]*Attachment, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+attachmentColumns+" FROM attachments a WHERE a.message_id IS NULL AND a.created_at < now() - $1::interval ORDER BY a.created_at LIMIT $2", olderThan, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Attachment{}
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ClaimDisplayBackfill picks the newest image stored before display
// renditions existed and marks it decided, so each one is tried once and by
// one server. ErrNotFound when none is left.
func (s *Store) ClaimDisplayBackfill(ctx context.Context) (*Attachment, error) {
	return scanAttachment(s.pool.QueryRow(ctx, `WITH a AS (
			UPDATE attachments SET display_checked_at = now()
			WHERE id = (
				SELECT id FROM attachments
				WHERE display_checked_at IS NULL AND kind = 'image'
				ORDER BY created_at DESC LIMIT 1
				FOR UPDATE SKIP LOCKED
			)
			RETURNING *
		) SELECT `+attachmentColumns+` FROM a`))
}

// SetAttachmentDisplay records a display rendition made after the upload.
// ErrNotFound when the attachment was deleted meanwhile.
func (s *Store) SetAttachmentDisplay(ctx context.Context, id uuid.UUID, in DisplayInput) error {
	tag, err := s.pool.Exec(ctx, "UPDATE attachments SET display_key = $2, display_id = $3, display_width = $4, display_height = $5, display_size = $6 WHERE id = $1",
		id, in.Key, in.ID, in.Width, in.Height, in.Size)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteAttachment removes the metadata row.
func (s *Store) DeleteAttachment(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM attachments WHERE id = $1", id)
	return err
}
