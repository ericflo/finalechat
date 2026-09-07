-- Agent activity: an ephemeral one-line status per thread ("running tests…")
-- that expires on its own. Only the latest value is kept; it is not part of
-- the transcript.

ALTER TABLE threads
    ADD COLUMN activity_text       text NOT NULL DEFAULT '',
    ADD COLUMN activity_kind       text NOT NULL DEFAULT '',
    ADD COLUMN activity_at         timestamptz,
    ADD COLUMN activity_since      timestamptz,
    ADD COLUMN activity_expires_at timestamptz;
