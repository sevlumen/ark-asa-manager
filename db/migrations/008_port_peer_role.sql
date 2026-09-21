-- +goose Up

-- ASA needs a second UDP allocation for peer traffic. Keep the allocation
-- model role-based instead of reintroducing instance/protocol uniqueness.
ALTER TABLE port_allocations
    DROP CONSTRAINT IF EXISTS port_allocations_purpose_check;

ALTER TABLE port_allocations
    ADD CONSTRAINT port_allocations_purpose_check
    CHECK (purpose IN ('game', 'peer', 'query', 'rcon', 'legacy'));

-- +goose Down

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM port_allocations WHERE purpose = 'peer') THEN
        RAISE EXCEPTION 'cannot downgrade while peer port allocations exist';
    END IF;
END $$;

ALTER TABLE port_allocations
    DROP CONSTRAINT IF EXISTS port_allocations_purpose_check;

ALTER TABLE port_allocations
    ADD CONSTRAINT port_allocations_purpose_check
    CHECK (purpose IN ('game', 'query', 'rcon', 'legacy'));
