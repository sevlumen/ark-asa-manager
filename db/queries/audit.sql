-- name: AppendAudit :one
INSERT INTO audit_log (actor, action, resource, metadata)
VALUES ($1, $2, $3, $4)
RETURNING id, actor, action, resource, metadata, created_at;

-- name: ListAudit :many
SELECT id, actor, action, resource, metadata, created_at
FROM audit_log
ORDER BY id DESC
LIMIT $1 OFFSET $2;
