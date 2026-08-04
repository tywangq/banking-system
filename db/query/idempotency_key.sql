-- name: CreateIdempotencyKey :one
-- Claims the key. The primary key on (owner, key) is what makes this the mutual
-- exclusion: a second concurrent request carrying the same key blocks here until the
-- first transaction commits, then fails with a unique violation.
INSERT INTO idempotency_keys (
  owner, key, request_hash
) VALUES (
  $1, $2, $3
) RETURNING *;

-- name: GetIdempotencyKey :one
SELECT * FROM idempotency_keys
WHERE owner = $1 AND key = $2;

-- name: CompleteIdempotencyKey :one
UPDATE idempotency_keys
SET response_body = $3
WHERE owner = $1 AND key = $2
RETURNING *;
