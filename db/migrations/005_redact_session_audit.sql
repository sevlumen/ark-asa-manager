-- +goose Up

-- Session IDs are bearer credentials and must never remain visible in the
-- audit resource column. This migration is intentionally one-way.
UPDATE audit_log
SET resource = 'session'
WHERE action IN ('auth.login', 'auth.logout')
  AND resource LIKE 'session:%';

-- +goose Down
-- Irreversible redaction: the original session identifiers are not recoverable.
