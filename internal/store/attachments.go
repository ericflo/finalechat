package store

import (
	"context"
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
const MaxAttachmentBytes = 10 << 20

// MaxAttachmentsPerMessage bounds how many files one message carries.
const MaxAttachmentsPerMessage = 8

// Attachment is the metadata of an uploaded file; bytes live in object
// storage under ObjectKey (and ThumbKey for image thumbnails).
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
	CreatedAt   time.Time  `json:"created_at"`
	ObjectKey   string     `json:"-"`
	ObjectID    string     `json:"-"`
	ThumbKey    *string    `json:"-"`
	ThumbID     string     `json:"-"`
	// URL and ThumbURL are filled by the API layer.
	URL      string `json:"url"`
	ThumbURL string `json:"thumb_url,omitempty"`
}

// HasThumb reports whether a thumbnail object exists.
func (a *Attachment) HasThumb() bool { return a.ThumbKey != nil && *a.ThumbKey != "" }

const attachmentColumns = "a.id, a.thread_id, a.message_id, a.kind, a.content_type, a.filename, a.size, a.width, a.height, a.thumb_width, a.thumb_height, a.created_at, a.object_key, a.object_id, a.thumb_key, a.thumb_id"

func scanAttachment(row pgx.Row) (*Attachment, error) {
	var a Attachment
	if err := row.Scan(&a.ID, &a.ThreadID, &a.MessageID, &a.Kind, &a.ContentType, &a.Filename, &a.Size, &a.Width, &a.Height, &a.ThumbWidth, &a.ThumbHeight, &a.CreatedAt, &a.ObjectKey, &a.ObjectID, &a.ThumbKey, &a.ThumbID); err != nil {
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
}

// CreateAttachment records an upload that is not yet part of a message.
func (s *Store) CreateAttachment(ctx context.Context, userID, threadID uuid.UUID, in AttachmentInput) (*Attachment, error) {
	var thumbKey *string
	if in.ThumbKey != "" {
		thumbKey = &in.ThumbKey
	}
	return scanAttachment(s.pool.QueryRow(ctx, `WITH a AS (
			INSERT INTO attachments (id, user_id, thread_id, kind, content_type, filename, size, width, height, object_key, object_id, thumb_key, thumb_id, thumb_width, thumb_height)
			SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15
			WHERE EXISTS (SELECT 1 FROM threads WHERE id = $3 AND user_id = $2)
			RETURNING *
		) SELECT `+attachmentColumns+` FROM a`,
		in.ID, userID, threadID, in.Kind, in.ContentType, truncate(in.Filename, 255), in.Size, in.Width, in.Height, in.ObjectKey, in.ObjectID, thumbKey, in.ThumbID, in.ThumbWidth, in.ThumbHeight))
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
	rows, err := s.pool.Query(ctx, "SELECT "+attachmentColumns+" FROM attachments a WHERE a.message_id IS NULL AND a.created_at < now() - $1 ORDER BY a.created_at LIMIT $2", olderThan, limit)
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

// DeleteAttachment removes the metadata row.
func (s *Store) DeleteAttachment(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM attachments WHERE id = $1", id)
	return err
}
