# PostgreSQL foundation

`ConfigFromEnv` reads `FLEETOPS_DATABASE_URL` from the process environment. Go does
not load `.env` files. Keep URLs private and URL-escape credentials. `Open` creates
and pings a pgx pool. Defaults are eight maximum connections, a five-second
connection timeout, and a five-second query timeout. Typed configuration can
override these values. Queries use client context deadlines and a PostgreSQL
`statement_timeout`; earlier caller deadlines still apply. `QueryRow` callers
must call `Scan`. Always close pools.

Configuration formatting and returned errors redact connection strings, SQL and
server details. Do not log `DatabaseURL`, raw pgx errors, environment contents or
SQL arguments. Database URLs are excluded from configuration JSON.

## Migrations

Call `Pool.Migrate(ctx)` explicitly; opening a pool does not migrate. SQL files
are embedded from `migrations/`. Names are positive numeric versions followed by
an underscore and a lowercase description, such as `002_example.sql`. Versions
are sorted numerically and must be unique. Applied versions, filenames and
SHA-256 checksums of exact file bytes are retained in `schema_migrations`.
Migration SQL is checked out with LF line endings through the local
`.gitattributes`, keeping those bytes consistent across Windows and Linux.
Missing, renamed or modified applied migrations and backfilled versions are
refused. Add a higher version for every change; no down migrations are supported.

One PostgreSQL session advisory lock covers history validation and the complete
run, including the initial history-table creation. Locks are scoped by schema
within a database. Each migration's SQL and history insertion share one
transaction. A failed migration rolls back; earlier committed migrations remain.
Cancellation cleanup uses a separate bounded context and discards a connection
if its advisory lock cannot be released. Migration SQL is trusted repository
code: it must not include transaction-control statements or DDL that cannot run
in a transaction. The initial embedded migration documents the history table.
Migration 002 adds the minimal `fleet_proposals` and `fleet_audit` tables used by
the charging workflow in `backend/fleet`.
Migration 004 replaces only `fleet_proposals_payload_check` with the two-action
allowlist (`set_charging`, `reassign_unstarted_job`). Existing proposal/audit JSON,
execution history, state consistency and the migration ledger are preserved.
The new request is stored in the immutable proposal's `Reassignment` value;
historical SetCharging `Request` payloads remain readable without rewriting.
There is no job-state or incident persistence migration.

pgx is pinned to **v5.11.0**, verified against the
[official stable release](https://github.com/jackc/pgx/releases/tag/v5.11.0).
Lock semantics follow the
[PostgreSQL advisory-lock documentation](https://www.postgresql.org/docs/18/explicit-locking.html#ADVISORY-LOCKS).

## Integration tests

Start the existing `postgres:18.6` service with `make up`. Privately export
`FLEETOPS_TEST_DATABASE_URL` for that instance, then run `make test-integration`.
This target runs database integration tests with Go's race detector. Windows
users should use the repository's documented WSL toolchain. The test role needs
permission to create and drop schemas; use a development/test database only.
Each test creates a random `fleetops_test_...` schema with an independent search
path and migration ledger, then closes its pools and drops only that schema.
Tests do not drop databases or Compose volumes.

Without a test URL, integration tests skip locally. `FLEETOPS_REQUIRE_DB=1` makes
that condition fail. A supplied URL that cannot connect always fails. The runtime
URL is never used as a fallback. Unit tests run without PostgreSQL.

CI adds an independent `integration` job using `postgres:18.6`, required database
verification and the race detector. Its ephemeral service uses passwordless
authentication on a port bound only to runner loopback; it has no committed
credential. The existing `test` and `gitleaks` jobs remain intact, including the
enabled Phase 2 S3/seed99 soak failure. No waiver or CI split is introduced.

## Pending Phase 3 integration decisions

Simulator-tick expiration and low-battery `SendToCharge` support remain pending
architectural proposals from Claude's plan. Neither changes the existing safety,
proposal or simulator contracts. Existing wall-clock lifecycle expiration and
SetCharging behavior remain unchanged. The `backend/fleet` service now combines
approval and execution in one controlled operation and persists proposals and
audit. PostgreSQL and simulator memory do not share a rollback mechanism; the
service poisons its runtime after uncertain persistence. Broader command support
and transport integration remain future work.
