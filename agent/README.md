# FleetOps P4-01: incident-aware agent

LLM proposes. Go validates. Human approves. Go executes.

This slice reads the real authenticated `fleetState`, including active incidents,
and can submit the existing
`SetChargingInput` through `submitProposal`. The Mastra agent has exactly two
tools: `readFleetState` and `submitSetCharging`. No approval, rejection, ticking,
Commander, Driver, fault injection, or job-list query is exposed.
Go remains the authority for incident classification, safety, authorization,
persistence and execution. The read tool retrieves fleet state and incidents together.

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
and `recommendedAction`. The current command cannot recover these incident types:
it cannot move or unblock robots, restore offline robots, or charge below 30%.
The report requests operator attention and recommends no recovery command.
Routine charging is recommended only when there are no active incidents. An
explicitly requested eligible demonstration on a healthy robot remains available
and is labeled separately, never presented as incident recovery.

Model output cannot override structured incident classification, severity,
operator-attention items, proposal status or the host's supported recommendation.
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

The default heuristic recommends charging only an idle, job-free robot already
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
charges**, including multiple tool-reasoning steps. No live demonstration is
required for this ticket. Choose a provider/model that supports tool calling and
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
bounded to four steps, 1200 output tokens per step, a 30-second generation timeout,
and zero model retries. These bounds do not guarantee a monetary limit; provider
pricing and input-token usage apply. Framework logging is disabled, and the CLI
prints sanitized errors. No environment object or private config is sent to the
model. Only snapshot data, time, explicit demo IDs and public run controls are sent.

Results use `disabled`, `not_requested`, `pending_approval`, `rejected`, or
`unknown` proposal status. HTTP 200 alone is insufficient: acceptance requires a
proposal ID, pending state, committed audit and no execution attempt. A committed
backend denial is `rejected`. Uncertain transport, malformed mutation responses
or uncommitted outcomes are `unknown`; **never retry automatically**. Each run
permits at most one submission, including concurrent tool calls. The limit is
per run, not durable deduplication across processes. A model failure after a tool
call preserves the host's proposal status and reports fallback snapshot observations.

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
provider. The real-backend suite is deliberately excluded from `npm test`;
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
submission opted in. This fixture is not part of the server and adds no fault
endpoint. The server and database container are removed in `finally`. Run the harness in a
separate PowerShell process as shown so its temporary environment does not persist.
No model key is read or used by this integration suite.
Go packages and `t.Parallel` test cases are serialized because they share the
PostgreSQL migration advisory lock; concurrent runs can exceed its deadline.
Tests that explicitly start concurrent migration workers still exercise those workers.

## Before genuine incident response

There is a current-incident read field, but no incident history, job-list query, movement action, autonomous
recovery, scheduling, durable agent memory, cross-run proposal deduplication or
provider quality evaluation in P4-01. A robot below the minimum or away from a
station cannot be recovered with this action. Real incident handling needs a
separately authorized API/action contract and later-ticket design, plus a reviewed
provider configuration and evaluation. This ticket stops at current-state incident awareness.
