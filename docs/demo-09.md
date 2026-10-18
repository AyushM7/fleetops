# DEMO-09: multiple interactive warehouse scenarios

Verified locally on 2026-10-17 on `phase6-demo09-scenarios`. Real Go simulation, PostgreSQL, authenticated GraphQL and production React assets are used. Reasoning is the deterministic local Mastra adapter; no paid calls were made. Automated approvals below are test actions, not human acceptance evidence.

## Launch and use

With Go 1.27.1, Node 24.21.0, Docker and the existing locked dependencies available, run from the repository root:

```powershell
pwsh -NoProfile -File evals/phase4/start-demo.ps1 -LocalDemo -Submit
```

Open the READY URL. Choose **Scenario** above the warehouse. Selection preserves evidence and creates a fresh run. **Restart Scenario** recreates the current selection, resets pacing to 1× and starts paused or awaiting decision. No login, provider key or paid inference is required. This remains a local-only demo.

This machine's default Go was older. Verification used the already installed toolchain:

```powershell
$env:Path = 'C:\Users\Ayush\go\pkg\mod\golang.org\toolchain@v0.0.1-go1.27.1.windows-amd64\bin;' + $env:Path
$env:GOTOOLCHAIN = 'local'
$env:GOPROXY = 'off'
$env:GOSUMDB = 'off'
pwsh -NoProfile -File evals/phase4/start-demo.ps1 -LocalDemo -Submit
```

Do not rebuild assets while the demo runs: its gateway retains startup HTML, so changing asset hashes requires a new session before reloading. Graceful shutdown:

```powershell
New-Item evals/dist/p405/stop -ItemType File -Force | Out-Null
```

## Actual starting-state comparison

Every preset uses seed 42. Geometry, positions and jobs come from Go; the browser does not synthesize them.

| Scenario / fixed ID | Trusted setup and actual differences | Proposal and observable behavior |
| --- | --- | --- |
| Blocked job recovery / `blocked-reassignment` | Unchanged `NewReassignmentTestFixture`, tick 42, 8×8, five robots, S1 (7,0), wall at x=2 except offline R2's pocket exit (2,3). R1 (0,3) holds uncollected J1; idle R3 (4,3) has 98% battery. J1 pickup (4,4), delivery (6,4). | Existing Go-admitted J1 R1→R3 proposal. Approval transfers at tick 42. Subsequent ticks 43–45 pick up/deliver; R2 stays offline. Reject leaves J1 on R1. Matched tick-242 baseline stays blocked. Default scenario and direct historical acceptance path preserved. |
| Routine charging / `routine-charging` | `DefaultConfig`, 20×12, six robots, no shelf walls, stations (0,0)/(19,0). Trusted startup places R1 on S1 before simulator creation, matching the existing charging evaluation. Zero initial jobs, MaxJobs=1, NewJobEvery=10000, default AutoCharge=false. Exactly 1,000 ordinary idle ticks reduce all batteries from 100% to 50%. R1 is idle at S1, station free. | Existing `SetCharging` proposal R1/S1. Go admission is required; approval starts charging without ticking. Ordinary ticks add two battery points each. Browser observed 50% at tick 1000, 54% at 1002. Select R1 for current battery/status; station occupancy is in the map. No charging navigation or teleport occurs. |
| Healthy fleet operations / `healthy-operations` | Unmodified `WarehouseConfig`, tick 0, 20×12, six robots starting at x=1,3,5,7,9,11 on y=11; eight pending jobs, 100% batteries, no incidents. Two stations; shelf walls at x=2,3,5,6,7,8,10,11,12,13,15,16,17 for y=2…9. Existing dispatch/autocharge/recovery enabled. | No agent invocation or proposal. Play/Step available immediately. Tick 1 assigns jobs and moves all six robots. Unit checks observe movement and delivery within 40 ticks; browser observed operations at tick 36. Later ordinary congestion/incidents can occur; this is a healthy starting state, not a perpetual health guarantee. |
| No eligible recovery / `blocked-no-candidate` | Blocked fixture at tick 42 followed by the existing evaluation's trusted low-battery injections into R3/R4/R5, each 15%. Visitors cannot choose fault targets or levels. | Go confirms zero admitted transfer candidates at startup. No agent invocation/proposal. Operator-attention description and real incidents remain visible; time controls work. Step 42→43 showed R3 autonomously moving to (4,2) at 14%, S1 reserved for R3, J1 still on R1. This is existing autonomous charging, not an approved recovery or bypass of the command's 30% floor. Eligibility can change later. |

Charging and blocked recovery retain human decisions. Healthy/escalation presets do not automatically submit proposals later. Select/restart an actionable preset for a new proposal. No new recovery command, offline repair, charging navigation or guaranteed incident-free soak is claimed. Descriptions state the initial setup; current telemetry comes from tick, selection, incidents and job progress.

## Trusted controller and safety

The existing DEMO-04/06 controller adds a fixed allowlist and scenario factory. Local `/demo/scenario/<fixed-id>` accepts the same exact empty JSON object, generation, loopback peer, exact Host/Origin, session cookie, CSRF header, content type and Fetch Metadata protections as restart. Arbitrary IDs, query/config payloads, seeds, fault targets, robot positions, paths and commands are rejected. No public GraphQL mutation or agent capability was added.

The lifecycle mutex still serializes GraphQL, ticks, capture and replacement. Switching drains the active tick/decision, stops pacing, captures committed history, constructs a fresh simulator/service/runtime/generation, validates fresh identity and requested scenario, then publishes. The single existing harness pulse owns ticking; no worker is created by Play or selection. Speeds remain 4/2/1 seconds per tick without catch-up.

No-action runs record their service runtime ID too. Capture includes scenario ID, initial/final snapshots, generation/runtime identity, poison flag and full committed proposal/audit rows. Historical rows are retained. Capture/factory/identity/scenario/lifecycle-write failures leave the retired run stopped and inaccessible. Replacing poison does not reconcile historical uncertainty.

Old-generation GraphQL, restart, selection and controls fail closed. An old proposal ID sent with the current generation is denied/audited by the existing service. Browser generation changes clear snapshots, selection, inbox polling, receipts and stale IDs, aborting retired requests. Other tabs discover changes through status polling.

Actionable runs retain decision gating: only rejection or successful approved execution opens ticking. Wall-clock expiry/failed approval do not open it. No-action runs start paused with time controls available; poison still disables mutation. Admission, independent proposer/operator identity, persistence, rejection, expiry and execution checks remain unchanged.

All seven frozen rules, production schema, agent tool signatures, simulator routing/scheduling/movement/battery algorithms and golden fixtures are unchanged. No dependencies were added. Scenario state belongs to each run; public visitor isolation/authentication/hosting remain future work. Local tabs still share one trusted demo owner.

## Tests and real output

| Check | Actual result |
| --- | --- |
| Native Go 1.27.1: `go test -tags fleetops_test ./fleet -run TestDemoScenarioPresets -count=1 -v` | PASS, all four presets: initial conditions; identical initial/100-tick repeat state; charging preflight and 50→52% effect; healthy movement/delivery; low-battery recipients; distinct snapshots; invalid ID. |
| Temporary launcher checkout, PostgreSQL: `go test -tags fleetops_test ./fleet -run '^(TestDemo.*)$' -count=1 -v -timeout 240s` | PASS, fleet **82.863s**. Gateway security/workflow, controls, poison/expiry, historical restarts, four-scenario integration, active-tick switch draining. |
| Final affected native suite: `go test -tags fleetops_test ./fleet -run '^(TestDemoScenariosPostgres\|TestDemoRestartIntegration\|TestDemoController.*)$' -count=1 -v -timeout 180s` | PASS, fleet **23.101s**. Fresh/no-action runtimes, selected restarts, stale proposal denial/audit, captured poisoned runtime replaced with healthy operations. |
| Web serial Vitest | **10 files passed, 67 tests passed**. New tests cover accessible selector/loading/busy/context; request allowlist; stale, failed and mismatched acknowledgements without retry; unknown status; scenario-specific controls. Existing permissions, uncertainty, expiry, generation, transport and design regressions pass. |
| Web typecheck/build; agent typecheck/build | Exit 0; final Vite production build transformed **25 modules**. |
| Agent serial Vitest, excluding separate backend integration entry | **8 files passed, 100 tests passed**. Default parallel pool initially failed seven suites with temporary SSR ENOENT; serial rerun passed. New charging/reassignment adapter paths also ran against Go/PostgreSQL in the demo suites. |
| Native `go test ./... -count=1 -timeout 180s`, repeated with loopback access | Exit 1: only **TestGoldenSnapshot** and **TestWarehouseGoldenSnapshot** fail with the documented Windows CRLF/LF byte mismatch. Initial sandbox run additionally denied a server test's loopback socket; that test passes with socket access. Goldens untouched. |
| Cached Linux Go 1.27.1, full tagged race suite and vet | Exit 0, **every package passed**, fleet **13.455s**, sim **68.119s**; no race/vet diagnostics. Only ephemeral golden copies were LF-normalized. Node-only/interactive tests excluded here ran separately on Windows. |
| Final clean launcher `-LocalDemo -Submit -CheckOnly` | Exit 0, fleet **23.887s**, including all scenario, security, controls, restart and fail-closed selection tests. |
| Final focused serial race suite and vet after controller cleanup | Exit 0, fleet **3.956s**, no race/vet diagnostics. A preceding parallel attempt timed out on the shared PostgreSQL migration lock; rerunning with documented `-p 1 -parallel 1` passed. |
| Final clean interactive launcher and browser session | Exit 0, fleet **134.727s**; final evidence captured, ephemeral container and temporary checkout cleaned by launcher, session file and run lock absent. |

Exact frontend commands:

```powershell
node web/node_modules/vitest/vitest.mjs run --root web --pool=threads --no-file-parallelism --testTimeout=60000
npm --prefix web run typecheck
npm --prefix web run build
node agent/node_modules/vitest/vitest.mjs run --root agent --pool=threads --no-file-parallelism --exclude src/backend.integration.test.ts --testTimeout=60000
npm --prefix agent run typecheck
npm --prefix agent run build
```

Full Linux command in the temporary copy, using isolated PostgreSQL:

```sh
go test -p 1 -parallel 1 -race -tags fleetops_test ./... -skip 'TestPhase4AcceptanceDemo|TestDemoRestartIntegration|TestDashboardHTTPIntegration|TestDemoScenariosPostgres' -count=1 -timeout 240s
go vet -tags fleetops_test ./fleet
```

Final focused race command:

```sh
go test -p 1 -parallel 1 -race -tags fleetops_test ./fleet -run '^(TestDemoControls.*|TestDemoController.*|TestDemoGateway.*|TestDemoScenarioPresets)$' -count=1 -timeout 180s
go vet -tags fleetops_test ./fleet
```

The initial launcher was interrupted during toolchain setup; infrastructure/work directory and lock were retained. Cached Go then ran real suites/browser acceptance, closing gracefully with PASS, fleet **400.591s**. During screenshot work a rebuild removed the hash retained by the running gateway; a reload produced an asset error. Final starting captures use a fresh gateway. No fabricated screenshot, DOM override, frontend coordinate injection, provider result or human approval is claimed.

## Browser acceptance and screenshot manifest

The in-app browser exercised all four choices, approval/delivery, charging/battery progression, healthy activity, no-action escalation, switches with pending proposals and running ticks, and restart for each selection. Charging restart returned tick 1000, zero old receipts and zero old selections. Healthy/escalation had no Approve buttons. Keyboard End selected escalation; Enter selected real robot telemetry.

CSS viewports: desktop 1280×720, mobile 390×844. Client/scroll widths matched at 1265 and 375px. The native labelled selector is at least 44px tall and linked to its description. Mobile scrolls naturally; the 20×12 map is denser. Full screen-reader, physical-device, cross-engine, forced-colors and 200% zoom audits were not run.

Measured desktop warehouse/context widths were 843.2/421.6px, preserving the 67/33 split. The final clean browser session had **zero warning/error log entries** before deliberate shutdown. The original browser database had four create audits, two independent approve audits and two execute audits; both executions succeeded, with two other proposals still pending. These counts matched retained committed evidence before cleanup.

All JPEGs are genuine and unedited. [Capture manifest](evidence/demo-09/captures.json) lists filenames, dimensions, bytes and SHA-256. [Browser observations](evidence/demo-09/browser-checks.json) retain visible telemetry/measurements. Earlier progression images precede only the final clarification that descriptions describe starting setup.

| Comparison | Desktop start | Mobile start | Later actual state |
| --- | --- | --- | --- |
| Blocked | [42](evidence/demo-09/blocked-tick-42-desktop.jpg) | [42](evidence/demo-09/blocked-tick-42-mobile.jpg) | [43](evidence/demo-09/blocked-tick-43-desktop.jpg), [45](evidence/demo-09/blocked-tick-45-delivered-desktop.jpg). Go telemetry confirmed delivery; viewport shows receipt/changed position. |
| Charging | [1000](evidence/demo-09/charging-tick-1000-desktop.jpg) | [1000](evidence/demo-09/charging-tick-1000-mobile.jpg) | [1002, battery 54%](evidence/demo-09/charging-tick-1002-desktop.jpg). |
| Healthy | [0](evidence/demo-09/healthy-tick-0-desktop.jpg) | [0](evidence/demo-09/healthy-tick-0-mobile.jpg) | [1 mobile](evidence/demo-09/healthy-tick-1-mobile.jpg), [36 desktop](evidence/demo-09/healthy-tick-36-desktop.jpg). |
| No eligible recovery | [42](evidence/demo-09/no-candidate-tick-42-desktop.jpg) | [42](evidence/demo-09/no-candidate-tick-42-mobile.jpg) | [43 mobile](evidence/demo-09/no-candidate-tick-43-mobile.jpg), autonomous motion without a recovery proposal. |

## Exact files changed; unfinished or guessed

Source/tests:

- `agent/scripts/phase4-demo-reasoning.mjs`
- `backend/fleet/demo_export_test.go`
- `backend/fleet/demo_scenarios_test.go`
- `evals/phase4/controller_test.go`
- `evals/phase4/controller_security_test.go`
- `evals/phase4/controls_test.go`
- `evals/phase4/demo_test.go`
- `evals/phase4/gateway_test.go`
- `evals/phase4/restart_integration_test.go`
- `evals/phase4/scenario_test.go`
- `evals/phase4/scenarios_integration_test.go`
- `evals/phase4/start-demo.ps1`
- `web/src/App.tsx`
- `web/src/SimulationControls.tsx`
- `web/src/restart.tsx`
- `web/src/scenarios.tsx`
- `web/src/scenarios.test.tsx`
- `web/src/styles.css`

Docs/evidence: `docs/phase4-demo.md`, this file, every JPEG named by `docs/evidence/demo-09/captures.json`, that manifest and `browser-checks.json`. Local logs and full lifecycle/database evidence remain under ignored `evals/dist/`.

Remaining limits: local shared operator/20-minute session; ephemeral simulator state; sampled movement; later congestion/eligibility changes; no automatic dynamic re-analysis; expired actionable proposals require restart. Evidence is committed history, not a restorable checkpoint. Public deployment, DEMO-07, new commands, rewind and provider inference remain outside this ticket. No requirements were guessed, dependencies added, branches changed, or commits/pushes/merges/deployments performed.

