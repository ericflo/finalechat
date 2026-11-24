-- Display renditions: a compressed copy of a heavy image, stored beside the
-- original under display_key. The app shows it full screen; a download still
-- fetches the original under object_key. An image whose compressed copy
-- would not be much smaller has none.

ALTER TABLE attachments
    ADD COLUMN display_key        text,
    ADD COLUMN display_id         text NOT NULL DEFAULT '',
    ADD COLUMN display_width      integer NOT NULL DEFAULT 0,
    ADD COLUMN display_height     integer NOT NULL DEFAULT 0,
    ADD COLUMN display_size       integer NOT NULL DEFAULT 0,
    -- When the server decided whether this image gets one. NULL marks an
    -- image stored before the decision existed; a background pass works
    -- through those newest first, claiming each by setting this.
    ADD COLUMN display_checked_at timestamptz;

CREATE INDEX attachments_display_backfill_idx ON attachments (created_at DESC)
    WHERE display_checked_at IS NULL AND kind = 'image';
