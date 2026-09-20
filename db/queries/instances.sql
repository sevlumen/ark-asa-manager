-- name: ListInstances :many
SELECT id, node_id, map_name, cluster_id, desired_state, created_at, updated_at
FROM instances
ORDER BY id;
