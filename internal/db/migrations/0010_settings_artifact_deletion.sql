-- An artifact-backed command must never become an independently authorized
-- project command when ON DELETE SET NULL removes its source references.
-- This trigger also covers thread/account cascades, in the same transaction.
CREATE INDEX settings_commands_artifact_idx ON settings_commands(artifact_id)
    WHERE artifact_id IS NOT NULL;

CREATE FUNCTION fence_deleted_artifact_commands() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    WITH changed AS (
        UPDATE settings_commands
        SET status = CASE WHEN status='queued' THEN 'cancelled' ELSE 'unknown' END,
            finished_at=now(), lease_until=NULL,
            result=jsonb_build_object('reason','artifact_deleted')
        WHERE artifact_id=OLD.id AND status IN ('queued','executing')
        RETURNING id,user_id,resource_id,status
    )
    INSERT INTO settings_audit(id,user_id,resource_id,command_id,event,detail)
    SELECT id,user_id,resource_id,id,'artifact.deleted',
           jsonb_build_object('artifact_id',OLD.id,'status',status)
    FROM changed
    -- Account deletion also cascades through artifacts. Do not recreate audit
    -- rows belonging to the account that is itself disappearing.
    WHERE EXISTS (SELECT 1 FROM users WHERE users.id=changed.user_id)
    ON CONFLICT DO NOTHING;
    RETURN OLD;
END;
$$;

CREATE TRIGGER artifacts_fence_commands BEFORE DELETE ON artifacts
    FOR EACH ROW EXECUTE FUNCTION fence_deleted_artifact_commands();
