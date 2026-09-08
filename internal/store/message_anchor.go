package store

import (
	"context"

	"github.com/ericflo/finalechat/internal/artifact"
	"github.com/google/uuid"
)

func (s *Store) FindSourceMessage(ctx context.Context, userID, threadID uuid.UUID, anchor artifact.SourceAnchor) (*Message, error) {
	// Legacy producers already mirrored native sequences and transcript UUIDs.
	// Their fallback must also match the thread's exact native session identity.
	external, legacy := "", JSON{}
	switch {
	case anchor.DatasetFormat == "eagent.session-jsonl/v1" && anchor.Seq > 0:
		external = "eagent:" + anchor.SessionID
		legacy = JSON{"seq": anchor.Seq}
	case anchor.DatasetFormat == "claude-code.native-jsonl/v1" && anchor.EventID != "":
		external = "claude-code:" + anchor.SessionID
		legacy = JSON{"via": "claude-code", "transcript_uuid": anchor.EventID}
	}
	return scanMessage(s.pool.QueryRow(ctx, "SELECT "+messageColumns+` FROM messages m
		WHERE m.user_id = $1 AND m.thread_id = $2 AND m.deleted_at IS NULL
		AND (m.meta->'source_anchor' = $3::jsonb OR ($4 <> '' AND m.meta @> $5::jsonb
		AND EXISTS (SELECT 1 FROM threads t WHERE t.id = m.thread_id AND t.user_id = $1 AND t.external_id = $4)))
		ORDER BY m.created_at ASC, m.id ASC LIMIT 1`, userID, threadID, JSON(anchor.Metadata()).value(), external, legacy.value()))
}
