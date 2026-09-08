-- A clear's timestamp bounds how long its sequence watermark rejects
-- sequenced status writes, so one clear from a fast clock cannot pin a
-- thread's status line shut forever.
ALTER TABLE threads ADD COLUMN activity_cleared_at timestamptz;

-- 0005's live-message index covers every query this one served.
DROP INDEX IF EXISTS messages_thread_unread_idx;
