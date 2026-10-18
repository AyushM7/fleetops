# AGENT-05: durable decision provenance

`agent-run-v1` records the `recovery-decision-v2` path. There is no inference endpoint, new simulator command, approval capability, or dashboard change.

## Persistence and identity

Migration `005_agent_runs.sql` adds `fleet_agent_runs`. Go issues a random run ID after authenticating a proposer and matching the supplied observation to the current service under its mutex. Runtime identity and owner come from Go and authentication, never from client metadata. Go saves a bounded projection of current incident classifications, admitted candidates, route/battery metrics, tick and poison status. This initial transaction precedes model investigation.

Each row has server start/completion timestamps, reported metadata, verified observation facts, optional completion, completion SHA-256, optional unique proposal foreign key, and the original creation receipt. A start without completion is explicitly incomplete, not successful. JSONB size checks, completion consistency checks, and the unique foreign key supplement service validation. The migration preserves existing proposals and audits.

The agent completes a run once, after investigation and TypeScript grounding. A completion can record a decision, abstention, rejection, timeout, cancellation, invalid tool/usage, budget exhaustion or other investigation failure. Arbitrary provider errors are replaced with enumerated categories. Known fresh-read failures are captured as a reported submission check.

## Atomic proposal association

`finishAgentRun` locks the service and existing run row. It checks authenticated ownership, current runtime, the exact original observation identity, and a bounded completion schema. Go independently checks the decision projection against the saved candidate facts: membership, metric values, relations, comparison coverage, objective, basis and abstention conditions. `serverVerifiedDecision` refers to these checks; it does not authenticate model inference or prove that the client ran its entire TypeScript validator.

For an explicitly authorized proposal, completion calls the same Go admission/audit implementation used by legacy submission. The current observation precondition and independent Go safety checks run inside the service mutex. Proposal insertion, audit insertion and run completion/link all share one database transaction. Required completion persistence or COMMIT failure produces no successful receipt. The existing lifecycle may already have advanced in memory, so uncertain proposal persistence poisons the runtime for reconciliation as before. No simulator command is invoked during run completion.

An exact replay by the same owner returns the original committed creation receipt. An altered completion, another owner, foreign runtime or foreign observation is rejected. The client cannot provide a proposal ID to attach. A run cannot be completed again to attach a later proposal. A unique proposal foreign key prevents multiple run associations. Distinct independently started runs are distinct operations; this is not global action deduplication.

Neither start nor completion is automatically retried. A lost start acknowledgement can leave an incomplete row. A lost completion acknowledgement can leave a committed run/proposal; authenticated retrieval supports reconciliation. Approval and execution retain the existing independent operator and Go preflight checks.

## Data, measurements and redaction

`agentReportedMetadata` contains contract/version, objective, reported source (`deterministic`, `mocked`, `recorded`, `live-model`) and a validated requested model identifier. The runner defaults string model configurations to `live-model`. Injected adapters require an explicit host source category (tests use `mocked`); Go does not certify these claims. An empty requested-model identifier means unavailable.

`agentReportedCompletion` contains accepted/rejected/failed status, enumerated failure, host-measured latency/request/tool counts, reported usage, an optional validated provider-reported model identity, a bounded sequence of request/tool names, submission check, and a typed decision projection. The full model response and full snapshot evidence are not stored. Comparisons retain exact admitted candidate tuples, metric names, integer values and relation enums. Tool arguments, raw outputs, prompts, reasoning prose, provider exceptions, API keys and bearer tokens are excluded. Unknown JSON fields are rejected.

Reported usage is `reported`, `partial` or `unavailable`. Missing measurements are null, never fabricated zeros. Counts and latency are host measurements; token/model fields are provider reports relayed by the host, not server-attested billing. Server timestamps and `serverElapsedMs` provide independent wall-clock duration. An empty reported actual-model identifier means unavailable. Estimated cost is always explicitly unavailable: this ticket introduces no pricing feed or assumed price. Request limits are not an exact billing guarantee.

## Retrieval and authorization

The existing authenticated GraphQL endpoint adds:

- `startAgentRun(expectedObservationId: String!, metadata: String!): AgentRun!`
- `finishAgentRun(id: ID!, completion: String!): AgentRunReceipt!`
- `agentRun(id: ID!): AgentRun`
- `agentRuns(limit: Int!): [AgentRun!]!`

Input JSON strings are strictly decoded into the exported Go DTOs in `fleet/agent_types.go`. The `AgentRun.data` JSON string is the versioned read contract represented by the exported `AgentRun` DTO; outer fields expose run/runtime/observation identity and an associated current proposal. `AgentRunReceipt.proposalResult` is the existing mutation receipt and is null when no proposal was requested. Server submission outcome distinguishes no request, a committed pending creation, stale-observation denial and other admission denial. The nested proposal reports current lifecycle state separately from the original creation receipt.

Only authenticated proposers can start/complete their own runs. Proposers read only their own subject's records in the current runtime. Operators may review all records in the current runtime. Viewers and unauthenticated callers cannot read provenance. The existing authenticator defines identity by subject; credential rotation for the same subject does not create a new private session. Previous-runtime provenance remains in PostgreSQL for privileged database audit but is deliberately absent from this runtime-scoped API.

Limits: 32 KiB JSON input/completion/facts, 4 KiB metadata at the database, 64 candidates/incidents, 128 comparisons, 14 sanitized activity steps, six model requests/eight tools, and 1–20 rows per list request. Existing HTTP body/query/time limits remain. These limits can reject large otherwise valid v2 investigations; no fallback proposal is submitted.

## Compatibility and limitations

Legacy charging, deterministic runs, historical evaluation and operator mutations keep their existing contracts. Only the new v2 runner requires start/completion support and uses atomic provenance submission; it never falls back to the legacy submission endpoint. Existing legacy callers are not retroactively labeled as v2 runs. Observation binding remains Go-issued and authoritative.

If an observation cannot be read, or start cannot be acknowledged, the host cannot safely invent a run/runtime binding and performs no inference or proposal submission. After a durable start, process termination or unavailable completion storage leaves a visible incomplete record. This ticket does not add a background reconciler or retention policy. Raw rejected model output is intentionally unavailable for forensic replay. Usage authenticity and cost remain external trust limitations.

## Verification

Tests cover real PostgreSQL transaction rollback and lost COMMIT acknowledgements, concurrent replay, ownership/runtime/observation rejection, fabricated decision/association fields, source labeling, bounded sanitized activity, failed/abstained completion, authenticated retrieval, independent approval and legacy regressions. `agent/scripts/check-agent-run-fixture.mjs`, invoked by `TestIntegrationAgentRunMastraHTTP`, executes actual Mastra tools with a local scripted adapter, selects a non-first real Go candidate, creates a linked pending proposal, retrieves its durable record, and verifies that no simulator state changed. It permits network access only to the local Go fixture.

No provider calls or API spending are part of verification. Test counts and environmental limitations are reported with the implementation review.

## Exact change manifest

| File | Purpose |
|---|---|
| `agent/README.md` | Entry-point documentation and compatibility notes. |
| `agent/scripts/check-agent-run-fixture.mjs` | Real Mastra/Go/PostgreSQL contract check using local scripted inference. |
| `agent/src/agent-run.test.ts` | Projection, redaction, usage and single-attempt completion tests. |
| `agent/src/agent-run.ts` | Bounded provenance projection and atomic completion transport. |
| `agent/src/backend.integration.test.ts` | Explicit source category for the real-backend v2 adapter test. |
| `agent/src/cli.test.ts` | Unknown provenance acknowledgement exit-status test. |
| `agent/src/cli.ts` | Fail the CLI on unknown provenance persistence. |
| `agent/src/decision-agent.test.ts` | Provenance-aware doubles, category persistence and required-protocol tests. |
| `agent/src/decision-agent.ts` | Durable start/completion, measured latency and explicit source metadata. |
| `agent/src/graphql.ts` | Authenticated production provenance mutation transport. |
| `agent/src/investigation-budget.test.ts` | Reported model metadata and provenance-aware failure tests. |
| `agent/src/investigation-budget.ts` | Usage availability, provider metadata and bounded failure categories. |
| `backend/db/migrations/005_agent_runs.sql` | Versioned run table, completion constraints, unique proposal link and index. |
| `backend/db/migrations_test.go` | Expect all five embedded migrations. |
| `backend/fleet/agent_http_integration_test.go` | Go-hosted end-to-end scripted Mastra provenance integration. |
| `backend/fleet/agent_runs.go` | Transactional start/completion, replay, identity isolation and reads. |
| `backend/fleet/agent_runs_integration_test.go` | PostgreSQL atomicity, concurrency, denial, privacy and approval tests. |
| `backend/fleet/agent_types.go` | Bounded versioned DTOs and strict input validation. |
| `backend/fleet/agent_types_test.go` | Unknown-field, sensitive-identifier and size rejection tests. |
| `backend/fleet/agent_validation.go` | Server snapshot projection and independent decision-summary checks. |
| `backend/fleet/read.go` | Reuse the existing read implementation while the service lock is held. |
| `backend/fleet/service.go` | Extract unchanged proposal admission/audit gates for atomic reuse. |
| `backend/graphql/agent_runs.go` | Authorized provenance resolver helpers and sanitized errors. |
| `backend/graphql/agent_runs_integration_test.go` | Real authenticated API persistence, retrieval and redaction tests. |
| `backend/graphql/generated.go` | Regenerated GraphQL execution bindings. |
| `backend/graphql/handler_test.go` | Assert the exact expanded read/write contract. |
| `backend/graphql/model/models_gen.go` | Regenerated provenance GraphQL DTOs. |
| `backend/graphql/schema.graphqls` | Narrow provenance start/completion/read contract. |
| `backend/graphql/schema.resolvers.go` | Delegate new fields to provenance helpers. |
| `docs/agent-05-provenance.md` | Schema, protocol, trust boundaries, verification and exact change manifest. |


## Recorded verification results

Final checks on 2026-10-18:

| Check | Passed | Failed | Skipped |
|---|---:|---:|---:|
| Agent unit suites | 277 | 0 | 0 |
| Agent real Go/PostgreSQL integration | 6 | 0 | 0 |
| Evaluation suites | 254 | 0 | 0 |
| Frontend regressions | 77 | 0 | 0 |
| Scripted v2 evaluation cases | 17 | 0 | 0 |
| Go db/safety/proposal/fleet/GraphQL, native with contract checks | 265 | 0 | 1 |
| Same Go packages with Linux race detector | 263 | 0 | 3 |

All agent/evaluation/frontend TypeScript checks, the agent build, production Go server build and `git diff --check` passed. Go counts include parent tests and subtests; overlapping runs are not summed. Native skip: `TestDemo08ExportObservations`. Race skips: that export plus `TestAgentIncidentHTTPClient` and `TestIntegrationAgentRunMastraHTTP`; both HTTP contracts passed in the native run.

One initial native opt-in run reported 262 passed, 3 failed and 1 skipped because the pre-existing Node fixture scripts import TypeScript sources containing `.js` imports. Plain Node cannot resolve those source imports. A temporary `registerHooks` resolver under ignored build output fixed module lookup without changing assertions or tracked harness code. The final native counts above include the successful rerun. The new AGENT-05 script imports compiled modules and does not require that workaround itself.

The final end-to-end record retrieved through production GraphQL selected R5 (the first candidate was R3), linked exactly one pending proposal, and recorded four model requests, six executed tools, eight mock-provider-reported tokens, an explicitly mocked source, and unavailable estimated cost. A sanitized copy is available locally in ignored `agent/dist/agent05-run-record.json`. No real provider inference or paid calls were made. Temporary PostgreSQL resources were removed after verification.
