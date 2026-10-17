# P4-03 implementation report

## Stage 1: demonstrated before production implementation

`TestReassignmentRecoveryScenarioProof` passed (exit 0). Its private trusted
fixture retains automatic dispatch, navigation, charging and recovery. R1 holds
J1 before pickup; offline R2 occupies the only exit from R1's pocket. The Go
traffic-blocked condition develops through ordinary ticks.

At the identical tick-42 observation: R1 is moving with J1, battery 100%,
waitTicks 42, blocked true. Observation continues for 200 ticks, covering 66
replanning opportunities and more than 13 recovery thresholds. The unchanged
baseline at tick 242 still has J1 assigned to R1, battery 100%, waitTicks 242.
Healthy-cycle recovery cannot move the offline obstruction. This is a blocked
traffic scenario, not a low-battery incident.

The Stage 1 counterfactual changes ownership only in test setup and asks the
existing scheduler to admit a replacement. R3 is selected and delivers at tick
45 through ordinary navigation. Collision, exclusive-aisle, ownership, station
and energy invariants pass after every tick. Repeated baseline runs match.
The production command was added only after this proof succeeded.

## Stage 2: implementation

- One new command, `ReassignUnstartedJob(jobId, fromRobotId, toRobotId)`, atomically
  clears source ownership/navigation/wait state and assigns the uncollected job
  to the recipient. Execution success means transfer; normal ticks deliver.
- Read-only `PreviewReassignment` shares physical admission with the command.
  Both robots must be healthy/available and battery >=30%. The source must own an
  assigned uncollected job, be before pickup and currently blocked. The recipient
  must be idle/jobless, without a pending offline transition or charging claim.
- Admission reuses A* and blocks other robot cells and occupied aisles for pickup,
  delivery and a free charging exit. Energy covers all legs plus configured
  reserve and assignment minimum. The admitted pickup route is installed. In the
  fixture the costs are 1 pickup + 2 delivery + 5 charging-exit steps, plus reserve
  5 = 13 energy units; the separate 30% command floor still applies.
- Proposal requests remain immutable comparable values. The existing create,
  independent approval, final execution gate, runtime binding, row locking,
  auditing and poison-on-persistence-failure path dispatches only the two known
  action types. Mixed/unknown action payloads fail closed. SetCharging is retained.
- GraphQL adds typed `submitReassignment`, current `jobs` and Go-admitted
  `reassignmentCandidates`, and action-specific proposal read fields. All use the
  existing authentication boundary. There is no Tick, Commander, Driver or
  injection mutation. Irrelevant proposal ID fields are empty; inspect `action`.
- Mastra consumes Go incident/job/candidate evidence. The host validates exact
  snapshot references, job/source/recipient/candidate telemetry and command type.
  Offline recommendations choose the first Go candidate deterministically.
  Before explicit opted-in submission the host requires a fresh identical full
  snapshot. Go rechecks current eligibility at creation, approval and execution.
  The model's tool map still contains only `readFleetState`.

The seven frozen rules remain unchanged: approval/authentication gates every
execution; both robots meet availability and battery restrictions; transfer
preserves single ownership; routes exclude occupied aisles; charging station
checks remain enforced; rejected/expired requests do not execute; attempts use
the existing audit transaction. Persistence failures retain fail-closed behavior
and never falsely claim a durable audit or a safe retry.

## Persistence

Forward-only migration `004_reassign_unstarted_job.sql` replaces only the original
`fleet_proposals_payload_check` with an action allowlist for `set_charging` and
`reassign_unstarted_job`. It preserves state consistency, all historical JSON,
timestamps, execution outcomes, submitter identity and audits. New requests use
the proposal's `Reassignment` value; old SetCharging `Request` records decode with
an empty reassignment value. A real PostgreSQL upgrade test applies versions
001–003, inserts historical charging success, applies 004, checks history is
unchanged, admits reassignment and rejects unknown actions/inconsistent states.
No job or incident persistence, migrations to frozen rules, or simulator history
restoration is introduced.

## Verification

Toolchain: Node.js 24.21.0, Go 1.27.1, PostgreSQL 18.6. No dependencies or lockfiles
changed. Existing Mastra 1.75.0, Zod 4.6.7 and gqlgen 0.17.95 are retained; GraphQL
bindings were regenerated with the existing pinned gqlgen tool.

| Check | Result | Exit |
| --- | --- | --- |
| Stage 1 deterministic recovery proof | Passed; baseline tick 242 vs delivery tick 45 | 0 |
| `npm test` | 82 tests, 7 files passed | 0 |
| `npm run typecheck` | Passed | 0 |
| `npm run build` | Passed | 0 |
| Go all packages, `-race -tags fleetops_test -p 1 -parallel 1` | All 12 packages with tests passed, PostgreSQL required | 0 |
| `go vet -tags fleetops_test ./...` | Passed | 0 |
| Real-backend agent integration | 5 tests passed | 0 |
| Existing Go incident HTTP/Mastra contract | 6 Go-classified conditions; local provider adapter; passed | 0 |
| Full `agent/scripts/verify-integration.ps1` harness | Passed, including Go race/regression and both Node contract checks | 0 |
| Real Go -> local Mastra -> opted-in pending proposal -> independent GraphQL approval | Approve/reject/expire/stale subtests passed; delivery vs blocked baseline verified | 0 |
| Whitespace: `git diff --check` plus all new-file text checks | Passed | 0 |

Coverage includes command atomicity and subsequent completion; no transfer before
approval; identical baseline comparison; rejection/expiry/stale ownership;
already-reached pickup/collected job; both battery restrictions; unavailable/busy
recipient; pending offline transition; energy; occupied pickup/delivery/aisles/
charger; reserved charger; duplicate ownership; duplicate and unauthorized/self
approval; execution-boundary authorization/expiry; submission commit failure;
post-transfer deferred COMMIT failure and committed-but-unacknowledged outcome.
Both uncertain cases poison the service, prevent ticks/replay, and report unknown
execution without claiming committed audit. Existing SetCharging tests pass.

The full simulator suite passes unchanged, including default and warehouse
goldens and all 30 S1–S4 Phase 2 acceptance checks (including S3/seed99). Windows
checkout CRLF makes the byte-comparison golden tests fail when run directly;
the harness normalizes only an ephemeral Linux container copy of those two JSON
fixtures to LF. Repository golden fixtures and simulator dynamics are unchanged.
Initial verification caught and repaired the migration constraint target and a
test-only loopback URL comparison. A sandbox temporary-directory error in Vitest
was resolved by running the same deterministic tests with approved execution
outside that temp-directory restriction. No auto-review refusal remains.
The existing Makefile integration target now enables `fleetops_test`, keeping
the trusted recovery fixture and approval tests in the existing integration CI
path. The full tagged race suite above covers that Go test selection; no CI run
or remote result is claimed.

## Run, limitations and scope

See [workflow, commands and demonstration](P4-03.md). Use the existing private
proposer token. `npm start` reads/recommends; `npm start -- --submit` explicitly
permits one proposal attempt. A separate operator reviews and approves through
GraphQL. The complete test-only workflow runs with:

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File agent/scripts/verify-integration.ps1
```

Live provider setup is unchanged: explicit `--live`, the configured model and a
private provider API key are required; the CLI explains possible charges. Paid
requests default off. **No real/paid provider requests occurred.** Mastra generation
in verification uses a deterministic local adapter, and fault scenarios use
private test fixtures. `fleetops_test` must be used only for test builds; normal
production builds omit the fixture factory. No fault injection is exposed to
GraphQL or model tools, even in the fixture server.

Route admission is conservative and snapshot-scoped, not a route/charger
reservation or completion guarantee. The command does not clear the offline
obstruction or restore a robot; offline/low-battery incidents can still require
operator attention. Only pre-pickup jobs qualify. The simulator and lifecycle
remain in memory; restart/uncertain-commit reconciliation remains manual.
Snapshot incident references are not stable IDs and cannot be reused across
simulator runs. Natural-language model assessments remain untrusted interpretation.

The React dashboard was not changed and is not the reassignment approval surface
for P4-03. P4-04, new traffic behavior, autonomous reassignment, other commands and
frozen safety-rule edits were not started. No commits, pushes, merges or branch
changes were made. No requirement is intentionally left to a guess.

## Files changed

- `agent/P4-03-REPORT.md`
- `agent/P4-03.md`
- `agent/README.md`
- `agent/scripts/check-reassignment-fixture.mjs`
- `agent/scripts/verify-integration.ps1`
- `agent/src/agent.test.ts`
- `agent/src/agent.ts`
- `agent/src/contracts.ts`
- `agent/src/fixtures.ts`
- `agent/src/graphql.ts`
- `agent/src/incidents.test.ts`
- `agent/src/observe.ts`
- `agent/src/reasoning.test.ts`
- `agent/src/reasoning.ts`
- `agent/src/reassignment.test.ts`
- `agent/src/submission.ts`
- `agent/src/tools.ts`
- `backend/db/migrations_test.go`
- `backend/db/migrations/004_reassign_unstarted_job.sql`
- `backend/db/README.md`
- `backend/db/reassignment_integration_test.go`
- `backend/fleet/decision.go`
- `backend/fleet/read.go`
- `backend/fleet/README.md`
- `backend/fleet/reassignment_export_test.go`
- `backend/fleet/reassignment_graphql_test.go`
- `backend/fleet/reassignment_integration_test.go`
- `backend/fleet/service.go`
- `backend/graphql/generated.go`
- `backend/graphql/handler_test.go`
- `backend/graphql/inbox.go`
- `backend/graphql/model/models_gen.go`
- `backend/graphql/README.md`
- `backend/graphql/resolver.go`
- `backend/graphql/schema.graphqls`
- `backend/graphql/schema.resolvers.go`
- `backend/proposal/lifecycle.go`
- `backend/proposal/reassignment_test.go`
- `backend/proposal/types.go`
- `backend/safety/reassignment_test.go`
- `backend/safety/reassignment.go`
- `backend/sim/reassignment_fixture.go`
- `backend/sim/reassignment_scenario_test.go`
- `backend/sim/reassignment_test.go`
- `backend/sim/reassignment.go`
- `backend/sim/roles.go`
- `Makefile`
