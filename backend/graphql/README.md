# Charging GraphQL API (P3-05)

The server exposes `/graphql` as authenticated JSON POST and keeps `/healthz`.
The adapter depends on fleet service interfaces and detached read DTOs. The only
mutations are `submitProposal`, `approveProposal`, and `rejectProposal`.

## Run

Use Go 1.27.1 and the existing Compose PostgreSQL (`docker compose up -d --wait postgres`).
Export `FLEETOPS_DATABASE_URL` privately in the server process; Go does not read
`.env`. Startup pings PostgreSQL and applies the embedded migrations.
Set `FLEETOPS_AUTH_TOKENS` to a JSON array containing `subject`, `role`, and
`sha256` (64 hex digits representing SHA-256 of the bearer token). Only these
three fields are accepted. Supported roles are `viewer`, `proposer`, `operator`.
Use independently generated cryptographically random 32-byte tokens, encoded as
hex, with distinct subjects for the proposer and approving operator. Keep the
plaintext tokens privately in client processes; do not print them, check them
in, or put them in URLs. The server stores only hashes in its auth configuration.
Hash configuration is mandatory and is not logged on failure.

For a local station-ready demonstration, also export `FLEETOPS_CHARGING_DEMO=1`.
This explicit profile places R1 at S1, disables initial jobs and dispatch, and
leaves simulator defaults and safety contracts unchanged. Start with
`cd backend; go run ./cmd/server`. No background ticking is introduced by this
ticket. Read state remains available after the charging mutation.

Send `Content-Type: application/json` and `Authorization: Bearer <private token>`.
Use a proposer token for submission, an independent operator token for decisions,
and any configured role for reads. Operators may also submit, but cannot approve
their own proposals (identity is the subject, even across different tokens).
Only operators may reject. Self-rejection is permitted.

```graphql
query {
  fleetState { tick poisoned robots { id battery status x y } stations { id x y occupiedBy } }
}

mutation Submit($input: SetChargingInput!) {
  submitProposal(input: $input) {
    proposalId state execution auditCommitted executionAttempted error
  }
}
# variables: {"input":{"robotId":"R1","stationId":"S1","expiresAt":"<future RFC3339 UTC timestamp>"}}

query Status($id: ID!) {
  proposal(id: $id) { id state execution submittedBy currentRuntime expiresAt }
}

mutation Approve($id: ID!) {
  approveProposal(id: $id) { proposalId state execution auditCommitted executionAttempted error }
}

mutation Reject($id: ID!) {
  rejectProposal(id: $id) { proposalId state auditCommitted error }
}
```

Each HTTP body is `{ "query": "...", "variables": { ... } }`. Use one operation
and one mutation field per request. Domain denials return an `error` in the
mutation result; check it along with `auditCommitted`. HTTP 200 alone is not
success. `executionAttempted` describes this call; `execution` can describe an
earlier successful execution on a duplicate attempt. Unknown commit outcomes
preserve `auditCommitted=false` and poison the runtime; manual reconciliation is
required. Arbitrary dependency errors and credentials are not sent to clients.

## Boundaries

Every well-formed authenticated mutation reaches the existing fleet service,
including role denials, which are committed as decision-attempt audit events.
Unauthenticated or malformed transport/schema requests are rejected before a
proposal operation and do not create database decision events. Viewer submission
is denied in the service. Operator authorization is bound to the verified context
and rechecked by the existing execution gate. Trusted in-process P3-04 callers
retain their existing API; unauthenticated callers cannot enter through HTTP.

Migration 003 adds immutable service-managed submitter provenance without changing
P3-01 proposal contracts. No approval/revalidation logic was moved into resolvers.
Expired/rejected/duplicate/stale requests cannot execute; the existing battery
minimum and at-station rule still apply. Expiration is evaluated by the existing
service on a decision; reads show the persisted state, with its expiration time.
No scheduler changes proposal status in the background.

A fresh service owns a fresh simulator runtime. Prior-runtime proposals remain
readable (`currentRuntime=false`) but cannot be approved or executed. They are
not resumed or restored. Runtime poisoning and uncertain commit semantics from
P3-04 are preserved. No durable audit is claimed until the transaction commits.
There are no Tick, Commander, Driver, fault injection, or alternative charging
command fields. Introspection is disabled; the schema file is available locally.

Requests are bounded to 64 KiB, query text to 16 KiB, parser tokens to 1000,
expanded selections to 100, nesting to 8, and gqlgen complexity to 200. Fragment
expansion is bounded before schema validation/complexity analysis. Only JSON POST
is supported; no batches, GET, multipart uploads, or subscriptions. Operation
contexts have a 10-second deadline; the HTTP server also bounds reads/writes and
headers. Hash lookup uses constant-time digest comparison. This minimal static
configuration has no token issuance/rotation endpoint. For deployment, terminate
HTTPS at the trusted deployment boundary; local HTTP is intended for the demo.

## Verification and generation

`make test-integration` includes real PostgreSQL + HTTP tests under the race
detector. Tests create isolated schemas, skip without `FLEETOPS_TEST_DATABASE_URL`,
and fail when it is missing and `FLEETOPS_REQUIRE_DB=1`.

The pinned stable release is [gqlgen v0.17.95](https://github.com/99designs/gqlgen/releases/tag/v0.17.95),
verified against the official release listing. From `backend`, regenerate with
`go tool gqlgen generate --config graphql/gqlgen.yml`. Generated schema execution
and model files are checked in; generator tooling is pinned with Go's tool directive.
No proposal persistence or simulator contracts are replaced by generated code.
