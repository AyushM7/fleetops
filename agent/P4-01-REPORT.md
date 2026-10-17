# P4-01 implementation and verification report

Completed 2026-10-16 on `phase4-agent`. Existing P4-00 working-tree changes were
preserved. No commit, push, merge or branch change occurred. No P4-02 work started.

## Implementation

The only schema addition is `FleetState.activeIncidents` and its read DTO type.
The fleet service calls the existing Go `incident.Reconcile` against the same
locked simulator snapshot used for fleet telemetry. GraphQL exposes affected
robot, incident type, severity and supporting robot telemetry through the existing
authenticated read. No service or mutation signatures changed.

The agent consumes Go classifications without a TypeScript incident detector.
Structured incident records and operator-attention items are distinct from action
recommendations. Unsupported incident recovery produces no robot proposal.
The existing eligible SetCharging demonstration still works with explicit
submission opt-in. A fresh read bounds proposal targets to the host recommendation;
Go remains responsible for validation, authorization, human approval and execution.

There is no new persistence, simulator behavior, fault endpoint, robot command,
approval capability, Commander/Driver/Tick tool, or paid model request.

## Files changed in P4-01

This list describes P4-01 changes on top of the uncommitted P4-00 baseline.
Earlier package/lockfile, scaffold and configuration changes remain intact.

| Area | Files |
| --- | --- |
| Go service | `backend/fleet/read.go`, `backend/fleet/incidents_test.go` |
| GraphQL contract and adapter | `backend/graphql/schema.graphqls`, `backend/graphql/schema.resolvers.go` |
| Regenerated gqlgen bindings | `backend/graphql/generated.go`, `backend/graphql/model/models_gen.go` |
| GraphQL tests and documentation | `backend/graphql/incidents_test.go`, `backend/graphql/README.md` |
| Agent contracts and transport | `agent/src/contracts.ts`, `agent/src/graphql.ts`, `agent/src/fixtures.ts` |
| Agent analysis and tools | `agent/src/observe.ts`, `agent/src/tools.ts`, `agent/src/agent.ts` |
| Agent tests | `agent/src/agent.test.ts`, `agent/src/incidents.test.ts`, `agent/src/backend.integration.test.ts` |
| Integration harness | `agent/scripts/verify-integration.ps1`, `agent/scripts/check-incident-fixture.mjs` |
| Agent documentation | `agent/README.md`, `agent/DEPENDENCY-AUDIT.md`, `agent/P4-01-REPORT.md` |

## Final verification

Agent checks used **Node.js v24.21.0**, TypeScript 7.0.2 and Vitest 5.0.2.
Backend checks used Go 1.27.1; race checks used the existing Linux Go Docker image
and isolated PostgreSQL 18.6. Database-required checks ran with
`FLEETOPS_REQUIRE_DB=1`.

| Command/check | Actual final result | Exit code |
| --- | --- | --- |
| `npm test` | 3 files passed; 39 tests passed | 0 |
| `npm run typecheck` | No TypeScript errors | 0 |
| `npm run build` (inside harness) | TypeScript compilation succeeded | 0 |
| `go vet ./incident/... ./fleet/... ./graphql/... ./cmd/server/...` | No diagnostics | 0 |
| `go test -p 1 -parallel 1 -race ./incident/... ./db/... ./fleet/... ./graphql/... ./cmd/server/... -count=1` | All five suites passed, including PostgreSQL integration | 0 |
| `npm run test:integration` (inside harness) | 1 file passed; 4 tests passed against the real Go server and PostgreSQL | 0 |
| `go test ./graphql/... -run '^TestAgentIncidentHTTPClient$' -count=1 -v` | Passed the Go-to-TypeScript incident HTTP contract test | 0 |
| `pwsh -NoProfile -File scripts/verify-integration.ps1` | Completed successfully; temporary server and database container cleaned up | 0 |
| Whitespace validation | Tracked and untracked files passed; Windows CRLF accepted | 0 |
| `npm audit --omit=dev --json` | 5 moderate; 0 high; 0 critical | 1 |

The cross-language test printed:

```text
PASS: 6 Go-classified incidents; boundary and healthy cases; coherent telemetry; deterministic agent consumption; zero mutations or model requests.
```

Fault cases use trusted Go test snapshots and the existing detector behind the
production GraphQL handler. They do not create a production fault-injection path.
The separate PostgreSQL-backed tests exercise actual simulator snapshots and the
existing SetCharging proposal workflow, with no execution on submission.

The usage-limit rejection prevented the final unit rerun and the subsequent
vet command from executing before resumption. Both were completed successfully
after resumption. Earlier integration attempts hit migration-lock deadlines under
parallel test execution. The harness now serializes packages and `t.Parallel`
cases. Explicit concurrent-migration worker tests still run their own workers and
passed; production database timeouts and implementation were not changed.

## Dependency findings

No dependencies or versions were changed for P4-01. The affected runtime chain is
`@mastra/core@1.75.0 → gray-matter@4.0.3 → js-yaml@3.15.2 → argparse@1.0.10 → sprintf-js@1.0.3`.
These are five propagated findings for one underlying advisory, not development-only
findings. No sprintf-js patch is published; parent major-version alternatives
require compatibility review. The latest verified stable Mastra release still
retains the chain. No force fix or downgrade was performed.

Source tracing indicates that the vulnerable sprintf formatting path is reached
through the separate YAML CLI, not the library path used here. FleetOps enables
no Mastra skills/workspace or shell tool. This is a scoped reachability assessment,
not a claim that the installed packages are patched. See
[DEPENDENCY-AUDIT.md](DEPENDENCY-AUDIT.md) for versions, sources and alternatives.

## Running and known limitations

From `agent/`, privately configure the existing ignored `.env` with a proposer-only
token and start the updated backend. Run `npm run build`, then `npm start`.
Current incidents are included automatically. Submission remains disabled unless
`--submit` is supplied. The stationary charging demo still uses `--demo --submit`;
live provider execution separately requires `--live` and a provider key, can incur
charges, and was not requested or run for verification. See [README.md](README.md).

This is current-state detection only. There is no persisted incident history,
episode identity, acknowledgement or resolution lifecycle in the API. Existing
SetCharging cannot recover low-battery/job, offline or blocked-traffic incidents;
operator attention is the supported recommendation. Future recovery commands,
durable cross-run proposal deduplication and model-quality evaluation remain
outside this ticket. No requirements were guessed. **Real model API calls: zero.**
