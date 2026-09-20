-- name: GetUserByUsername :one
SELECT id, username, password_hash, role, disabled_at, created_at, updated_at
FROM users
WHERE username = $1;

-- name: GetUserByID :one
SELECT id, username, password_hash, role, disabled_at, created_at, updated_at
FROM users
WHERE id = $1;

-- name: CreateSession :one
INSERT INTO sessions (id, user_id, csrf_hash, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING id, user_id, csrf_hash, expires_at, revoked_at, created_at;
