# Charging GraphQL API (P3-05)

`fleetState.observationId` is an opaque runtime/revision identity. Authenticated
proposers must send it as `expectedObservationId` on both submission inputs.
Omission, null, fabrication, a prior runtime, or a changed simulator revision is
an audited creation denial. This check runs inside the production fleet service
lock alongside safety admission and persistence; it does not depend on the demo
gateway. An identity grants no authentication, approval, or execution authority.

The input field remains optional for existing authenticated operator workflows.
Supplying it opts those workflows into the same check; proposers cannot omit it.
The TypeScript agent requires identities on live reads and transport submissions.
Historical snapshots without identities remain usable for offline evaluation only.

## P4-03 job recovery

`fleetState.jobs` and `fleetState.reassignmentCandidates` are detached read-only
data from the same locked live snapshot as incidents and telemetry. Go computes
candidate admission; this does not reserve routes or guarantee delivery.
`submitReassignment(input: ReassignUnstartedJobInput!)` accepts `jobId`,
`fromRobotId`, `toRobotId` and `expiresAt`, using the same proposer/operator roles,
independent operator approval and audit lifecycle as SetCharging. Proposal reads
add `jobId`, `fromRobotId` and `toRobotId`; irrelevant action-specific ID fields
are empty strings. Existing SetCharging fields and mutation remain compatible.
See [P4-03 workflow and verification](../../agent/P4-03.md) for queries, safety
preconditions and the test-only complete operator-approval demonstration.
The React dashboard is unchanged and awaits P4-04.

## P4-01 current incidents

The additive `fleetState.activeIncidents` field is read-only and uses the existing
authentication boundary (viewer, proposer or operator). It returns `robotId`,
`type`, `severity` and `telemetry` (the affected `Robot` fields). The fleet service
calls the existing Go incident detector under its lock using the same snapshot as
the surrounding fleet state. No simulator operations or database writes occur.

```graphql
query {
  fleetState {
    tick
    activeIncidents {
      robotId type severity
      telemetry { id x y battery status jobId waitTicks blocked }
    }
  }
}
```

These are current conditions only: `low_battery` requires battery <30% and an
assigned job (high below 15%); `robot_unresponsive` requires offline status (high);
`traffic_blocked` requires the blocked flag (high above 40 wait ticks, medium
otherwise). Normal waiting is not an incident. The detector's allocated episode
IDs are not exposed because this integration retains no history across reads.
Results are deterministic and ordered by numeric robot ID, then incident type.
An empty list means no active conditions; detector failures produce the existing
sanitized query error, not a misleading empty list. Existing mutations, approval,
safety and execution rules are unchanged.

The server exposes `/graphql` as authenticated JSON POST and keeps `/healthz`.
The adapter depends on fleet service interfaces and detached read DTOs. The only
mutations are `submitProposal`, `submitReassignment`, `approveProposal`, and `rejectProposal`.

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
ticket. Read state remains available after the charging mutation. Automatic ticking
is now available as an opt-in server setting: `FLEETOPS_AUTO_TICK=1`, with
`FLEETOPS_TICK_INTERVAL=500ms` by default. Keep it disabled for this stationary
charging walkthrough. See [live runtime instructions](../../docs/live-runtime.md).

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
