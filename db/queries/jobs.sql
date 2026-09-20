-- name: EnqueueJob :one
INSERT INTO jobs (id, instance_id, kind, payload, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, instance_id, kind, payload, status, attempts, max_attempts,
          lease_owner, lease_expires_at, last_error, created_by, created_at,
          started_at, finished_at;

-- name: LeaseNextJob :one
UPDATE jobs
SET status = 'leased', lease_owner = $1, lease_expires_at = now() + $2::interval,
    attempts = attempts + 1, started_at = COALESCE(started_at, now())
WHERE id = (
    SELECT id FROM jobs
    WHERE (status = 'queued' OR (status IN ('leased', 'running') AND lease_expires_at < now()))
      AND attempts < max_attempts
    ORDER BY created_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING id, instance_id, kind, payload, status, attempts, max_attempts,
          lease_owner, lease_expires_at, last_error, created_by, created_at,
          started_at, finished_at;

-- name: CompleteJob :exec
UPDATE jobs
SET status = 'succeeded', lease_owner = NULL, lease_expires_at = NULL,
    finished_at = now(), last_error = NULL
WHERE id = $1 AND lease_owner = $2;

-- name: FailJob :exec
UPDATE jobs
SET status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'queued' END,
    lease_owner = NULL, lease_expires_at = NULL, last_error = $3,
    finished_at = CASE WHEN attempts >= max_attempts THEN now() ELSE NULL END
WHERE id = $1 AND lease_owner = $2;
