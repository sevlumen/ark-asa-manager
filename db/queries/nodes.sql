-- name: ListNodes :many
SELECT id, name, endpoint, status, last_heartbeat, created_at, updated_at
FROM nodes
ORDER BY name, id;

-- name: GetNode :one
SELECT id, name, endpoint, status, last_heartbeat, created_at, updated_at
FROM nodes
WHERE id = $1;

-- name: UpsertNodeHeartbeat :exec
INSERT INTO nodes (id, name, endpoint, status, last_heartbeat)
VALUES ($1, $2, $3, 'online', now())
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    endpoint = EXCLUDED.endpoint,
    status = 'online',
    last_heartbeat = now(),
    updated_at = now();
