# LedgerLock

A Go banking API that serves the same definition over **both gRPC and REST**, with
money transfers that stay correct when they run at the same time.

Users sign up, hold accounts in a currency, and transfer between them. The
interesting part is not the CRUD — it is keeping two balances consistent under
concurrent transfers, and doing it without deadlocking.

---

## Two protocols, one definition

`proto/service_bank.proto` is the single source of truth. From it:

- **gRPC** — `CreateUser`, `LoginUser`, `UpdateUser`, for service-to-service callers
- **REST** — the same handlers exposed over HTTP by [grpc-gateway](https://github.com/grpc-ecosystem/grpc-gateway),
  so a browser or `curl` needs no gRPC client
- **Swagger UI** — `doc/swagger/bank.swagger.json` is generated from the same proto
  (`protoc --openapiv2_out`), bundled with the Swagger UI assets and embedded into the
  binary by `statik`, then served at `/swagger/index.html`. The running service
  documents itself; there is no separate docs deploy to drift out of sync.
  The same spec is also published at
  [SwaggerHub](https://app.swaggerhub.com/apis/IvyWang/bank/1.0) if you want to browse
  the API without running anything.

A second, Gin-based HTTP server carries the account, transfer, and session endpoints.
**`main.go` currently starts the gateway and gRPC servers, not the Gin one** (see the
swap point in `runGrpcServer`'s caller), so those routes are reachable when you run Gin
locally but are not exposed by the deployed service. The health endpoints below are
registered on both listeners for exactly that reason.

## Concurrency: the part that needed care

A transfer touches two account rows. Two transfers running at once can interleave
and lose an update, and the naive fix — lock both rows — deadlocks the moment two
transfers move money in opposite directions between the same pair.

Three things prevent that:

1. **`SELECT ... FOR NO KEY UPDATE`** when reading the balance. `FOR UPDATE` would
   also block the foreign-key checks that `INSERT INTO transfers` needs; `FOR NO KEY
   UPDATE` takes a weaker lock that still serializes balance writes.
2. **Consistent lock ordering.** `TransferTx` always updates the lower account ID
   first (`db/sqlc/store.go`), so two opposing transfers acquire locks in the same
   order and one waits instead of both deadlocking.
3. **`CHECK (balance >= 0)` on `accounts`.** Checking the balance in Go before
   calling `TransferTx` would be racy: two concurrent transfers can both read a
   sufficient balance and both proceed. Only the database sees the serialized
   result, so the guarantee belongs there. The handler maps that constraint
   violation to a `400` — a client asking to overdraw is a client error, not a
   server fault.

None of it is left to reasoning — all three are pinned by tests:

- `TestTransferTx` runs N concurrent transfers and checks the final balances and
  that every transfer record exists
- `TestTransferTxDeadlock` runs transfers in both directions between the same two
  accounts and asserts the run completes
- `TestTransferTxRejectsOverdraft` asserts one oversized transfer is refused and
  that the rollback leaves both balances untouched
- `TestTransferTxConcurrentOverdraftLeavesBalanceNonNegative` fires 20 concurrent
  transfers of 10 at a balance of 100. Each is individually affordable, so an
  application-level check would let them all through — exactly 10 succeed, the
  balance lands on 0, and it never goes negative

## Retries: idempotency on `POST /transfers`

A client whose request times out does not know whether the transfer happened. Retrying
is the only sane thing for it to do, and without protection the retry moves the money
again.

Send an `Idempotency-Key` header and the retry returns the original result instead:

```bash
curl -X POST localhost:8080/transfers \
  -H "Authorization: Bearer $TOKEN" \
  -H "Idempotency-Key: 8f14e45f-ea6d-4b1e-9a2f-0c3d1b7a9e55" \
  -d '{"from_account_id":1,"to_account_id":2,"amount":100,"currency":"USD"}'
```

**The claim is inserted before the transfer, in the same transaction.** That ordering is
the whole design. Asking "has this key been used?" and *then* transferring is the same
race as checking a balance before debiting it — two concurrent retries both see no key
and both move the money. Inserting the claim first hands the mutual exclusion to the
primary key on `(owner, key)`: the second request blocks on the index until the first
commits, then fails to insert, and that failure is what triggers the replay.

`TestIdempotentTransferTxConcurrentRetriesMoveMoneyOnce` fires 8 simultaneous requests
carrying one key. Exactly one succeeds and the balance moves once.

Three details that follow from the design:

- **Keys are scoped to their owner.** One caller's key cannot collide with, or read
  back, another caller's result.
- **A failed transfer releases the key.** The claim rolls back with the transfer, so a
  retry can try again once the cause is fixed. The key exists to stop a *successful*
  transfer happening twice, not to make a failure permanent.
- **The same key with different parameters is a `409`**, not a replay. A stored response
  to a different request would be an answer to a question nobody asked. That is what
  `request_hash` is for.

The header is optional, so existing callers keep working — a caller that omits it gets
no replay protection. On a greenfield API this endpoint would require it.

A replayed response carries `Idempotent-Replay: true`.

## Auth

Sessions are **Paseto** tokens, not JWT. Paseto has no algorithm-negotiation field,
so the `alg: none` and algorithm-confusion classes of JWT bug are not reachable —
`TestInvalidJWTTokenAlgNone` demonstrates the attack against the JWT maker that Paseto
structurally cannot have.

Both are implemented behind one interface:

```go
type Maker interface {
    CreateToken(username string, duration time.Duration) (string, *Payload, error)
    VerifyToken(token string) (*Payload, error)
}
```

`NewPasetoMaker` is what the servers use; `NewJWTMaker` exists and is swappable at
one line in `api/server.go`. Access tokens are short-lived and renewed through
`POST /tokens/renew_access` against a stored refresh session.

## Sessions and signing out

Logging in stores a refresh session; `POST /tokens/renew_access` trades a refresh token
for a new access token and refuses a session that is blocked, expired, mismatched, or
owned by somebody else.

`is_blocked` had been read from the start and never written, so there was no way to sign
out. `POST /users/logout` revokes one session — the device whose refresh token you send —
and `POST /users/logout_all` revokes every session the caller holds, for when a password
may be compromised. Both verify that the session belongs to the authenticated user, so a
refresh token picked up elsewhere cannot be used to revoke someone else's session.

Sessions are blocked rather than deleted, which keeps the user agent and client IP of a
signed-out session inspectable afterwards.

**What logout does not do:** it stops refresh, not the access token already in the
caller's hands. Access tokens are stateless, so invalidating one would need a denylist
consulted on every request — giving up the property that makes stateless tokens worth
having. The window is bounded by `ACCESS_TOKEN_DURATION` instead, which is 15 minutes.

## API surface

**REST (Gin)**

| Method | Path | Auth | Notes |
| --- | --- | --- | --- |
| POST | `/users` | — | |
| POST | `/users/login` | — | |
| POST | `/tokens/renew_access` | — | |
| POST | `/accounts` | Paseto | |
| GET | `/accounts/:id` | Paseto | |
| GET | `/accounts` | Paseto | |
| POST | `/transfers` | Paseto | accepts `Idempotency-Key` |
| POST | `/users/logout` | Paseto | revokes the session for the supplied refresh token |
| POST | `/users/logout_all` | Paseto | revokes every session the caller holds |
| GET | `/health/live` | — | also on the gateway listener |
| GET | `/health/ready` | — | also on the gateway listener |

Authorized routes go through a Gin middleware; the gRPC side does the equivalent in
`gapi/authorization.go` by reading metadata.

**gRPC / gRPC-Gateway** — `CreateUser`, `LoginUser`, `UpdateUser`

## Data model

- `doc/db.dbml` — schema as DBML, rendered at [dbdiagram.io](https://dbdiagram.io)
- `doc/schema.sql` — generated SQL
- Migrations with `golang-migrate`; queries are hand-written SQL in `db/query/`,
  and **`sqlc` generates the typed Go** from them. No ORM, no runtime string building,
  and a query that does not compile fails at build time rather than in production.

## Running it

```bash
cp app.env.example app.env   # then fill in a 32-character token key
make postgres                # start Postgres in Docker
make createdb                # create the database
make migrateup               # apply migrations
make server                  # run the API
```

```bash
make test          # go test ./... with coverage
make mock          # regenerate gomock doubles
make proto         # regenerate Go + gateway + swagger from proto/
make db_docs       # render doc/db.dbml
make evans         # interactive gRPC client
```

Config is read by `viper` from `app.env`, which is **not** in the repo —
`app.env.example` documents the keys. When no file is present `LoadConfig` falls back
to environment variables, which is how CI and the production pods run; see
Deployment below.

## Tests

`testify` for assertions, `gomock` for the store double, so handler tests run without
a database. The `db/sqlc` tests are integration tests and do need a live Postgres.

| Package | Coverage | What it covers |
| --- | --- | --- |
| `val` | 100% | username / name / password / email rules |
| `health` | 100% | liveness and readiness, including the 503-not-500 distinction |
| `gapi` | 92% | the three gRPC handlers, auth, logging interceptors |
| `db/sqlc` | 86% | queries, `TransferTx`, and the concurrency and idempotency tests above |
| `token` | 78% | Paseto and JWT makers, expiry, the `alg: none` attack |
| `api` | 85% | REST handlers, including every `POST /transfers` rejection and replay path |
| `util` | 61% | config loading, password hashing |

`POST /transfers` is covered for every way it can refuse: an account the caller
does not own, either side's currency mismatching, the same account on both sides, a
non-positive amount, a missing account, and an overdraft. A `check_violation` from
some *other* constraint is asserted to stay a `500`, so the overdraft mapping cannot
silently swallow an unrelated database error.

The `gapi` suite is where the security-relevant assertions live: a valid token for one
user is rejected when it tries to edit another (`PermissionDenied`, not a silent
success), an expired token and a token signed with a different key are both rejected,
a password update is verified to be hashed rather than stored in the clear, and a
partial update is verified not to blank the fields it omitted.

CI runs the whole suite against a Postgres service container on every push and pull
request (`.github/workflows/test.yml`).

```bash
make test                     # go test -v -cover ./...
go test ./... -race -cover    # same suite under the race detector
```

## Deployment

- **Docker** — 2-stage build; the runtime image is `alpine` carrying the compiled
  binary, the migration files, and the entrypoint scripts, not the Go toolchain
- **Self-migrating** — `main.go` runs `migrate.Up()` against `MIGRATION_URL` on
  startup, so a new deploy brings the schema with it instead of needing a separate
  migration step in the pipeline. `docker-compose.yaml` fronts it with `wait-for.sh`
  so the container does not race Postgres coming up
- **Probes** — `livenessProbe` hits `/health/live`, which never touches Postgres: a
  failing liveness probe restarts the pod, and restarting every replica during a
  database outage turns it into a crash loop. `readinessProbe` hits `/health/ready`,
  which does check Postgres, because failing it only pulls the pod from the Service —
  the right response to an unusable dependency. Verified by killing Postgres under a
  running container: liveness stayed `200`, readiness went `503`, and it returned to
  `200` on recovery
- **Resource requests and limits** — set, so the scheduler can place the pod and one
  replica cannot starve its node
- **AWS EKS** — manifests in `eks/`: `deployment.yaml`, `service.yaml`,
  `ingress.yaml`, `issuer.yaml`, `aws-auth.yaml`
- **TLS** — terminated at the ingress, with certificates issued automatically by the
  cluster issuer rather than mounted by hand
- **Configuration is not in the image.** The Dockerfile does not copy `app.env`, so
  the built image carries no secrets and nothing sensitive sits in the registry.
  In production the deploy workflow syncs the values from **AWS Secrets Manager**
  into a Kubernetes Secret, which the deployment reads via `envFrom`. Locally the
  same binary reads `app.env`. `docker build` works from a clean clone with no
  config file present.

## Scope

Deliberately not here: rate limiting, caching, and horizontal sharding. This is one
service with one database, and the goal was correctness under concurrency and a clean
dual-protocol contract — not throughput. Adding a cache in front of balances would
have to answer invalidation on every transfer, which is a different project.

Spent idempotency keys are never pruned. A real deployment would expire them on a
window long enough to outlive any client's retry budget; there is no such job here.

## Stack

Go · Gin · gRPC · grpc-gateway · Protocol Buffers · PostgreSQL · sqlc · golang-migrate ·
Paseto · viper · zerolog · testify · gomock · Docker · AWS EKS
