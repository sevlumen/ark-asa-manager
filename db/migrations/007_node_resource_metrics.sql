-- +goose Up
ALTER TABLE nodes
    ADD COLUMN IF NOT EXISTS cpu_percent DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS disk_used_bytes BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS network_rx_bytes BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS network_tx_bytes BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS resource_observed_at TIMESTAMPTZ;

ALTER TABLE nodes
    ADD CONSTRAINT nodes_cpu_percent_nonnegative CHECK (cpu_percent >= 0),
    ADD CONSTRAINT nodes_disk_used_nonnegative CHECK (disk_used_bytes >= 0),
    ADD CONSTRAINT nodes_network_rx_nonnegative CHECK (network_rx_bytes >= 0),
    ADD CONSTRAINT nodes_network_tx_nonnegative CHECK (network_tx_bytes >= 0);

-- +goose Down
ALTER TABLE nodes
    DROP COLUMN IF EXISTS cpu_percent,
    DROP COLUMN IF EXISTS disk_used_bytes,
    DROP COLUMN IF EXISTS network_rx_bytes,
    DROP COLUMN IF EXISTS network_tx_bytes,
    DROP COLUMN IF EXISTS resource_observed_at;
