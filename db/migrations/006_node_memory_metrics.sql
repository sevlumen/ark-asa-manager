-- +goose Up
ALTER TABLE nodes
    ADD COLUMN IF NOT EXISTS memory_total_bytes BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS memory_used_bytes BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS memory_observed_at TIMESTAMPTZ;

ALTER TABLE nodes
    ADD CONSTRAINT nodes_memory_total_nonnegative CHECK (memory_total_bytes >= 0),
    ADD CONSTRAINT nodes_memory_used_nonnegative CHECK (memory_used_bytes >= 0),
    ADD CONSTRAINT nodes_memory_used_within_total CHECK (memory_used_bytes <= memory_total_bytes);

-- +goose Down
ALTER TABLE nodes
    DROP COLUMN IF EXISTS memory_total_bytes,
    DROP COLUMN IF EXISTS memory_used_bytes,
    DROP COLUMN IF EXISTS memory_observed_at;
