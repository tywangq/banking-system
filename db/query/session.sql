-- name: CreateSession :one
INSERT INTO sessions (
  id,
  username,
  refresh_token,
  user_agent,
  client_ip,
  is_blocked,
  expires_at
) 
VALUES (
  $1, 
  $2, 
  $3, 
  $4, 
  $5, 
  $6, 
  $7
) 
RETURNING *;

-- name: GetSession :one
SELECT * 
FROM sessions
WHERE id = $1 
LIMIT 1;

-- name: BlockSession :one
-- Revokes one session. Blocking rather than deleting keeps the audit trail: the
-- user agent and client IP of a session that was signed out stay inspectable.
UPDATE sessions
SET is_blocked = true
WHERE id = $1
RETURNING *;

-- name: BlockUserSessions :execrows
-- Revokes every session a user holds, for "sign out everywhere". Returns the number
-- of rows touched so the caller can report how many were actually open.
UPDATE sessions
SET is_blocked = true
WHERE username = $1 AND is_blocked = false;
