CREATE TABLE connectors (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL,
    provider text NOT NULL,
    token_hash bytea NOT NULL UNIQUE,
    state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','active','revoked')),
    requested_grants jsonb NOT NULL,
    grants jsonb NOT NULL DEFAULT '[]',
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL DEFAULT now()+interval '15 minutes',
    last_seen_at timestamptz,
    instance text NOT NULL DEFAULT ''
);
CREATE INDEX connectors_user_idx ON connectors(user_id,id);

CREATE TABLE settings_resources (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    connector_id uuid NOT NULL REFERENCES connectors(id) ON DELETE CASCADE,
    resource_key text NOT NULL,
    label text NOT NULL,
    scope text NOT NULL,
    generation text NOT NULL DEFAULT '',
    descriptor jsonb NOT NULL,
    snapshot jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(connector_id,resource_key)
);
CREATE TABLE settings_bindings (
    artifact_id uuid PRIMARY KEY REFERENCES artifacts(id) ON DELETE CASCADE,
    resource_id uuid NOT NULL REFERENCES settings_resources(id) ON DELETE CASCADE,
    revision_id uuid NOT NULL REFERENCES artifact_revisions(id) ON DELETE CASCADE,
    generation text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE settings_commands (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    connector_id uuid NOT NULL REFERENCES connectors(id) ON DELETE CASCADE,
    resource_id uuid NOT NULL REFERENCES settings_resources(id) ON DELETE CASCADE,
    artifact_id uuid REFERENCES artifacts(id) ON DELETE SET NULL,
    revision_id uuid REFERENCES artifact_revisions(id) ON DELETE SET NULL,
    client_key text NOT NULL,
    proposal jsonb NOT NULL,
    proposal_sha256 text NOT NULL,
    status text NOT NULL CHECK(status IN ('queued','executing','succeeded','rejected','conflicted','expired','cancelled','unknown')),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    claim_token uuid,
    claim_instance text,
    lease_until timestamptz,
    attempts integer NOT NULL DEFAULT 0,
    result jsonb,
    finished_at timestamptz,
    UNIQUE(user_id,client_key)
);
CREATE INDEX settings_commands_claim_idx ON settings_commands(connector_id,status,id);
CREATE INDEX settings_commands_resource_idx ON settings_commands(resource_id,id DESC);
CREATE TABLE settings_audit (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    resource_id uuid NOT NULL REFERENCES settings_resources(id) ON DELETE CASCADE,
    command_id uuid REFERENCES settings_commands(id) ON DELETE CASCADE,
    event text NOT NULL,
    detail jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX settings_audit_resource_idx ON settings_audit(resource_id,id DESC);
CREATE TABLE settings_drafts (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    resource_id uuid NOT NULL REFERENCES settings_resources(id) ON DELETE CASCADE,
    proposal jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(user_id,resource_id)
);
