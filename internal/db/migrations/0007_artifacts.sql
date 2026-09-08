-- Artifact bytes are independent of message attachments. Immutable revisions
-- retain their objects until explicitly removed with the artifact/thread.
CREATE TABLE artifacts (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    thread_id uuid NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    external_key text NOT NULL,
    title text NOT NULL,
    current_revision_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(thread_id, external_key)
);
CREATE INDEX artifacts_user_thread_idx ON artifacts(user_id, thread_id);

CREATE TABLE artifact_revisions (
    id uuid PRIMARY KEY,
    artifact_id uuid NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE,
    previous_revision_id uuid,
    manifest jsonb NOT NULL,
    manifest_sha256 text NOT NULL,
    client_key text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(artifact_id, client_key)
);
CREATE INDEX artifact_revisions_artifact_idx ON artifact_revisions(artifact_id, id DESC);
ALTER TABLE artifacts ADD CONSTRAINT artifacts_current_revision_fk
    FOREIGN KEY(current_revision_id) REFERENCES artifact_revisions(id) ON DELETE SET NULL;

CREATE TABLE artifact_blobs (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sha256 text NOT NULL,
    size bigint NOT NULL CHECK (size > 0 AND size <= 1048576),
    object_key text NOT NULL UNIQUE,
    object_id text NOT NULL DEFAULT '',
    state text NOT NULL CHECK (state IN ('uploading', 'ready')),
    reservation uuid NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(user_id, sha256)
);
CREATE TABLE artifact_revision_blobs (
    revision_id uuid NOT NULL REFERENCES artifact_revisions(id) ON DELETE CASCADE,
    user_id uuid NOT NULL,
    sha256 text NOT NULL,
    PRIMARY KEY(revision_id, sha256),
    FOREIGN KEY(user_id, sha256) REFERENCES artifact_blobs(user_id, sha256) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX artifact_revision_blobs_object_idx ON artifact_revision_blobs(user_id, sha256);

-- Survives deletion of the account as well as server restarts. B2 deletes
-- use version IDs; retrying removal of an already missing object is harmless.
CREATE TABLE artifact_object_deletions (
    object_key text NOT NULL,
    object_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    attempts integer NOT NULL DEFAULT 0,
    retry_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(object_key, object_id)
);
CREATE FUNCTION queue_artifact_blob_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO artifact_object_deletions(object_key, object_id, retry_at)
    VALUES (OLD.object_key, OLD.object_id,
        CASE WHEN OLD.state='uploading' THEN now()+interval '15 minutes' ELSE now() END)
    ON CONFLICT DO NOTHING;
    RETURN OLD;
END;
$$;
CREATE TRIGGER artifact_blob_delete AFTER DELETE ON artifact_blobs
    FOR EACH ROW EXECUTE FUNCTION queue_artifact_blob_delete();
