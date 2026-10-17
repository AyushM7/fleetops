# DEMO-02: Phase 6 repository inventory

Inspection date: 2026-10-17. Scope: architecture inspection only. The sole deliverable is this file; no application, API, simulator, dependency, fixture, branch, or deployment change is included. No live model calls were made. References below are repository-relative links, with symbols identifying the evidence. Proposed capabilities are explicitly described as recommendations, not existing behavior.

## 1. Verified architecture and responsibilities

The existing separation supports a richer presentation without replacing simulation algorithms. The principal missing layer is a controlled local-demo lifecycle plus read-only presentation evidence.

| Layer | Verified responsibility and evidence |
| --- | --- |
| Simulator | [`backend/sim/sim.go`](../backend/sim/sim.go), `Sim`, `New`, `Tick`, `Snapshot`: owns configuration, seeded RNG, robots/jobs/stations/faults, navigation, recovery and fault countdowns. Not goroutine-safe. `New` clones configuration. Each `Tick` increments the integer tick, may generate a job, steps robots and refreshes aisle occupancy. `Snapshot` copies public state, including nested fault data. |
| Navigation, dispatch, energy | [`backend/sim/nav.go`](../backend/sim/nav.go), `advanceNavigation`, handles existing grid movement, waiting and replanning. [`backend/sim/dispatch.go`](../backend/sim/dispatch.go), [`autocharge.go`](../backend/sim/autocharge.go), [`recovery.go`](../backend/sim/recovery.go) and [`recovery_idle.go`](../backend/sim/recovery_idle.go) implement autonomous dynamics. A moving robot advances through discrete cells, not continuous physics. [`backend/routing/astar.go`](../backend/routing/astar.go) and [`backend/scheduler/assign.go`](../backend/scheduler/assign.go) remain reusable. |
| Capability boundary | [`backend/sim/roles.go`](../backend/sim/roles.go): `Reader` provides observations/previews; `Commander` adds only charging and unstarted-job reassignment; `Driver` adds trusted fault injections. These are Go capabilities, not agent tools or HTTP endpoints. |
| Fleet service | [`backend/fleet/service.go`](../backend/fleet/service.go), `New`: exclusively constructs/owns the simulator; callers receive neither Commander nor Driver. One mutex serializes reads, ticks, proposal submission, decisions and execution. `Tick` refuses a poisoned runtime. `newService` creates a random runtime ID and an in-memory canonical proposal map/lifecycle. |
| Read model | [`backend/fleet/read.go`](../backend/fleet/read.go), `ReadState`: creates a detached view from one locked snapshot, including layout, telemetry, jobs, active conditions and Go-admitted reassignment candidates. Layout is derived from configuration. Candidate distances and required battery are previews, not actual route polylines. |
| Server lifecycle | [`backend/cmd/server/application.go`](../backend/cmd/server/application.go), `application`: authenticates, opens/migrates PostgreSQL, constructs seed-42 `fleet.Service` using `DefaultConfig`, installs health/GraphQL handlers and starts optional ticks. `FLEETOPS_CHARGING_DEMO=1` changes initial configuration only: R1 starts at S1, zero initial jobs, dispatch disabled. This is distinct from the traffic fixture. [`main.go`](../backend/cmd/server/main.go) handles signal-driven HTTP shutdown. Normal server binds `:PORT`, not explicitly loopback. |
| Transport/auth | [`backend/graphql/handler.go`](../backend/graphql/handler.go): authenticated JSON POST only, bounded body/query/selection/complexity and request deadline. [`backend/auth/auth.go`](../backend/auth/auth.go): hashed configured bearer tokens map to trusted viewer/proposer/operator identities; constant-time digest comparison. [`backend/graphql/resolver.go`](../backend/graphql/resolver.go) exposes workflow/read methods, no privileged simulator interface. |
| Agent | [`agent/src/tools.ts`](../agent/src/tools.ts): read telemetry and submit the two supported proposal types, opt-in, at most one attempt per session; no approval/execution/fault/tick tools. [`submission.ts`](../agent/src/submission.ts) rereads and checks facts before submission. [`reasoning.ts`](../agent/src/reasoning.ts) validates grounding. Go still validates admission and execution. |
| Persistence | [`backend/fleet/storage.go`](../backend/fleet/storage.go) persists proposals and decision/execute attempts through [`backend/db/transaction.go`](../backend/db/transaction.go). PostgreSQL does not persist the running simulator, RNG/navigation state, fleet history or incident episodes. |

### Existing time controls

[`backend/cmd/server/runtime.go`](../backend/cmd/server/runtime.go), `parseTickConfig`, defaults to automatic ticking **disabled**, with a default interval of 500 ms when enabled. `FLEETOPS_AUTO_TICK` and `FLEETOPS_TICK_INTERVAL` are startup settings. `startTicks` owns one worker; its stop function cancels and joins in-flight work before resources close. Wall-clock pulses pace calls but do not enter simulation state; slow operations can drop pulses without catch-up ticks.

| Capability | Existing status |
| --- | --- |
| Advance one tick | Trusted Go `Service.Tick()` exists, serialized with decisions. No browser/agent step operation. |
| Continuous ticking | Optional normal-server worker exists. The current local traffic demo instead scripts tick progression. |
| Pause/resume | No runtime API or dashboard control. Worker cancellation is an internal shutdown primitive, not a pause contract. |
| Speed | Startup duration configuration exists; no runtime speed adjustment. |
| Restart/reset | Re-running the launcher constructs a new isolated demo. No in-process reset/swap endpoint or simulator restore method. |
| Playback/rewind | No dashboard snapshot timeline or playback controls. Public snapshots omit private RNG/navigation/recovery state and cannot be treated as restorable checkpoints. |

The complete GraphQL surface in [`backend/graphql/schema.graphqls`](../backend/graphql/schema.graphqls) is three queries (`fleetState`, `proposal`, `pendingProposals`) and four mutations (`submitProposal`, `submitReassignment`, `approveProposal`, `rejectProposal`). It contains none of these lifecycle controls or subscriptions.

## 2. DEMO-01 loopback gateway and frontend requests

The implementation lives in [`evals/phase4/start-demo.ps1`](../evals/phase4/start-demo.ps1), [`gateway_test.go`](../evals/phase4/gateway_test.go), and [`demo_test.go`](../evals/phase4/demo_test.go), rather than `backend/cmd/server`.

The PowerShell launcher requires `-LocalDemo -Submit`, Node 24.21.0, existing dependencies, Docker and Go. It builds agent/web, creates a random temporary backend copy under ignored `evals/dist/p405`, copies harness tests into that copy's `fleet` package and starts its own ephemeral PostgreSQL 18.6 container on a random loopback port. An exclusive `run.lock` and existing `session.json` check prevent ordinary concurrent runs. It executes explicitly tagged `fleetops_test` tests. `-CheckOnly` stops before human review; it does not prove human approval or recovery.

`TestPhase4AcceptanceDemo` creates an authenticated backend test server for the proposer script and a separate browser-facing loopback test server. The latter serves built assets and injects `<meta name="fleetops-local-demo" content="enabled">`. The gateway:

- Checks loopback peer, exact Host, supplied Origin and Fetch Metadata. GraphQL requires POST, exact Origin, `application/json`, `X-FleetOps-Demo: 1`, and a valid cookie; browser Authorization headers are rejected.
- Issues a random host-only, HttpOnly, SameSite=Strict cookie with a 20-minute deadline; bounds sessions to 256. Cookie values are opaque capabilities, not backend bearer tokens. Local HTTP intentionally has no Secure cookie flag.
- Keeps the backend operator credential in memory, strips Cookie and injects the operator bearer credential into the in-process authenticated handler. All local browser sessions share this demo operator identity. The separate script submits as proposer.
- Applies no-store, CSP, no-referrer and nosniff headers. Serves `/` and `/assets/` paths; unknown paths have no SPA fallback. Closing clears session records and the operator credential.

[`web/src/demo.ts`](../web/src/demo.ts), `authentication`, selects same-origin cookies/custom header only when the server marker is present. Otherwise requests use an in-memory bearer token with `credentials: "omit"`. [`web/src/App.tsx`](../web/src/App.tsx), `App`, owns login draft/token, feed, refresh and auth error; refresh/disconnect clears normal-mode credentials. The demo's `local-demo-session` string is a UI sentinel, not a backend credential. A forged HTML marker does not bypass backend authentication.

[`web/src/fleet.ts`](../web/src/fleet.ts) and [`proposals.ts`](../web/src/proposals.ts) use relative `/graphql`, POST JSON, no cache, no redirects, aborts and eight-second client deadlines. Fleet polling schedules the next request one second after completion; pending proposals poll after two seconds. Reads validate response shapes. Authentication failure clears the session view; outages retain explicitly stale data. Mutations are not automatically retried. [`web/vite.config.ts`](../web/vite.config.ts) binds development Vite to loopback and proxies GraphQL to `127.0.0.1:8080`; the demo serves built assets directly.

## 3. Existing traffic demo and proposal workflow

### Scenario generation

[`backend/sim/reassignment_fixture.go`](../backend/sim/reassignment_fixture.go), `NewReassignmentTestFixture`, is behind `fleetops_test`. It constructs an 8x8 seed-42 warehouse, five robots, one station, a vertical wall with a pocket exit, and enables existing dispatch/autocharge/recovery. Trusted package-local setup seeds moving R1 holding assigned/uncollected J1, offline R2 at the exit, and J1's pickup/delivery. It advances 42 ordinary ticks. This is explicit fixture construction, not random traffic emerging from the default server, and not `InjectAisleConflict`.

[`backend/fleet/reassignment_export_test.go`](../backend/fleet/reassignment_export_test.go), `NewReassignmentTestService`, wraps that simulator using the production service machinery and a private test schema. Its layout is supplied by the harness's `dashboardFixture` adapter, defined in [`web/scripts/go-dashboard-integration_test.go`](../web/scripts/go-dashboard-integration_test.go), because this internal service constructor does not initialize production layout. This coupling matters if the demo is later packaged outside tests.

[`evals/phase4/demo_test.go`](../evals/phase4/demo_test.go), `TestPhase4AcceptanceDemo`, constructs equal baseline/main snapshots at tick 42. It advances baseline 200 ticks and verifies J1 remains assigned and R1 blocked. [`agent/scripts/phase4-demo-reasoning.mjs`](../agent/scripts/phase4-demo-reasoning.mjs) restricts fetch to the exact local GraphQL endpoint, uses a deterministic Mastra model double with tools disabled during generation, validates the grounded response, rereads the same facts, and submits one pending reassignment. It requests a 30-minute proposal expiry. This is **not a live LLM**.

The harness verifies submission did not mutate simulation and that PostgreSQL contains the accepted proposer create event. Main remains at tick 42 while the human chooses. After observed approval plus successful execution, the harness waits two seconds per tick for three ticks, then advances another 197 ticks without presentation pacing. It verifies J1 is done on R3 and the final tick matches baseline tick 242, and queries committed independent operator approve/execute audit rows. Rejection/expiry writes evidence without recovery; stop/outage files manage test-session shutdown. The tick-45 delivery is documented in [`docs/phase4-demo.md`](phase4-demo.md); the harness records intermediate snapshots but its final assertion is at tick 242.

### Admission, approval, execution and audit

[`backend/proposal/lifecycle.go`](../backend/proposal/lifecycle.go) owns immutable canonical proposal records and pending/approved/rejected/expired transitions. Creation preflights without executing. Approval preflights again; rejection does not execute. Expiration uses a trusted wall clock, not simulation ticks, and is materialized by lifecycle operations rather than a background expiry sweep. Reading the pending inbox does not expire records.

[`backend/fleet/decision.go`](../backend/fleet/decision.go), `decide`, locks persisted records, checks trusted operator authorization, runtime ownership, independent submitter/approver identity and exact canonical record equality. `executionGate` rechecks context, authorization, Go preflight, wall-clock expiry and approved state immediately before the command. Only `SetCharging` and `ReassignUnstartedJob` can execute. Service mutex plus database transaction prevents tick/approval interleaving and duplicate command execution in that service.

[`backend/safety/charging.go`](../backend/safety/charging.go) requires a healthy robot with at least 30% battery, idle/no job, physically at an eligible free station. It is charging **in place**, not a send-to-station command. [`backend/safety/reassignment.go`](../backend/safety/reassignment.go) checks both robots and the 30% floor, then uses Go's reassignment preview in [`backend/sim/reassignment.go`](../backend/sim/reassignment.go). An approval does not imply completed delivery or removal of the obstruction.

[`backend/db/migrations/002_charging_workflow.sql`](../backend/db/migrations/002_charging_workflow.sql), [`003_proposal_submitter.sql`](../backend/db/migrations/003_proposal_submitter.sql), and [`004_reassign_unstarted_job.sql`](../backend/db/migrations/004_reassign_unstarted_job.sql) store proposal/runtime IDs, state, execution, immutable proposal payload and submitter; audit rows contain identity, timestamp, attempt, accepted/reason and full event payload. Unknown proposal IDs can be audited without an FK. `storage.go` stages/updates an execute audit row inside the transaction; nothing is durable until commit. Stored execution values are none/succeeded/failed; `unknown` is a service uncertainty response, not a stored execution enum value.

Simulator memory cannot roll back with PostgreSQL. Persistence uncertainty or a caught operation panic poisons the runtime; subsequent ticking/mutations fail closed. `MutationResult.auditCommitted` and `executionAttempted` distinguish acknowledgement from invocation. A lost response may follow a committed command: retain the existing unknown-outcome receipt and refresh persisted status, never automatically resubmit.

The seven frozen rules remain the authority in [`docs/safety-rules.md`](safety-rules.md): approved pending proposal; healthy existing target and 30% minimum; single job ownership; occupied-aisle exclusion; free charging station; no mutation from rejection/expiry; audit every accepted/rejected attempt. Transport authentication/query-limit failures are rejected before domain lifecycle/audit; do not advertise `fleet_audit` as a complete HTTP access log or claim a durable event when persistence itself failed. No reset design may waive these rules for operational commands.

## 4. Existing reusable functionality and frontend inventory

| File / component | Reuse and limitations |
| --- | --- |
| [`web/src/App.tsx`](../web/src/App.tsx): `App` | Session/auth boundary, Demo Mode banner, poll cleanup and decision-triggered refresh. React hooks/local state; no external store or router. |
| Same file: `Dashboard` | Loading/unavailable/poisoned notices, stale map, tick, map/fleet arrangement and last-received time. Retain these semantics in a dark redesign. |
| Same file: `Warehouse` | Original SVG grid using backend layout; coordinates, wall/aisle/station cells, station occupancy titles, robot IDs/status/job/blockage titles and markers. Fixed 40-unit cell coordinates in responsive SVG with horizontal scrolling. No routes, selection, camera, interpolation or movement animation. |
| Same file: `RobotCard` | Status, job, position, accessible battery meter, blocked wait and offline/fault text. Currently internal to App; extraction would be presentation refactoring. |
| [`web/src/Operations.tsx`](../web/src/Operations.tsx): `Operations` | Go-classified active conditions, severity/telemetry, job lifecycle table. Explicitly labels current conditions versus history and transfer versus delivery. |
| [`web/src/ApprovalInbox.tsx`](../web/src/ApprovalInbox.tsx): `ApprovalInbox`, `InboxView`, `DecisionReceipt` | Operator-only feed, expiry/runtime/availability gates, one in-flight decision, approve/reject actions, receipt and persisted-status refresh. `InboxView` is exported; receipt is internal. No recorded rationale displayed: normal mode says "No rationale recorded"; demo uses a fixed adapter notice. |
| [`web/src/fleet.ts`](../web/src/fleet.ts) | Typed DTOs, runtime validation and non-overlapping polling. Web currently does not request `reassignmentCandidates` even though GraphQL exposes them. |
| [`web/src/proposals.ts`](../web/src/proposals.ts) | Typed proposals/receipts, mutation response validation, bounded pending inbox and generation-aware refresh. Reuse error/uncertainty semantics. |
| [`web/src/styles.css`](../web/src/styles.css) | Original light cream/green styling, status colors, panels, buttons, responsive breakpoints at 1000/760/480 px, focus outlines, meters and stale indicators. Reuse layout/accessibility patterns; graphite/blue styling requires new theme values. No design-token system, asset library or animation library. Reduced-motion rule only sets scroll behavior, so new movement needs explicit handling. |
| [`web/src/main.tsx`](../web/src/main.tsx), [`web/package.json`](../web/package.json) | Existing React/Vite entry and build/typecheck workflow. Runtime dependencies are React and React DOM; SVG/CSS can implement the visual direction without adding dependencies. |

Additional reusable backend pieces: `sim.WarehouseConfig()` in [`backend/sim/layout.go`](../backend/sim/layout.go) supplies shelf-like walls with autocharge/recovery; the normal server uses `DefaultConfig()`, not this layout. `incident.Reconcile` can maintain episodes when given previous history, but `fleet.currentIncidents` deliberately calls it with nil history and exposes no episode ID. Do not confuse this detector capability with an existing persistent incident timeline.

## 5. Potential scenarios and feasibility

The strongest reproducible alternatives are already defined by the trusted Go harness in [`backend/evalrecovery/scenarios.go`](../backend/evalrecovery/scenarios.go), `scenarios`/`newScenario`, behind `fleetops_test`. They are not options in the current demo launcher.

| Scenario | Verified reproduction without algorithm changes | Honest outcome / remaining wiring |
| --- | --- | --- |
| Blocked reassignment | Existing tick-42 fixture, admitted J1 R1-to-R3 transfer. | Current end-to-end demo path. Compare delivery with an equal untouched baseline, not "repair R2". |
| Blocked, no eligible candidate | Same fixture; trusted tick-42 low-battery injection into R3/R4/R5. | No supported intervention at observation time; operator attention/abstention. Candidates may become eligible later. Needs scenario selection and no-proposal UI flow. |
| Offline unfinished job | Same fixture; inject R1 unresponsive at tick 42, then three ordinary ticks. | Offline source cannot use existing reassignment/charging commands. Honest escalation, not automatic repair. |
| Routine charging | Default configuration, no initial jobs, job generation deferred, R1 on S1; 1000 ordinary ticks reduce battery to 50%. | Existing SetCharging eligible above frozen 30% floor. Measures battery/station occupation, not incident recovery or throughput. |
| Healthy control | Same job-free station layout, fully charged at tick 0. | No recommendation or execution; useful for uncluttered monitoring/control. |
| Critical battery with a job | Existing trusted `InjectLowBattery` plus eligible job-bearing setup; detector already classifies battery below 30% with a job. | Driver sets level to 15% by default (medium; high requires below 15%). Direct command below 30% remains forbidden. Specific full-run profile/seed/horizon needs validation; do not call it a supported charging recovery. |
| Warehouse autonomous operations | `WarehouseConfig()` and seed/config-driven normal dynamics. | Feasible visually without algorithm edits; long-horizon acceptance is not assumed. Check soak results before promising congestion-free operation. |
| Aisle conflict | [`backend/sim/faults.go`](../backend/sim/faults.go), `InjectAisleConflict`, records a fault only; physical state is unchanged. | Not a physical blockage generator and not a separate incident-detector type. Any congestion story requires a validated trusted setup using actual dynamics, not merely this record. |

[`evals/src/dataset.ts`](../evals/src/dataset.ts) also has synthetic decision/probe cases (critical battery, collected job, station reservation, poisoned runtime, multiple incidents, stale/fabricated evidence). These are grounded contract fixtures, **not proof of turnkey simulator profiles**. [`evals/src/recovery.ts`](../evals/src/recovery.ts), [`backend/evalrecovery/experiment.go`](../backend/evalrecovery/experiment.go) and [`measurement.go`](../backend/evalrecovery/measurement.go) already implement five paired Go experiments with initial equality/hash checks and 200-tick windows. These direct-command experiments use a trusted test operator and in-memory domain events, not human GraphQL approval or durable PostgreSQL audits.

## 6. Safe restart and scenario-control options

### Option A: retain launcher-level session replacement (smallest first increment)

Use the existing stop/join/close/cleanup lifecycle, preserve sanitized evidence under a distinct run name before it can be overwritten, then launch a new approved profile with fresh credentials, cookie/session state and service. Current launcher reruns the one scenario; profile selection and evidence retention are future work. Existing cleanup destroys its private test schemas and ephemeral container, so preserve evidence before cleanup if review retention is required. Never use shared Compose volume deletion for reset.

This avoids an in-memory restore contract and is the safest initial implementation choice. A browser restart button would still need a new bounded local host/controller mechanism; the current gateway cannot restart its own PowerShell parent. Do not describe launcher rerun as an existing one-click runtime restart.

### Option B: local-only session manager for interactive controls

A future trusted owner can serialize lifecycle requests outside the agent surface. Accept only a server-defined scenario ID, current session/generation and bounded enum controls; no robot positions, arbitrary seed/config payload, database schema/URL, file path, command, fault target or injected script. Scenario factories and fault schedules stay trusted and outside the agent.

For reset: stop accepting old-generation decisions; stop and join the ticker; drain in-flight service requests; retain/capture evidence; construct a **new** simulator, lifecycle, canonical map and runtime ID; atomically publish the new session; rotate/invalidate browser sessions as appropriate; reject stale controls. Keep historical proposal/audit records when using persistent storage. Existing foreign-runtime checks prevent replay into the new service, but old service references must be drained too. Never clear poison and reuse an uncertain service. Reconciliation of a poisoned run is a separate requirement; reset must not imply its history was repaired.

For pause/resume/speed/step: one owner controls scheduling and calls only `Service.Tick()`. A pause acknowledgement must mean any in-flight tick finished. Step should be allowed only while paused, with bounded count; speed changes pacing, never drains, thresholds or algorithms. Decide whether the trusted review workflow holds the scenario stable pending human choice (as the current demo does). Do not freeze or extend real proposal expiry merely by pausing ticks.

Lifecycle controls must use their own authenticated, same-origin, session-bound local boundary and audit accepted/denied lifecycle requests. Do not send them through Mastra tools, expose Driver/Commander, or add unrestricted simulation mutation. Interface details and authorization require a later approved ticket; no API is introduced here.

### Playback is a separate concept

Recorded-frame playback can pause/seek/change presentation speed without modifying a live simulation. It needs bounded frame storage and an unmistakable replay label; disable operational decisions while viewing historical frames. A live restart reconstructs a trusted factory/setup and advances fresh dynamics. `sim.State` is not a full checkpoint: recreating only visible snapshot fields loses RNG, routes, recovery/countdown internals and canonical proposals.

## 7. Evidence, provenance and genuinely missing contracts

| Evidence already present | Scope and absence |
| --- | --- |
| Simulator snapshot | `sim.State`: seed, tick, robots, jobs, stations, aisles and fault IDs/types/robots/aisle/tick. GraphQL omits seed, fault list and aisle occupancy; it exposes layout and current telemetry. No capture timestamp, scenario ID or public runtime ID in fleet state. |
| Incident references | [`agent/src/contracts.ts`](../agent/src/contracts.ts): strict snapshot reference (`scope`, tick, robotId, type), interpretation severity, exact telemetry, assessment and operator-attention response. Reference is not a durable incident episode. Tick/robot ID alone will collide across resets. |
| Action explanations | Strict rationale, purpose and action evidence: robot/station or source/recipient/job/candidate including distances and battery requirement. [`agent/src/reasoning.ts`](../agent/src/reasoning.ts) checks evidence; [`statements.ts`](../agent/src/statements.ts) checks prose claims. Not fields in persisted Proposal or GraphQL. |
| Proposal/receipt | Proposal ID, action/targets, created/expiry times, state, execution, submitter and `currentRuntime`; domain payload also includes deciding operator and decision timestamp. Mutation reports audit commit and whether this call attempted execution. No rationale/observation link or audit-history query. |
| Local demo artifacts | `reasoning.json`: model mode/localCalls/paidRequests, observed facts, validated reasoning and submission. `evidence.json`: proposal/schema, no preapproval execution, decision observed, initial/baseline/final/progress snapshots and queried audit summary. These files are not served by gateway routes. |
| Recovery experiment provenance | Scenario/source/goal, seed, initial hash, paired equality, start/end/horizon, setup faults, measurement schedule, intervention events and outcomes. Completion tick/time/censoring, job count, blocked/charging/station tick samples and stored battery. Snapshot hash does not serialize private simulator internals. |
| Saved model evaluation | [`docs/evidence/p5-04-live-full.json`](evidence/p5-04-live-full.json) and [`docs/phase5-evaluation-report.md`](phase5-evaluation-report.md) describe 11/12 GPT-6 Luna decision trials with a deterministic action hint. This is archived assisted decision evidence, not current demo execution/approval/audit proof or authorization for another paid call. |

Minimal future contracts, subject to review:

1. **Session/status and control acknowledgement:** local demo scenario catalogue (ID/label/version/mode), run/generation, tick, paused/running/review/ended/poisoned status, allowed bounded speeds and acknowledgement semantics. Use expected generation to reject stale controls; add lifecycle audit correlation. Keep public deployment entirely separate. No agent tool changes are needed.
2. **Movement/routes:** telemetry is sufficient for a static redesign. Smooth truthful motion needs sequential tick frames/cadence and generation handling; one-second polling can skip intermediate cells and must not animate a straight line through walls. Actual route display needs detached read-only active navigation/target/phase data supplied under the service lock, or verified per-tick recorded trails. `Reader.PlanRoute` in [`backend/sim/readers.go`](../backend/sim/readers.go) ignores other robots and is not the actual active route. No such route DTO exists today; do not fabricate it in the browser. Prefer an additive read-only view over changing algorithms or command signatures; any capability/schema change needs explicit approval.
3. **Explanation-to-proposal linkage:** bounded, validated reasoning attached to run/generation, observation tick and returned proposal ID; include deterministic/local versus live model provenance, purpose and exact evidence. Local session storage/read-only delivery can serve the demo; durable public rationale requires a separately approved persistence/transport design. Preserve Go proposal parameters as authority. No need for a live model or new action type.
4. **Recovery comparison:** read-only matched baseline/treatment identity and initial hash/equality, setup schedule, horizon, target job, command versus delivery status, censored completion metrics, measured tick-series and audit verification source. Existing local files/Go experiments supply calculation patterns, but there is no dashboard contract. Never compute percent speedup from an unfinished baseline or call domain events PostgreSQL proof.
5. **Frontend state:** separate authoritative feed from selection, animation and replay cursor; clear frames, receipts, explanation and caches on generation change; stop old requests and ignore old results. Preserve stale/poisoned/unknown states and human decision gating. Existing auth, fleet queries and approval mutations can remain for the first visual ticket.

No backend contract is required just to change typography/colors/panel composition. WebSockets, a replacement state library, a new database, new simulator algorithms and broader agent tools are not prerequisites. History persistence, public multi-user auth and deployment are separate design decisions, not implicit Phase 6 work.

## 8. Security and lifecycle risks

- **Public exposure:** loopback cookies grant a shared operator identity; there is no individual public login. Exact-origin/peer protections and credential-in-memory behavior must survive local changes. Do not publish the gateway or reuse its HTTP cookie design as a public auth architecture.
- **Reset races:** ticking, approve requests, stale polling and previously captured service references can cross a session swap unless drained/generation-checked. Existing runtime ownership alone does not stop an old service that is still reachable from executing its own old proposal.
- **Audit uncertainty:** Go mutation and SQL commit are not atomically reversible. A poisoned run needs evidence preservation and manual reconciliation; "Restart" cannot mean "the previous execution definitely failed". Avoid automatic mutation retries.
- **Proposal freshness:** real-clock expiry continues during pause/replay. Inbox reads may still show expired-by-time pending records; existing browser and Go gates must remain. Continuously ticking while reasoning is generated can invalidate the fresh-snapshot check; decide review pacing explicitly.
- **Proof versus display:** optimistic transfer animation, cached rationale or replay must never imply current Go admission, human approval, committed audit, repaired obstruction or completed delivery.
- **Ephemeral data:** launcher finally cleanup removes test schemas/container and temporary copy; evidence files are overwritten on the next run and a crash can leave stale lock/session/infrastructure. Recovery/retention policy needs validation, not broad cleanup commands.
- **CSP/accessibility:** current CSP forbids arbitrary inline scripts/styles and external assets; check SVG/CSS animation behavior under it. Preserve focus, text alternatives, non-color status information and reduced motion in the new design. Use original assets and styling with no Tesla branding.
- **Limited scenario evidence:** synthetic eval cases and one seed/horizon do not prove general recovery. The existing soak suite must remain visible; do not waive simulator failures for a polished demo.

## 9. Recommended minimal Phase 6 implementation order

1. Approve lifecycle scope: launcher replacement versus session manager, whether review pauses dynamics, scenario list, audit retention and replay semantics. Specify tests for stale generations, reset/approval/tick races, denied controls, expiry and poisoning before exposing controls.
2. Create the bounded local demo lifecycle/scenario owner using existing trusted factories and service methods; retain normal server/public boundaries. Start with existing traffic profile and healthy/routine-charge controls. Any relocation of tagged fixtures or harness packaging needs explicit scope approval; do not import test helpers into production casually.
3. Restyle/recompose the existing dashboard with original graphite/blue SVG/CSS; retain auth, polling, approval and receipt semantics. Build against existing snapshots first. Add component tests and verify the actual local gateway/CSP/browser layout before claiming visual acceptance.
4. Add read-only tick frames/actual route evidence and presentation interpolation, selection and playback with reduced motion and discontinuity/reset/staleness tests. No navigation algorithm changes.
5. Add deterministic explanation delivery linked to proposal/run, then guided incident-to-recommendation-to-human-decision UX. Add no-action/escalation paths. Keep proposer and operator independent and preserve rejection/expiry behavior.
6. Surface verified matched recovery comparisons and retained audit evidence, distinguishing transfer, delivery, test-operator experiments and actual human review. Expand only to validated scenarios. Run relevant offline, Go safety/runtime, real PostgreSQL, gateway security and frontend integration checks; obtain human demo evidence separately.
7. Plan public deployment separately after local acceptance; do not turn the local operator gateway into public hosting.

## 10. Tests, CI and inspection verification

Test results are recorded below after execution. No fixtures were altered or regenerated; no `-update`, `-warehouse-golden-proposal`, diagnostic tuning sweep or live evaluation was requested.

### Existing coverage and CI inspected

- [`Makefile`](../Makefile): `test` runs Go tests/vet plus agent/web/evals tests/typechecks. `test-integration` runs race-enabled tagged db/fleet/graphql Integration tests. [`backend/cmd/server/runtime_test.go`](../backend/cmd/server/runtime_test.go) covers configuration, worker failure/cancellation and joining ticks; [`backend/fleet/runtime_integration_test.go`](../backend/fleet/runtime_integration_test.go) covers tick/decision serialization and deterministic replay.
- Go safety/lifecycle/role/invariant/property/soak/golden tests are already in their respective packages. Fleet boundary integration tests cover duplicate approval, stale/tampered/foreign-runtime records, preflight at execution and persistence poisoning. Database integration tests skip without a test URL unless `FLEETOPS_REQUIRE_DB=1`; never infer database verification from unit package success alone.
- [`web/src/demo.test.tsx`](../web/src/demo.test.tsx), [`fleet.test.ts`](../web/src/fleet.test.ts), [`proposals.test.ts`](../web/src/proposals.test.ts), [`ApprovalInbox.test.tsx`](../web/src/ApprovalInbox.test.tsx), [`reassignment.test.tsx`](../web/src/reassignment.test.tsx) and [`App.test.tsx`](../web/src/App.test.tsx) cover authentication modes, feed parsing/polling, approval transport/gates, receipts and rendered content. Vite config uses a Node test environment; this is not browser layout/motion verification.
- [`agent/scripts/verify-integration.ps1`](../agent/scripts/verify-integration.ps1) runs a temporary-container Go race suite and real agent/backend checks. [`web/scripts/verify-integration.ps1`](../web/scripts/verify-integration.ps1) bundles frontend HTTP/render contracts and runs the copied tagged Go/PostgreSQL dashboard test. Neither was run for this document.
- [`evals/phase4/gateway_security_test.go`](../evals/phase4/gateway_security_test.go) tests origin/Host/peer/CSRF/session isolation, expiry/revocation, methods and cleanup. [`gateway_integration_test.go`](../evals/phase4/gateway_integration_test.go) tests real authenticated backend behavior. These files live outside the backend module and are copied into the temporary harness; ordinary `go test ./...` and `make test` do not include them.
- [`evals/src/model.test.ts`](../evals/src/model.test.ts) and [`model-cli.test.ts`](../evals/src/model-cli.test.ts) use offline doubles/injected generation and prohibit fetch in relevant cases. Ordinary eval unit tests do not launch real recovery Go runs or paid generation. The explicit recovery benchmark is separate.
- [`.github/workflows/ci.yml`](../.github/workflows/ci.yml): Ubuntu `test` installs locked npm packages and runs `make test`; independent `integration` provisions PostgreSQL 18.6 on loopback with required DB and runs `make test-integration`; `gitleaks` checks a pinned download/checksum and scans full Git history. All jobs run on push/PR. No dedicated gateway/browser demo job is present. Workflow configuration was inspected; remote CI status was not checked.

### Windows golden-file evidence

`git ls-files --eol backend/sim/testdata` returned `i/lf w/crlf attr/` for both `golden_seed42_tick200.json` and `golden_warehouse_seed42_tick300.json`. [`backend/sim/golden_test.go`](../backend/sim/golden_test.go) marshals LF JSON plus a trailing LF and compares exact bytes, so this checkout has the known line-ending mismatch. The migration attribute file [`backend/db/migrations/.gitattributes`](../backend/db/migrations/.gitattributes) applies `*.sql text eol=lf`; it does not normalize these JSON files.

The existing agent integration script normalizes goldens **only in an ephemeral container copy** before testing. That workaround was inspected, not executed here. Keep checked-in fixtures unchanged; a failing native Windows byte comparison alone does not establish changed simulator behavior. Conversely, do not label unrelated soak or semantic failures a CRLF issue.

A read-only check ran the existing `cmd/simrun` with seed 42 for default/tick 200 and warehouse/tick 300, captured stdout in memory, and compared it with each fixture. Both returned `rawEqual=False normalizedEqual=True` when replacing CRLF with LF **in the in-memory expected string only**. This verifies these two mismatches are line endings, without changing or generating fixture files.

### Results from this inspection

Environment: Windows PowerShell, Node `v24.21.0`; system Go reports `go1.26.0`, while `backend/go.mod` requires 1.27.1. Explicit follow-up Go commands used the already cached `C:/Users/Ayush/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.1.windows-amd64/bin/go.exe` with `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off`. No dependency installation was performed. Commands below are relative to their named package directory.

| Check | Actual output / result |
| --- | --- |
| Backend initial `go test ./...`, and explicit cached-Go `test ./...` in sandbox | Exit 1. Both golden tests failed; `TestRuntimeGraphQLFreshSnapshotsAndJobCompletion` also failed because loopback socket access was forbidden by sandbox permissions. Other tested package summaries were `ok`. |
| Cached-Go `test ./... -timeout 90s` outside sandbox | Exit 1. `auth`, `cmd/server`, `cmd/simrun`, `db`, `fleet`, `graphql`, `incident`, `proposal`, `routing`, `safety`, `scheduler` all `ok`; GraphQL model has no test files. Only `sim` failed: `TestGoldenSnapshot` at golden_test.go:50 and `TestWarehouseGoldenSnapshot` at :87, with "snapshot differs from golden fixture" and "warehouse snapshot differs from golden fixture" respectively. No soak failure was reported by this run. This is not a passing full backend suite. |
| Cached-Go `vet ./...` | Exit 0, no diagnostics. |
| Cached-Go `test -tags fleetops_test ./evalrecovery -count=1 -timeout 90s` | Exit 0: `ok github.com/AyushM7/fleetops/backend/evalrecovery 1.857s`. Runs existing paired-scenario, approval-boundary and measurement regressions without PostgreSQL or live generation. |
| Existing `cmd/simrun` read-only golden comparisons | Exit 0; both seed-42 outputs matched the corresponding expected JSON exactly after in-memory CRLF-to-LF normalization; raw bytes differed. Fixtures unchanged. |
| Web initial `npm test` | 4 failed suites, 2 passed; 17 tests passed. Failed suites could not load transformed modules from the Windows sandbox temporary `ssr` directory (`ENOENT`). |
| Web `node node_modules/vitest/vitest.mjs run --pool=threads` | `Test Files 6 passed (6)`, `Tests 37 passed (37)`; `TEST_EXIT=0`. Threads avoided the temporary-file failures without source/config changes. |
| Agent initial `npm test` | 7 failed suites, 1 passed; 14 tests passed. Same temporary transformed-module `ENOENT` issue. Integration test remains excluded by the existing script. |
| Agent threads retry with normal timeout | 1 failed test / 99 passed: local Mastra adapter test exceeded 5000 ms. No external provider call. |
| Agent `node node_modules/vitest/vitest.mjs run --pool=threads --no-file-parallelism --testTimeout=60000 --exclude src/backend.integration.test.ts` | `Test Files 8 passed (8)`, `Tests 100 passed (100)`, `TEST_EXIT=0`. CLI-only timeout/scheduling adjustment; repository config unchanged. |
| Evals initial `npm test` / `npm run typecheck` | Both failed to launch: `'vitest' is not recognized` / `'tsc' is not recognized`. Existing evals `.bin` lacks the Windows command wrappers. |
| Evals using existing agent-local Vitest, default pool | 8 failed suites / no tests: sandbox temporary transformed-module `ENOENT`. |
| Evals threads retry with normal timeout | 5 failed tests / 237 passed; five offline model/CLI tests exceeded 5000 ms. |
| Evals `node ../agent/node_modules/vitest/vitest.mjs run --pool=threads --no-file-parallelism --testTimeout=60000` | `Test Files 8 passed (8)`, `Tests 242 passed (242)`, `TEST_EXIT=0`. Uses the already installed Vitest 5.0.2 matching the package pin; no install or wrapper changes. |
| TypeScript typechecks | Web and agent `npm run typecheck` completed without diagnostics (exit 0). Direct `node node_modules/typescript/bin/tsc --noEmit` in web/agent and `node ../agent/node_modules/typescript/bin/tsc --noEmit` in evals each reported `TYPECHECK_EXIT=0`. |

Retries were prompted by observed Windows sandbox/temp-file or timeout failures. The original failures are retained above rather than reported as a clean default `make test`. Tests that require PostgreSQL were not provisioned/run as a database acceptance suite; package success does not prove those workflows. No Docker demo, real browser acceptance, live model evaluation, Gitleaks download or remote CI run was performed.

Document validation: all local Markdown link targets resolved (0 broken links), `git diff --check` exited 0, and final worktree inspection showed only the new `docs/demo-inventory.md`. These checks supplement the existing regression tests; no test/source/config files were added for this inspection ticket.

## 11. Unknowns requiring further investigation

- Choose launcher-level replacement or a persistent local session manager, and decide durable retention versus explicitly disposable demo audits. This inventory recommends options but does not approve a contract or lifecycle audit format.
- Validate each additional profile through the full service/GraphQL/PostgreSQL path. Five existing direct Go experiments establish reproducibility patterns, not five human-approved end-to-end demonstrations.
- Determine the smallest truthful route read model and tick-frame delivery cadence; actual routes are currently private. Validate animation, CSP and reduced motion in a browser before promising smooth realistic movement.
- Decide whether snapshot-scoped conditions are sufficient for the guided flow or whether stable incident episodes/history are needed. No persisted incident/history table or query currently exists.
- Decide explanation/evidence persistence and retention, including run/observation/proposal correlation across reset. Existing artifact files alone are not a product data contract.
- Test shutdown/crash recovery for stale launcher locks, active DB transactions, old browser requests and poisoned sessions. Do not infer safe in-process reset from process teardown tests.
- Live human approval, current PostgreSQL/gateway integration, public security/deployment, remote CI status and live model quality are not revalidated by this document. Saved Phase 4/5 evidence retains its original scope.
