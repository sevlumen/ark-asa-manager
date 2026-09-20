-- +goose Up
-- Convert the existing allocation table in place. Keeping the relation means
-- PostgreSQL can release the old port_allocations_pkey before recreating it.
ALTER TABLE port_allocations
    ADD COLUMN IF NOT EXISTS port_role TEXT,
    ADD COLUMN IF NOT EXISTS host_port INTEGER,
    ADD COLUMN IF NOT EXISTS container_port INTEGER;

UPDATE port_allocations
SET port_role = CASE WHEN protocol = 'udp' THEN 'game' ELSE 'rcon' END,
    host_port = port,
    container_port = port
WHERE port_role IS NULL
   OR host_port IS NULL
   OR container_port IS NULL;

ALTER TABLE port_allocations
    ALTER COLUMN port_role SET NOT NULL,
    ALTER COLUMN host_port SET NOT NULL,
    ALTER COLUMN container_port SET NOT NULL;

ALTER TABLE port_allocations
    DROP CONSTRAINT IF EXISTS port_allocations_pkey,
    DROP CONSTRAINT IF EXISTS port_allocations_instance_id_protocol_key;

ALTER TABLE port_allocations
    DROP COLUMN port;

ALTER TABLE port_allocations
    ADD CONSTRAINT port_allocations_pkey PRIMARY KEY (node_id, protocol, host_port),
    ADD CONSTRAINT port_allocations_instance_id_port_role_key UNIQUE (instance_id, port_role),
    ADD CONSTRAINT port_allocations_host_port_check CHECK (host_port BETWEEN 1 AND 65535),
    ADD CONSTRAINT port_allocations_container_port_check CHECK (container_port BETWEEN 1 AND 65535),
    ADD CONSTRAINT port_allocations_role_protocol_check CHECK (
        (port_role IN ('game', 'peer', 'query') AND protocol = 'udp')
        OR (port_role = 'rcon' AND protocol = 'tcp')
    );

-- +goose Down
ALTER TABLE port_allocations
    DROP CONSTRAINT IF EXISTS port_allocations_role_protocol_check,
    DROP CONSTRAINT IF EXISTS port_allocations_container_port_check,
    DROP CONSTRAINT IF EXISTS port_allocations_host_port_check,
    DROP CONSTRAINT IF EXISTS port_allocations_instance_id_port_role_key,
    DROP CONSTRAINT IF EXISTS port_allocations_pkey;

ALTER TABLE port_allocations
    ADD COLUMN IF NOT EXISTS port INTEGER;

UPDATE port_allocations
SET port = host_port
WHERE port IS NULL;

ALTER TABLE port_allocations
    ALTER COLUMN port SET NOT NULL;

ALTER TABLE port_allocations
    DROP COLUMN port_role,
    DROP COLUMN host_port,
    DROP COLUMN container_port;

ALTER TABLE port_allocations
    ADD CONSTRAINT port_allocations_pkey PRIMARY KEY (node_id, protocol, port),
    ADD CONSTRAINT port_allocations_instance_id_protocol_key UNIQUE (instance_id, protocol);
