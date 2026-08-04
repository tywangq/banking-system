-- A client that retries a transfer whose response it never saw must not move the
-- money twice.
--
-- The key is scoped to its owner, so one caller's key can neither collide with nor
-- read back another caller's result.
--
-- request_hash exists to catch the other client bug: reusing a key that was already
-- spent on a *different* request. Replaying the stored response in that case would
-- silently answer a question nobody asked.
--
-- No foreign key to users on purpose. This is bookkeeping about requests, not a
-- domain relationship, and replay behaviour should not change because a user row was
-- edited or removed.
CREATE TABLE "idempotency_keys" (
  "owner" varchar NOT NULL,
  "key" varchar NOT NULL,
  "request_hash" varchar NOT NULL,
  -- Written at the end of the same transaction that claimed the key. Only a
  -- successful transfer ever gets here: if the transfer fails, the claim rolls back
  -- with it and the key is free to retry.
  "response_body" text NOT NULL DEFAULT '',
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  PRIMARY KEY ("owner", "key")
);
