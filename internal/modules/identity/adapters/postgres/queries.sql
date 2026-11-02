-- name: CreateUser :exec
INSERT INTO users (id, username, display_name, password_hash, is_active, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $6);

-- name: GetUserByUsername :one
SELECT id, username, display_name, password_hash, is_active, created_at
FROM users
WHERE username = $1;

-- name: GetUserByID :one
SELECT id, username, display_name, password_hash, is_active, created_at
FROM users
WHERE id = $1;

-- name: TouchLastLogin :exec
UPDATE users SET last_login_at = now() WHERE id = $1;

-- name: UpdatePassword :exec
UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1;

-- name: SetUserActive :exec
UPDATE users SET is_active = $2, updated_at = now() WHERE id = $1;

-- name: CreateSession :exec
INSERT INTO sessions (id, user_id, user_agent, is_web)
VALUES ($1, $2, $3, $4);

-- A revoked session or a deactivated user cuts access at once.
-- name: IsSessionActive :one
SELECT EXISTS (
    SELECT 1
    FROM sessions s
    JOIN users u ON u.id = s.user_id
    WHERE s.id = $1 AND s.user_id = $2 AND s.revoked_at IS NULL AND u.is_active
);

-- name: RevokeSession :exec
UPDATE sessions SET revoked_at = now(), revoked_reason = $2
WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeUserSessions :exec
UPDATE sessions SET revoked_at = now(), revoked_reason = $2
WHERE user_id = $1 AND revoked_at IS NULL;

-- name: TouchSession :exec
UPDATE sessions SET last_used_at = now() WHERE id = $1;

-- name: CreateRefreshToken :exec
INSERT INTO refresh_tokens (id, session_id, token_hash, expires_at)
VALUES ($1, $2, $3, $4);

-- Locks the row: two refreshes with the same token cannot run at once.
-- name: LockRefreshToken :one
SELECT r.id, r.session_id, r.expires_at, r.used_at, s.user_id, s.is_web,
       s.revoked_at AS session_revoked_at, u.is_active AS user_active
FROM refresh_tokens r
JOIN sessions s ON s.id = r.session_id
JOIN users u ON u.id = s.user_id
WHERE r.token_hash = $1
FOR UPDATE OF r;

-- name: MarkRefreshTokenUsed :exec
UPDATE refresh_tokens SET used_at = $2 WHERE id = $1;

-- Housekeeping: expired tokens and long-dead sessions are no use to anyone.
-- name: DeleteExpiredRefreshTokens :execrows
DELETE FROM refresh_tokens WHERE expires_at < $1;

-- name: DeleteStaleSessions :execrows
DELETE FROM sessions s
WHERE s.last_used_at < $1
  AND NOT EXISTS (SELECT 1 FROM refresh_tokens r WHERE r.session_id = s.id);
