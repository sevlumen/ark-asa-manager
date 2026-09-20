-- +goose Up
-- Replace the protocol-only allocation key with an explicit role model while
-- preserving existing allocations during the transition.
ALTER TABLE IF EXISTS port_allocations RENAME TO port_allocations_legacy;

CREATE TABLE IF NOT EXISTS port_allocations (
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    port_role TEXT NOT NULL CHECK (port_role IN ('game', 'peer', 'query', 'rcon')),
    protocol TEXT NOT NULL CHECK (protocol IN ('udp', 'tcp')),
    host_port INTEGER NOT NULL CHECK (host_port BETWEEN 1 AND 65535),
    container_port INTEGER NOT NULL CHECK (container_port BETWEEN 1 AND 65535),
    allocated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (node_id, protocol, host_port),
    UNIQUE (instance_id, port_role),
    CHECK (
        (port_role IN ('game', 'peer', 'query') AND protocol = 'udp')
        OR (port_role = 'rcon' AND protocol = 'tcp')
    )
);

INSERT INTO port_allocations (node_id, instance_id, port_role, protocol, host_port, container_port)
SELECT node_id,
       instance_id,
       CASE WHEN protocol = 'udp' THEN 'game' ELSE 'rcon' END,
       protocol,
       port,
       port
FROM port_allocations_legacy
ON CONFLICT DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS port_allocations;
ALTER TABLE IF EXISTS port_allocations_legacy RENAME TO port_allocations;
