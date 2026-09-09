-- A settings website belongs to the resource it edits, not to one
-- conversation, so a thread and a new-session draft open the same editor.
-- An artifact is owned by exactly one of a thread or a settings resource.

ALTER TABLE artifacts ALTER COLUMN thread_id DROP NOT NULL;
ALTER TABLE artifacts ADD COLUMN resource_id uuid REFERENCES settings_resources(id) ON DELETE CASCADE;
ALTER TABLE artifacts ADD CONSTRAINT artifacts_one_owner CHECK ((thread_id IS NULL) <> (resource_id IS NULL));
CREATE UNIQUE INDEX artifacts_resource_key_idx ON artifacts(resource_id, external_key) WHERE resource_id IS NOT NULL;
