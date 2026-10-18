# FleetOps P4-03: operator-approved job recovery

LLM proposes. Go validates. Human approves. Go executes.

## AGENT-03: recovery-decision-v2

The opt-in `--live --decision-v2` path uses a separate recovery-only schema,
prompt and validator. Legacy `--live`, charging demonstrations, deterministic
offline mode and archived evaluation scoring retain their existing contracts.
Both submission paths retain AGENT-02's live observation identity and fresh read.
No Go, GraphQL, command, approval or persistence change is required for v2.

Set `FLEETOPS_DECISION_OBJECTIVE` to `compare_tradeoffs` (default),
`minimize_pickup_steps`, or `preserve_battery_headroom`. The objective is trusted
host configuration and must match the response. Strict objectives require a
minimum pickup count or maximum admission headroom respectively; all ties are
acceptable. Trade-off mode allows any admitted option without asserting it is
best. A `pickup_cost` or `battery_headroom` basis still requires the corresponding
extremum. A `tradeoff` basis requires actual advantages and disadvantages in
verified comparisons; `admitted_option` claims eligibility only.

The v2 prompt contains facts, eligible alternatives and the objective, with no
`supportedAction`, preferred candidate or `reasoningStatements` catalog.
Every candidate must be considered exactly once, with exact source, recipient,
job and blocked-incident evidence. Output copies all Go incident evidence and
acknowledges operator attention. Every selected candidate must be compared with
every alternative. All references, numbers and less/equal/greater directions are
independently checked against the observation, including these derived values:

- `totalRouteSteps = pickupSteps + deliverySteps + chargingExitSteps`.
- `missionHeadroom = battery - requiredBattery`.
- `admissionHeadroom = battery - max(30, requiredBattery)`.

Headroom is above the reported admission requirement, not a prediction of final
battery, measured consumption or energy savings. Preview routes are not reserved.
The model chooses structured claims, comparisons and decision basis. The host
renders an explanation only after validation; arbitrary narrative and lifecycle,
deadline, throughput or forecast claims are rejected rather than labelled verified.

Abstention reasons are checked against facts: poisoned runtime, no candidates for
a blocked incident, no supported incident, or an operator trade-off. The last
requires trade-off mode and a conflict where no candidate attains both minimum
pickup steps and maximum admission headroom. A tie alone does not justify it.
All decisions include snapshot/route/delivery/approval/optimality limitations.

This remains one generation step with no model tools, a 30-second deadline,
6,000 output-token cap and no automatic retries. `--submit` is a separate host
opt-in. Tool investigation is AGENT-04; no inference was paid for in AGENT-03 tests.
The programmatic entry point is `runRecoveryDecision`; legacy `runLive` is unchanged.

From `evals/`, `node scripts/run.mjs --decision-eval` runs 17 scripted validation
cases, including non-first minima, route/battery trade-offs, ties, abstention,
fabricated evidence, false comparisons and all six candidate-order permutations.
Expected recipient sets are independently declared. These are **synthetic
reasoning-only variants** of the saved Go observation: altered costs are not new
Go admissions or measured simulator outcomes. The report does not establish model
accuracy, scheduling superiority, throughput, or real recovery effectiveness.

The sections below describe the preserved legacy path.

This slice reads the real authenticated `fleetState`, including active incidents,
jobs and Go-admitted reassignment candidates. The host can submit
`SetChargingInput` through `submitProposal` or `ReassignUnstartedJobInput` through
`submitReassignment`. The Mastra agent exposes only the
read tool `readFleetState`; the host retains submission tools after output validation.
No approval, rejection, ticking,
Commander, Driver, or fault injection is exposed to the model.
Go remains the authority for incident classification, safety, authorization,
persistence and execution. The read tool retrieves fleet state and incidents together.

## P4-02 reasoning and snapshot references

The default offline mode remains deterministic. `--live` explicitly enables the
configured provider through Mastra's actual `Agent.generate` implementation.
The host first reads and validates a detached fleet snapshot, then supplies only
its facts, snapshot references, supported action candidate, explicit demo IDs,
and host-validated `reasoningStatements` choices.
Generation is limited to that snapshot: `activeTools=[]` and `toolChoice=none`;
there is no mutation tool in the model's tool map. Submission cannot occur while
an unvalidated response is being generated.

Results distinguish:

- `observedFacts`: host-owned fleet telemetry, Go incident records and references.
- `aiAnalysis`: separately labeled interpretations and a proposed action, with
  status `disabled`, `accepted`, `rejected` or `failed`.
- `recommendedAction`: a validated supported command or null.
- `submissionCheck` and `proposal`: host-owned freshness and backend submission outcomes.

An incident reference is exactly `{scope:"snapshot", tick, robotId, type}`.
These are **snapshot-scoped references, NOT stable incident IDs**. They are valid
only against the exact snapshot supplied to this analysis and **are not valid
across separate simulator runs**. Equal ticks or keys in two runs do not identify
the same incident. There is no persisted identity, episode tracking, migration,
runtime identity check or incident history. Do not store and reuse these references
as cross-request or cross-run evidence.

Every supplied incident must be interpreted exactly once. Host validation rejects
invented/missing/duplicate/ambiguous references, another tick, changed severity,
altered telemetry, unknown fields (including `incidentId`), and unsupported
commands. Source snapshots with duplicate robot/station/incident keys, missing
robots or contradictory incident telemetry also fail closed. No TypeScript
classification or severity algorithm is introduced.

Each incident interpretation contains its exact reference, severity and telemetry,
an `assessment` explicitly treated as AI interpretation, and
`recommendedResponse:"operator_attention"`. Optional SetCharging output must copy
the actual robot/station telemetry and tick, match the host's supported candidate,
and declare `routine_charging` or an explicitly enabled `demonstration` purpose.
Reassignment output must copy the exact source/recipient telemetry, job, Go
candidate, tick, and source `traffic_blocked` snapshot reference and declare
`incident_recovery`. It must match the host's deterministic first Go candidate.
Invalid output
is discarded as a whole, without silently rewriting it into a valid proposal.
As approved in the P5-02 follow-up, `assessment` and `rationale` must each match
one whole host-supplied statement for that field/action. `src/statements.ts`
provides policy/uncertainty explanations and statements grounded in current Go
job status. Affirmative approval is unavailable because this reasoning input
has no verified proposal/operator state. Completed-delivery text is available
only for a matching job with Go status `done`; its tick is observation time,
not a fabricated completion time. Pending/unknown states, negation, hypothetical
approval and exact quoted untrusted job IDs have supported forms.

Other prose, arbitrary paraphrases and appended claims reject the entire
response; the host does not silently rewrite it. Assessment validation also
runs when no action is proposed. Existing deterministic fixture explanations
remain in the catalog. The prompt provides the permitted statements, but only
host validation enforces them. No general natural-language classifier or keyword
ban is used. This changes accepted explanation prose without changing the
reasoning schema, tool signatures or actual Go authorization/approval rules.

For either offline or LLM submission, the host reads and validates another live
snapshot before proposing. Any changed telemetry or tick produces
`submissionCheck:"snapshot_changed"`; a failed fresh read produces `unavailable`.
Both remove the current recommendation and leave the proposal unsubmitted.
The original analysis remains explicitly attached to its original snapshot.
No automatic paid reanalysis occurs. The proposal tool also checks current
eligibility, and Go remains authoritative for safety and approval-time
revalidation. The network interval between read and submit cannot be made atomic
by this client; Go's validation remains required.

## Current incidents

`fleetState.activeIncidents` is an additive read-only field. Go calls the existing
`incident.Reconcile(nil, snapshot, 0)` under the fleet service lock, using the same
snapshot as `tick`, robots and stations. Each result contains `robotId`, `type`,
`severity` and the affected robot's complete exposed `telemetry`.
There are no incident IDs, timestamps, resolved records, episode identity or
persisted incident history in this interface. Reads recompute current conditions
without ticking, writing an audit or executing.

The existing deterministic Go rules are unchanged:

| Incident | Current condition | Severity |
| --- | --- | --- |
| `low_battery` | Battery <30% **and an assigned job** | High below 15%; medium at 15–29% |
| `robot_unresponsive` | Robot status is `offline` | High |
| `traffic_blocked` | `blocked=true` | High above 40 wait ticks; medium through 40 |

Waiting without `blocked=true` and low battery without an assigned job do not open
detector incidents. Historical fault entries do not establish current conditions.
TypeScript validates and consumes these Go results; it contains no incident
detector or severity thresholds.

Structured results separate `incidents` (with telemetry), `operatorAttention`,
and `recommendedAction`. Go-admitted reassignment can restore progress for an
uncollected job held by a healthy blocked robot. Without such a candidate, the
report requests operator attention and recommends no incident recovery command.
Neither command restores offline robots or removes an obstruction; SetCharging
cannot navigate to a station or charge a robot below the command minimum.
Routine charging is recommended only when there are no active incidents. An
explicitly requested eligible demonstration on a healthy robot remains available
and is labeled separately, never presented as incident recovery.

Model output cannot override structured incident classification, severity,
operator-attention items or proposal status. It may propose null or the supported
candidate only after passing grounding validation.
Before an opted-in submission, the tool reads a fresh combined snapshot and
requires its target to match the host recommendation. This bounds agent proposals;
Go still validates, authorizes and revalidates at approval time. Missing or
malformed incident data fails closed rather than falling back to TypeScript
guesses. Update older servers before using this client.

## Dependencies and compatibility

Use **Node.js 24.21.0**, matching the repository engine, and the existing npm
toolchain. Direct runtime dependencies are pinned exactly:

| Package | Version | Verification source |
| --- | --- | --- |
| `@mastra/core` | 1.75.0 | [Published npm release](https://www.npmjs.com/package/@mastra/core/v/1.75.0), `npm view @mastra/core@1.75.0 version engines peerDependencies --json` |
| `zod` | 4.6.7 | [Published npm release](https://www.npmjs.com/package/zod/v/4.6.7), `npm view zod version` |
| `@types/node` (development) | 24.19.2 | [Node 24 types](https://www.npmjs.com/package/@types/node/v/24.19.2), `npm view @types/node@24 version --json` |

Mastra's published metadata requires Node >=22.13.0 and accepts Zod
`^3.25.0 || ^4.0.0`; these pins satisfy both. Existing TypeScript 7.0.2 and
Vitest 5.0.2 remain unchanged. The lockfile records resolved transitive versions
and integrity hashes. We use the supported `Agent`, `createTool`, model router,
and structured-output APIs described in the [Mastra docs](https://mastra.ai/docs).
`skipLibCheck` follows that setup and avoids errors in Mastra's bundled dependency
declarations; strict checking of our source and tests remains enabled.

At implementation time `npm audit` reported **five moderate findings**, one
underlying advisory propagated through `sprintf-js → argparse → js-yaml →
gray-matter → @mastra/core`. This slice does not use frontmatter parsing.
No force upgrade or transitive override was applied. Review the dependency
finding before wider deployment.
See the [P4-01 dependency investigation](DEPENDENCY-AUDIT.md) for affected versions,
patch availability, production dependency status and execution-path analysis.

## Private configuration and running

From `agent/`, with Node 24.21.0 on PATH:

```powershell
npm ci
Copy-Item .env.example .env
# Edit .env privately. Never print or commit credentials.
npm run build
npm start
```

`npm start` loads the ignored `agent/.env` using Node's built-in environment-file
support. Configure `FLEETOPS_GRAPHQL_URL` and `FLEETOPS_PROPOSER_TOKEN` privately.
The token must have **only the proposer role** in the Go server's
`FLEETOPS_AUTH_TOKENS` hash configuration. Use a distinct subject from the
approving operator. See [backend authentication setup](../backend/graphql/README.md).
The token is opaque: there is no role-introspection endpoint, so the client
cannot verify a token's configured role. Do not give it an operator token.
Credentials stay in the HTTP closure and never enter model prompts, tool schemas,
tool results, or printed configuration. HTTP is allowed only on loopback;
use HTTPS elsewhere. Redirects are refused and requests have a ten-second timeout.
Transport, GraphQL and malformed-response errors are sanitized.

The default run makes **no model requests and no mutations**. It prints
deterministic observations, potential issues, a supported recommendation or null,
an explanation, and host-controlled proposal status. It reads current state,
not a canned fixture. Recommendations reflect snapshot facts and are advisory;
backend safety and authorization always decide whether a proposal is accepted.

To explicitly permit one proposal in a run:

```powershell
npm start -- --submit
```

The default heuristic first recommends an available Go reassignment candidate.
Without one, it recommends charging only an idle, job-free robot already
at an apparently available station with battery >=30% and <100%. It does not
route robots. Battery below 30% cannot be a valid `SetCharging` candidate; whether
it is an incident depends on Go's assigned-job rule. A healthy 100% robot is not
an incident.

For the existing stationary demo, run the backend with
`FLEETOPS_CHARGING_DEMO=1` and automatic ticking disabled, set the explicit
`FLEETOPS_DEMO_ROBOT_ID=R1` and `FLEETOPS_DEMO_STATION_ID=S1` in the private agent
configuration, then run:

```powershell
npm start -- --demo
npm start -- --demo --submit
```

The first command only inspects and recommends; the second permits submission.
The result labels this as a demonstration. `pending_approval` means a durable
proposal awaiting an independent human operator in the existing dashboard.
It does not mean charging started. Do not rerun submission to check status;
inspect the existing operator inbox instead.

## Optional paid model execution

Live mode sends fleet snapshots to the selected provider and **may incur API
charges**. No live demonstration is required for this ticket. Choose a provider/model that supports
structured output and configure `FLEETOPS_MODEL=provider/model` privately.
The supported provider key variables are `OPENAI_API_KEY` for `openai`,
`ANTHROPIC_API_KEY` for `anthropic`, and `GOOGLE_GENERATIVE_AI_API_KEY` for `google`.
Only configure the key for the chosen provider. There is no default model.

```powershell
npm start -- --live
# Explicit demonstration with both independent opt-ins:
npm start -- --live --demo --submit
```

`--live` plus a configured provider key are required before generation. Merely
setting a key or model does not enable live mode; `--submit` is a separate
host-controlled opt-in, never a model-controlled argument. Live execution is
bounded to one generation step, 2400 output tokens, a 30-second generation timeout,
and zero model retries. These bounds do not guarantee a monetary limit; provider
pricing and input-token usage apply. Framework logging is disabled, and the CLI
prints sanitized errors. No environment object or private config is sent to the
model. Only snapshot facts/references, the supported candidate and explicit demo
IDs are sent. The provider SDK uses its private API key for transport authentication;
neither provider keys nor the GraphQL bearer token enter model messages.

Results use `disabled`, `not_requested`, `pending_approval`, `rejected`, or
`unknown` proposal status. HTTP 200 alone is insufficient: acceptance requires a
proposal ID, pending state, committed audit and no execution attempt. A committed
backend denial is `rejected`. Uncertain transport, malformed mutation responses
or uncommitted outcomes are `unknown`; **never retry automatically**. Each run
permits at most one submission, including concurrent tool calls. The limit is
per run, not durable deduplication across processes. A model or SDK parsing failure
returns `aiAnalysis.status=failed`; host grounding rejection returns `rejected`.
Both preserve observed facts and prevent submission. The CLI exits 1 for these
statuses, a stale/unavailable submission check, or a rejected/unknown proposal.

## Verification

```powershell
npm test
npm run typecheck
npm run build
# PowerShell 7, Docker with postgres:18.6 and golang:1.27.1, Go 1.27.1:
pwsh -NoProfile -File scripts/verify-integration.ps1
git diff --check
```

Unit tests use deterministic API and model doubles. They never invoke a real
provider endpoint. A local in-process provider adapter exercises Mastra's actual
generation and structured-output parser with no network access. Other deterministic
model doubles test the application boundary. The real-backend suite is deliberately excluded from `npm test`;
`npm run test:integration` fails without explicit test endpoint/proposer/viewer
configuration rather than silently skipping. The harness generates temporary
random tokens, starts isolated PostgreSQL on a dynamic loopback port, runs the
affected Go suites and PostgreSQL integration under `-race -p 1 -parallel 1`
in Docker, builds the Go server into ignored `agent/dist`, and exercises the real HTTP contract from
TypeScript. It tests authenticated state retrieval, durable valid demo submission,
invalid proposals, invalid tokens, viewer denial and no execution on submission.
The harness also starts a trusted, test-only Go HTTP fixture using the existing
detector and production GraphQL handler. The compiled TypeScript agent consumes
six classified incidents, with severity boundaries and nonincident controls, and
verifies telemetry, determinism, operator attention and zero mutations even with
submission opted in. P4-02 additionally runs these real Go-classified records
through Mastra with a local provider adapter, verifies grounded references and
rejects wrong-tick evidence. A separate model double exercises validated demo
submission against the real backend. This fixture is not part of the server and adds no fault
endpoint. The server and database container are removed in `finally`. Run the harness in a
separate PowerShell process as shown so its temporary environment does not persist.
The optional-runner integration test stubs a test-only provider key and replaces
generation with a local double; it makes no paid model requests.
Go packages and `t.Parallel` test cases are serialized because they share the
PostgreSQL migration advisory lock; concurrent runs can exceed its deadline.
Tests that explicitly start concurrent migration workers still exercise those workers.

## Before genuine incident response

P4-03 adds one bounded recovery command. It applies only before pickup, with a
healthy blocked source, healthy idle recipient, and Go-admitted route and energy
budget. Both robots need battery >=30%. It cannot restore offline robots, move
an obstruction, transfer a collected load, or recover below-minimum robots.
Route feasibility is a current-state estimate, not a future reservation or
delivery guarantee. Autonomous reassignment, incident history, durable agent
memory, cross-run deduplication and provider quality evaluation remain outside
this ticket. The React approval UI awaits P4-04; use GraphQL for reassignment
review. See [P4-03 workflow](P4-03.md) for the deterministic demonstration.
