-- Per-account attachment storage is summed on every upload; an index on the
-- owner with the size included keeps that an index-only scan.

CREATE INDEX attachments_user_size_idx ON attachments (user_id) INCLUDE (size);
