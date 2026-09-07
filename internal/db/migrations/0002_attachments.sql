-- Attachments: images and files sent by agents or the user. Only metadata
-- lives here; the bytes live in object storage under object_key (and
-- thumb_key for image thumbnails).

CREATE TABLE attachments (
    id            uuid PRIMARY KEY,
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    thread_id     uuid NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    message_id    uuid REFERENCES messages(id) ON DELETE CASCADE,
    kind          text NOT NULL CHECK (kind IN ('image', 'file')),
    content_type  text NOT NULL,
    filename      text NOT NULL DEFAULT '',
    size          integer NOT NULL,
    width         integer NOT NULL DEFAULT 0,
    height        integer NOT NULL DEFAULT 0,
    object_key    text NOT NULL,
    object_id     text NOT NULL DEFAULT '',
    thumb_key     text,
    thumb_id      text NOT NULL DEFAULT '',
    thumb_width   integer NOT NULL DEFAULT 0,
    thumb_height  integer NOT NULL DEFAULT 0,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX attachments_message_id_idx ON attachments (message_id);
CREATE INDEX attachments_thread_created_idx ON attachments (thread_id, created_at);
-- Uploads that were never attached to a message are garbage-collected.
CREATE INDEX attachments_orphan_idx ON attachments (created_at) WHERE message_id IS NULL;
