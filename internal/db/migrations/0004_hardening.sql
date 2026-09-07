-- Indexes for the per-thread counts and the expiry sweep, message provenance
-- (session or token), idempotency keys for retried posts, a dismissed state
-- for questions the user declines to answer, and a sequence number that keeps
-- out-of-order status writes from going backwards.

CREATE INDEX messages_thread_unread_idx ON messages (thread_id, created_at) WHERE sender <> 'user';
CREATE INDEX questions_thread_pending_idx ON questions (thread_id) WHERE status = 'pending';
CREATE INDEX questions_expiring_idx ON questions (expires_at) WHERE status = 'pending' AND expires_at IS NOT NULL;

ALTER TABLE messages ADD COLUMN origin text NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN client_key text;
CREATE UNIQUE INDEX messages_client_key_idx ON messages (thread_id, client_key) WHERE client_key IS NOT NULL;

ALTER TABLE questions ADD COLUMN client_key text;
CREATE UNIQUE INDEX questions_client_key_idx ON questions (thread_id, client_key) WHERE client_key IS NOT NULL;
ALTER TABLE questions DROP CONSTRAINT questions_status_check;
ALTER TABLE questions ADD CONSTRAINT questions_status_check CHECK (status IN ('pending', 'answered', 'cancelled', 'expired', 'dismissed'));

ALTER TABLE threads ADD COLUMN activity_seq bigint NOT NULL DEFAULT 0;
