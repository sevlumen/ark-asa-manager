-- +goose Up
CREATE TABLE IF NOT EXISTS nodes (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'unknown',
    last_heartbeat TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS instances (
    id TEXT PRIMARY KEY,
    node_id TEXT NOT NULL REFERENCES nodes(id),
    map_name TEXT NOT NULL,
    cluster_id TEXT NOT NULL,
    desired_state TEXT NOT NULL DEFAULT 'stopped',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS audit_log (
    id BIGSERIAL PRIMARY KEY,
    actor TEXT NOT NULL,
    action TEXT NOT NULL,
    resource TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS instances;
DROP TABLE IF EXISTS nodes;
