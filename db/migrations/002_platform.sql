-- +goose Up
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('admin', 'operator', 'viewer')),
    disabled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_hash TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS api_tokens (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    last_used_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS node_enrollments (
    id TEXT PRIMARY KEY,
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS instance_status (
    instance_id TEXT PRIMARY KEY REFERENCES instances(id) ON DELETE CASCADE,
    observed_state TEXT NOT NULL DEFAULT 'unknown',
    container_id TEXT,
    health TEXT NOT NULL DEFAULT 'unknown',
    last_error TEXT,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS jobs (
    id TEXT PRIMARY KEY,
    instance_id TEXT REFERENCES instances(id) ON DELETE SET NULL,
    kind TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'leased', 'running', 'succeeded', 'failed', 'cancelled')),
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    lease_owner TEXT,
    lease_expires_at TIMESTAMPTZ,
    last_error TEXT,
    created_by TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS job_attempts (
    id BIGSERIAL PRIMARY KEY,
    job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    attempt INTEGER NOT NULL,
    worker_id TEXT NOT NULL,
    error TEXT,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    UNIQUE (job_id, attempt)
);

CREATE TABLE IF NOT EXISTS backups (
    id TEXT PRIMARY KEY,
    instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    backend TEXT NOT NULL CHECK (backend IN ('local', 's3', 'r2')),
    object_key TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    bytes BIGINT NOT NULL DEFAULT 0,
    verified_at TIMESTAMPTZ,
    created_by TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS encrypted_secrets (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    ciphertext BYTEA NOT NULL,
    nonce BYTEA NOT NULL,
    key_version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS port_allocations (
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    protocol TEXT NOT NULL CHECK (protocol IN ('udp', 'tcp')),
    port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    allocated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (node_id, protocol, port),
    UNIQUE (instance_id, protocol)
);

CREATE TABLE IF NOT EXISTS event_cursor (
    id BIGSERIAL PRIMARY KEY,
    event_type TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS jobs_lease_idx ON jobs (status, lease_expires_at, created_at);
CREATE INDEX IF NOT EXISTS audit_log_created_idx ON audit_log (created_at DESC);
CREATE INDEX IF NOT EXISTS event_cursor_created_idx ON event_cursor (id);

-- +goose Down
DROP TABLE IF EXISTS event_cursor;
DROP TABLE IF EXISTS port_allocations;
DROP TABLE IF EXISTS encrypted_secrets;
DROP TABLE IF EXISTS backups;
DROP TABLE IF EXISTS job_attempts;
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS instance_status;
DROP TABLE IF EXISTS node_enrollments;
DROP TABLE IF EXISTS api_tokens;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
