-- +goose Up

-- A single instance needs more than one port per protocol (game/query are
-- both UDP). Keep existing allocations by assigning them the legacy purpose
-- while allowing new allocations to be named and independently unique.
ALTER TABLE port_allocations
    ADD COLUMN IF NOT EXISTS purpose TEXT NOT NULL DEFAULT 'legacy';

ALTER TABLE port_allocations
    DROP CONSTRAINT IF EXISTS port_allocations_instance_id_protocol_key;

ALTER TABLE port_allocations
    ADD CONSTRAINT port_allocations_purpose_check
    CHECK (purpose IN ('game', 'query', 'rcon', 'legacy'));

CREATE UNIQUE INDEX IF NOT EXISTS port_allocations_instance_purpose_key
    ON port_allocations (instance_id, purpose);

-- +goose Down

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM port_allocations
        GROUP BY instance_id, protocol
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'cannot downgrade port allocations: multiple ports exist for one instance/protocol';
    END IF;
END $$;

DROP INDEX IF EXISTS port_allocations_instance_purpose_key;
ALTER TABLE port_allocations DROP CONSTRAINT IF EXISTS port_allocations_purpose_check;
ALTER TABLE port_allocations DROP COLUMN IF EXISTS purpose;
ALTER TABLE port_allocations
    ADD CONSTRAINT port_allocations_instance_id_protocol_key UNIQUE (instance_id, protocol);
