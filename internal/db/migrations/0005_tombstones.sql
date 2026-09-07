-- Deleted messages become tombstones so anchors (after=<id>) keep resolving
-- and clients learn to prune; a per-thread cleared sequence keeps a late
-- status write from resurrecting a status the agent already cleared.

ALTER TABLE messages ADD COLUMN deleted_at timestamptz;
CREATE INDEX messages_thread_live_idx ON messages (thread_id, created_at) WHERE deleted_at IS NULL AND sender <> 'user';
ALTER TABLE threads ADD COLUMN activity_cleared_seq bigint NOT NULL DEFAULT 0;
