-- A browser may keep editing a surface it opened while current, even as
-- proactive transcript publication advances the artifact's immutable head.
CREATE TABLE settings_surface_leases (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    resource_id uuid NOT NULL REFERENCES settings_resources(id) ON DELETE CASCADE,
    artifact_id uuid NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE,
    revision_id uuid NOT NULL REFERENCES artifact_revisions(id) ON DELETE CASCADE,
    generation text NOT NULL,
    expires_at timestamptz NOT NULL DEFAULT now()+interval '30 minutes'
);
CREATE INDEX settings_surface_leases_user_idx ON settings_surface_leases(user_id,expires_at);
