# Phase 4 repeatable local recovery review

This is a **seeded Go simulation with real PostgreSQL, GraphQL and React**.
The default reasoning demonstration uses Mastra's **deterministic local model
adapter, not a live LLM**. It does not establish live provider reasoning quality.
No paid request is authorized or made by these commands. Normal backend
authorization, safety rules and schema are unchanged. The local gateway lives
only in the temporary demo test checkout; it is never a production handler.

## Start

Use PowerShell 7, Docker, Node **24.21.0** and the repository Go toolchain
(**1.27.1**). From the repository root, with dependencies already installed:

```powershell
Set-Location C:\Users\Ayush\Desktop\Projects\fleetops
node --version  # must be v24.21.0
pwsh -NoProfile -File evals/phase4/start-demo.ps1 -LocalDemo -Submit
```

For a clean checkout, first run `npm ci --prefix agent` and `npm ci --prefix web`.
The runner builds both apps, creates its own loopback-only PostgreSQL container
and temporary backend test copy, and reuses P4-03/P4-04's trusted scenario helpers.
DEMO-04 adds Restart Scenario; DEMO-06 adds Play/Pause, Step Forward and fixed
simulation speeds inside this temporary local harness. There are no public
GraphQL lifecycle mutations or UI fault controls.
`-Submit` is mandatory: without it the runner refuses to submit. No provider API
key is needed. Do not add `--live` or configure a paid provider for this guide.

Wait for `READY: http://127.0.0.1:... · Restart Scenario enabled` and open that URL.
The warehouse loads immediately with a **Demo Mode** banner. No login form,
clipboard step, or bearer token is used. `evals/dist/p405/session.json` contains
only the non-secret URL and is removed at shutdown.

The gateway binds to loopback and validates the exact Host and request Origin.
It issues a random, host-only, HttpOnly, SameSite=Strict session cookie with a
20-minute deadline. HTTP is intentional for loopback; the cookie has no backend
credentials. Every GraphQL and local lifecycle POST requires the cookie, exact Origin, JSON
content type and a custom CSRF header. GraphQL and restart also require the
current generation header; lifecycle bodies must be exactly `{}`. Step also
requires the observed tick header. No cross-origin access is enabled. Cookie names and
sessions are unique per run; sessions and backend credentials stay in memory.
This explicitly enabled local simulation gives visitors the shared demo operator
identity; it is not suitable for a public or multi-user deployment.

## Human review

1. Confirm **Demo Mode** and the warehouse. The server uses the independent
   operator identity; Mastra submits as proposer. The runner never approves.
2. Confirm tick **42**: R1 holds uncollected **J1**, battery **100%**, blocked,
   waiting **42 ticks**. Go reports high-severity traffic blockage. R2 is offline
   at the pocket exit. This is not a low-battery incident. R3 is healthy/idle.
3. In **Approval inbox**, inspect `ReassignUnstartedJob`: J1, **R1 → R3**, pending,
   submitted by `proposer`. Job progress must still show J1 assigned to R1.
   The current run’s `reasoning.json` below `evals/dist/p405/history/` contains the exact Go snapshot, snapshot-scoped
   references and validated **local-adapter** output; it contains no credentials.
4. As the human operator, click **Approve** once. A transfer-success receipt must
   say this is **not delivery completion**. Source ownership clears, recipient
   R3 owns J1, and J1 initially remains **assigned / awaiting pickup**.
5. The simulation stays **Paused** after the decision. Press **Play** to advance
   existing Go dynamics. At 1× each tick takes at least two seconds. Watch R3
   move, **Picked up · delivering**, then **Delivered** at tick **45**. Expand
   Job progress to inspect the authoritative delivery state. Approval itself
   transfers J1; it does not complete delivery.
6. Press **Pause** to drain any active tick. After the success response there
   are no further automatic ticks until Play. **Step Forward** while paused
   advances exactly one tick and returns the resulting warehouse snapshot.
7. **Speed** changes only pacing: 0.5× = four seconds, 1× = two seconds,
   2× = one second per tick. Play is idempotent; Step is unavailable while
   running. The integer tick and all simulator algorithms remain unchanged.

The requested proposal expiry is 30 minutes; the session closes after 20 minutes.
The initial **Awaiting Decision** state blocks Play and Step on the server as
well as in the UI. Rejection never executes the reassignment; Play/Step then
follow the genuine no-intervention path (J1 remains on blocked R1 in the matched
200-tick comparison). Expiry uses real wall-clock time even while paused. An
expired or failed decision does not open the tick gate; restart for a fresh
proposal. Create the stop file when finished reviewing its outcome.
Expired/rejected proposals cannot be retried. Use **Restart Scenario** to create
a fresh pending proposal without relaunching PowerShell. Do not call an approval mutation from a script and label it
human approval.

## Verify and shut down

Evidence is retained in uniquely named session/run folders under
`evals/dist/p405/history/`. Each `capture-*.json` contains the generation, runtime
ID, poison flag, snapshots, matched baseline, and full committed PostgreSQL
proposal/audit rows. `reasoning.json` retains the exact deterministic adapter
output per run. Lifecycle files record restart requests and outcomes.

```powershell
$latest = Get-ChildItem evals/dist/p405/history -Recurse -Filter 'capture-*.json' |
    Sort-Object LastWriteTime -Descending | Select-Object -First 1
$e = Get-Content -LiteralPath $latest.FullName -Raw | ConvertFrom-Json
$e | Select-Object Generation, RuntimeID, Poisoned, Warning
$e.Evidence | Select-Object ModelMode, ProposalID, NoExecutionBeforeApproval
$e.Database.audit | Format-Table attempt, operator_subject, accepted, reason
$infra = Get-Content evals/dist/p405/infrastructure.json -Raw | ConvertFrom-Json
docker exec $infra.container psql -U postgres -d $infra.database -c 'SELECT attempt,operator_subject,accepted,reason FROM public.fleet_audit ORDER BY id'
```

Expect committed `create/proposer`, `approve/operator`, `execute/operator` records
with accepted=true. These rows are queried directly from PostgreSQL after commit;
they are not just GraphQL acknowledgment flags. Simulator completion remains
runtime state; the evidence does not claim persisted simulator history.

To test a real backend outage, leave the dashboard connected and run:

```powershell
New-Item evals/dist/p405/outage -ItemType File -Force | Out-Null
```

The local gateway closes. After polling fails, the dashboard retains an
explicitly stale snapshot, warns that incident/job progress cannot be confirmed,
and disables decisions. Closing the tab clears its view. Normal dashboard mode
continues to provide the existing bearer login and Disconnect behavior.

Gracefully stop from the second terminal:

```powershell
New-Item evals/dist/p405/stop -ItemType File -Force | Out-Null
```

The runner closes HTTP, captures final evidence, removes the URL file,
in-memory sessions/credentials and temporary backend copy, then stops only its
own container. Successful shutdown removes the ephemeral database only after
capture. Historical evidence files remain; no Compose service or volume is
stopped. Close the browser test tab.

On any failed/interrupted harness run, the launcher retains its named PostgreSQL
container and temporary checkout and prints a warning. Reconcile uncertain
outcomes using the retained database before manually stopping that exact
container. A failed capture is never a success acknowledgement. Abrupt process
termination cannot guarantee final capture; stale lock/session files may still
need operator inspection. Retention is local evidence capture, not a production
backup or simulator checkpoint.

## Restart Scenario (DEMO-04)

Click **Restart Scenario** after reviewing, approving, rejecting, or leaving the
proposal pending. The button disables while the controller drains in-flight
browser operations and trusted progress ticks, captures committed history, and
constructs the same verified seed-42/tick-42 fixture with a new service, runtime
ID, lifecycle and proposal. The existing local Mastra proposer and Go validation
run again; restart never approves or executes a proposed robot action.

The response succeeds only when the replacement is ready and the retired run is
inaccessible through the browser. Old-generation GraphQL/restart requests fail
with 409. Old proposal IDs cannot execute in the replacement. Other tabs discover
the generation through authenticated status polling. Fleet data, inbox and
receipts are cleared when the generation changes; restart also aborts old UI
requests and clears receipts immediately. Lost acknowledgements require status
refresh, never automatic restart or approval retries.

A single local owner serializes lifecycle, requests and ticks outside Mastra and
public GraphQL. Status/restart accept no seed, configuration, path, robot, command
or fault parameters. Pool and gateway are session-owned; proposer HTTP servers
and subprocesses are closed/joined per setup. Committed rows stay in one isolated
database throughout repeated restarts. Capture/factory/lifecycle-write failure
blocks the old run; a fresh retry is possible after status refresh. Poison is
never cleared or reconciled by restart. The gateway’s original 20-minute session
deadline remains; restart does not extend it.

## DEMO-06 time ownership and limits

The existing 200 ms harness pulse is the only scheduler. Interactive mode starts
paused. Its controller mutex covers generation checks, decisions, `Service.Tick`,
status snapshots, pause, capture and replacement. Play sets one next-due time;
repeated Play cannot create workers or shift it. Slow work drops pacing pulses;
there is no catch-up burst. Speed changes reset the next-due time and do not tick.
Restart disables progression and drains the current tick before capture/factory
work, resets speed to 1×, and publishes a new paused tick-42 runtime/proposal.
The scheduler retains no reference to the retired service. Shutdown closes the
same owner and captures history. No ticker goroutines are added per control.

Controls are fixed same-origin local POST operations (`/demo/play`, `/demo/pause`,
`/demo/step`, `/demo/speed/0.5`, `/demo/speed/1`, `/demo/speed/2`). They retain
gateway loopback, Host, exact Origin, cookie, CSRF and generation validation.
Competing Steps are rejected; other operations serialize with reads and ticks. Pause waits for the current operation. Step
requires the observed tick and has a 200 ms minimum spacing, so concurrent tabs
cannot advance twice from one observation. Failed/unknown Steps are never
automatically retried. Poisoned or unavailable runs cannot Play or Step.

Demo status supplies the detached existing GraphQL read model and authoritative
control state under that same gate. One non-overlapping browser poll, 250 ms
after completion, replaces the independent fleet polling in demo mode. This is
bounded read-only sampling (at most four requests/second), faster than the
maximum one tick/second. Positions, batteries, incidents and jobs come from Go;
there is no interpolation or invented route. Network delays/background-tab
throttling can skip visible samples; the next display uses the current snapshot
without inventing missing positions. This is not a playback or frame archive.
There is no defined end tick in interactive mode; it runs until Pause, restart,
failure or the original 20-minute session limit. Status outages retain visibly
stale telemetry and disable decisions/time controls. Keyboard selection, focus,
reduced motion, receipts and normal bearer-authenticated dashboard mode remain.

Historical Phase 4/DEMO-04 evaluation below used a separate explicit acceptance
path: after approval it paced ticks 43–45 and then advanced to 242, verifying
matched-baseline recovery and committed audits. That path remains in tests;
the current interactive launcher never invokes it. Interactive progression has
no automatic postapproval ticking or jump to 242. Historical captures retain
their original meaning; new interactive final captures reflect the actual
operator-controlled tick and committed database history.

## Automated verification, with no human/live-LLM claim

```powershell
npm --prefix agent test
npm --prefix agent run typecheck
npm --prefix web test
npm --prefix web run typecheck
pwsh -NoProfile -File agent/scripts/verify-integration.ps1
pwsh -NoProfile -File web/scripts/verify-integration.ps1
pwsh -NoProfile -File evals/phase4/start-demo.ps1 -LocalDemo -Submit -CheckOnly
```

The first harness runs the full tagged Go race suite, both golden comparisons
and Phase 2 S1–S4 checks using an ephemeral LF-normalized copy of Windows golden
files. The second checks real dashboard client/rendering against Go/PostgreSQL,
including rejection, expiry, stale state and authorization. CheckOnly validates
the new demo setup, local Mastra generation, pending proposal, committed create
audit and zero preapproval execution, then shuts down. A separate automated
gateway scenario tests approval, rejection, stale/duplicate/expired decisions, R3
delivery and committed audits. DEMO-04 adds three real replacement runs, stale
generation/ID rejection, history retention, capture failure, serialized
decision/tick races, and lifecycle security checks. It does not approve the interactive scenario or
claim a human or live-LLM demonstration.

A live LLM run remains a separate explicitly authorized step with private model
configuration/API key, snapshot data sent to that provider, and possible charges.
The existing agent limits it to one model step, at most 2400 output tokens and a
30-second timeout. These are request bounds, not a dollar-cost guarantee. Do not
mark live-LLM acceptance passed based on this deterministic adapter.

## DEMO-04 validation (2026-10-17)

All commands used existing locked dependencies; no paid model requests or golden
fixture changes were made. The cached Go 1.27.1 executable was used on Windows
with `GOTOOLCHAIN=local`, `GOPROXY=off`, and `GOSUMDB=off`.

| Check | Actual result |
| --- | --- |
| Web: `node node_modules/vitest/vitest.mjs run --pool=threads --no-file-parallelism --testTimeout=60000` | `Test Files 7 passed (7)`, `Tests 42 passed (42)`. |
| Web: `npm run build`; agent: `npm run build` | Exit 0; TypeScript and production bundles built. |
| Temporary demo harness: `go test -tags fleetops_test ./fleet -run '^(TestDemo.*|TestDashboardHTTPIntegration)$' -count=1 -v -timeout 180s` | All DEMO tests passed, including fresh identity, repeated restart, decision/tick drain, stale controls/IDs, security, persistence failure, captured poison, and approved delivery. An initial SSR build from the repository root failed to resolve its web-relative entry; rebuilding from `web/` succeeded. The first combined test run failed only in dashboard contracts because a later web build had removed their SSR bundle; rebuilding that bundle and rerunning the contracts passed both charging/reassignment cases. |
| Frontend HTTP contracts after `vite build --ssr scripts/contract-entry.ts --outDir dist/contract` | `TestDashboardHTTPIntegration` PASS; both charging and reassignment against real PostgreSQL. |
| Cached `golang:1.27.1` image: `go test -p 1 -parallel 1 -race -tags fleetops_test ./db ./fleet ./graphql -run '^(TestDemoController.*\|TestDemoGateway.*\|TestIntegration.*\|TestRuntime.*\|TestBoundary.*\|TestReassignment.*)$' -count=1 -timeout 180s` | Exit 0: db, fleet and graphql all `ok`, including disconnected restart and explicit concurrency workers. An earlier broader selection failed because it included Node-only dashboard tests in the Go image; no race detector finding was reported. |
| Native backend: `go test ./... -count=1 -timeout 120s` | Exit 1: only `TestGoldenSnapshot` and `TestWarehouseGoldenSnapshot` failed with the documented Windows CRLF/LF byte mismatch. All other package summaries were `ok`; fixtures unchanged. |
| `go vet ./...`; temporary harness `go vet -tags fleetops_test ./fleet` | Exit 0; no diagnostics. |
| `pwsh -NoProfile -File evals/phase4/start-demo.ps1 -LocalDemo -Submit -CheckOnly` | Exit 0; `PASS`, fleet `16.003s`. Real PostgreSQL, proposer, restart regressions, evidence capture, and launcher cleanup ran. Session file, lock, temporary checkout and that launcher container were absent afterward. |
| Real in-app browser | Rejection receipt, mouse restart, keyboard restart, disabled/progress state, fresh tick 42/proposal, receipt clearing, capture-failure error, successful retry after restoring database access, and shutdown stale-data/disabled-control behavior were exercised. Automated UI testing, not human approval acceptance. Approval/execution/delivery were exercised separately by automated Go/PostgreSQL tests. |
| `git diff --check`; PowerShell parser | Exit 0; parser reported `PowerShell parse: PASS`. |

The temporary browser harness was stopped gracefully after final evidence
capture. Browser screenshots and per-run history remain under ignored
`evals/dist/demo04-browser/`; launcher CheckOnly history remains under
`evals/dist/p405/history/`. No commit, push, merge, branch switch, production
GraphQL change, simulator algorithm change, dependency addition, or DEMO-05 work
was performed. The original 20-minute loopback session limit and local evidence
retention/crash limitations above remain.

## DEMO-06 validation — 2026-10-17

The branch remained `phase6-demo06-controls`; no commits, pushes, merges or
branch switches occurred. Existing production backend files, schemas, safety
rules, agent tools, simulator algorithms, dependencies and goldens are unchanged.
The guide's mixed character encoding was normalized to UTF-8 while updating it.

| Check | Actual result |
| --- | --- |
| `node web/node_modules/vitest/vitest.mjs run --root web --pool=threads --no-file-parallelism --testTimeout=60000` | `Test Files 9 passed (9); Tests 63 passed (63)`. |
| `npm --prefix web run typecheck`; `npm --prefix web run build` | Exit 0; TypeScript and production bundle built. |
| `pwsh -NoProfile -File evals/phase4/start-demo.ps1 -LocalDemo -Submit -CheckOnly` | Exit 0: `PASS`, fleet `42.760s`. Real PostgreSQL, deterministic proposer, controls, gateway security, historical recovery/restart and zero preapproval execution. |
| `pwsh -NoProfile -File web/scripts/verify-integration.ps1` | Sequential rerun exit 0: charging `1.16s`, reassignment `1.20s`; fleet `7.467s`. Authenticated clients/rendering, decision/expiry/duplicate/stale boundaries and real delivery. |
| Native cached Go 1.27.1: `go test ./... -count=1 -timeout 120s` | Exit 1, only `TestGoldenSnapshot` and `TestWarehouseGoldenSnapshot` failed with the documented Windows CRLF/LF byte comparison. All other package summaries were `ok`; fixtures untouched. |
| Cached `golang:1.27.1`, PostgreSQL, `go test -p 1 -parallel 1 -race -tags fleetops_test ./... -skip 'TestPhase4AcceptanceDemo\|TestDemoRestartIntegration\|TestDashboardHTTPIntegration' -count=1 -timeout 240s` | Exit 0; every package `ok`, including fleet `7.505s` and sim `115.573s`. Only ephemeral golden copies were LF-normalized. Node-only/interactive harness tests were excluded here and exercised separately on Windows. |
| Additional `go test -race -tags fleetops_test ./fleet -run '^TestDemoControls' -count=1 -timeout 60s` | Exit 0: fleet `12.293s`, including 32 simultaneous Step/pulse requests and unknown tick acknowledgement handling. |
| Final focused race: `go test -race -tags fleetops_test ./fleet -run '^(TestDemoControls.*\|TestDemoController.*\|TestDemoGateway.*)$' -count=1 -timeout 120s`; `go vet -tags fleetops_test ./fleet` | Exit 0: fleet `6.127s`; no vet diagnostics. Includes cancellation, read/control serialization and unconfirmed resulting-state regressions added during browser verification. |
| Final interactive launcher, including all focused controls tests and real browser exercise | Exit 0: `PASS`, fleet `226.067s`; graceful evidence capture/cleanup. Session file and lock absent afterward. |
| `go vet ./...`; `git diff --check`; launcher PowerShell parser | Exit 0, no vet diagnostics; `PowerShell parse: PASS`. Git printed Windows line-ending notices. |

Tests cover actual tick progression, post-Pause quiescence, single Step, all
allowlisted speeds, identical simulator state at equal tick counts, duplicate
Play/pulses, decision/poison gates, real approved delivery versus rejected
baseline, expiry independent of ticks, restart drain, stale generations, 100
pause/resume/restart cycles, client cleanup, loading/disabled/error presentation
and no automatic mutation retries. A resulting-status failure after Step returns
503 and stops the owner, rather than acknowledging an unobserved state.

An initial frontend-contract/browser-launch pair was mistakenly run concurrently
against shared `web/dist`: Vite cleanup produced `EPERM` and the contract copy
lost its schema file. Both checks were rerun sequentially successfully. These
were build-artifact conflicts, not waived application failures. A later attempt
to reuse an already-finished race container failed with `No such container`;
the final native harness includes the added status-read failure regression.

Browser verification also exposed two controller issues that were fixed before
completion: cancelled observational reads could revoke readiness, and mutex
TryLock could reject Step merely because status polling was active. Observational
failures now leave lifecycle ownership intact; accepted controls still fail
closed if their resulting state cannot be read. A separate atomic Step guard
rejects competing Steps, while the lifecycle mutex serializes controls with
ordinary reads/ticks. Both exact overlaps have focused regressions and passed
the final native and race tests. The initially observed cancellation failure is
retained in capture 11 rather than hidden. Captures 12–17 verify the final code.

Real in-app browser evidence is in
[`docs/evidence/phase6-ui/demo-06/`](evidence/phase6-ui/demo-06/), with hashes in
[`captures.json`](evidence/phase6-ui/demo-06/captures.json) and recorded checks in
[`browser-checks.json`](evidence/phase6-ui/demo-06/browser-checks.json). These are
genuine unedited local Go/PostgreSQL captures, with automated test approval and
rejection, not a human-acceptance or live-LLM claim.

| Capture | Observed behavior |
| --- | --- |
| [01 — Pending tick 42](evidence/phase6-ui/demo-06/01-pending-tick42.jpg) | Blocked R1/J1, offline R2, Play/Step disabled, 1×. |
| [02 — Approved, paused tick 42](evidence/phase6-ui/demo-06/02-approved-paused-tick42.jpg) | Successful transfer receipt; no automatic ticks or delivery. |
| [03 — Play then Pause, tick 43](evidence/phase6-ui/demo-06/03-play-pause-tick43.jpg) | R3 `(4,4)`, battery 97%; tick stayed 43 for more than eight seconds after Pause. |
| [04 — Step tick 44](evidence/phase6-ui/demo-06/04-step-tick44.jpg) | Exactly one tick; R3 `(5,4)`, battery 96%. |
| [05 — Delivered tick 45](evidence/phase6-ui/demo-06/05-delivered-tick45.jpg) | 2× Play, then Pause; R3 `(6,4)`, battery 95%, Go-reported Delivered J1. |
| [06 — Rejected tick 42](evidence/phase6-ui/demo-06/06-rejected-tick42.jpg) | Execution none, audit committed; R1 retains J1. |
| [07 — Rejected, running tick 43](evidence/phase6-ui/demo-06/07-rejected-running-tick43.jpg) | Genuine no-intervention state: blocked R1, J1 unchanged. |
| [08 — Restart after Play](evidence/phase6-ui/demo-06/08-restart-pending-tick42.jpg) | Fresh tick 42/pending proposal, speed 1×, receipt cleared, gate restored. |
| [09 — Actual shutdown](evidence/phase6-ui/demo-06/09-stopped-unavailable.jpg) | Stale retained map, outage explanation and disabled controls/decisions. |
| [11 — Regression before fix](evidence/phase6-ui/demo-06/11-status-cancellation-regression-before-fix.jpg) | Actual cancelled-read failure found during verification, subsequently fixed and tested. |
| [12 — Final Step](evidence/phase6-ui/demo-06/12-final-step-tick43.jpg) | Final code: Step succeeds exactly 42→43 after approval. |
| [14 — Final acknowledged Pause](evidence/phase6-ui/demo-06/14-final-paused-after-play.jpg) | 2× Play/Pause, authoritative paused tick 49. |
| [15 — Final Step after Pause](evidence/phase6-ui/demo-06/15-final-step-after-pause.jpg) | Exactly 49→50; paused state and original receipt retained. |
| [16 — Final running restart](evidence/phase6-ui/demo-06/16-final-restart-pending-tick42.jpg) | Tick 42, pending gate and speed 1× after restarting from Running. |
| [17 — Final rejected Step](evidence/phase6-ui/demo-06/17-final-rejected-step-tick43.jpg) | No execution; Step reaches 43 with blocked R1 still holding J1. |

The default browser CSS viewport was 1280×720. DOM client/scroll width both
measured 1265px, with no horizontal overflow. Enter selected R1 and linked its
incident. Browser warning/error logs were empty before deliberate shutdown.
The first 2× Play-to-visible-tick measurement was 1849 ms including browser/UI
sampling overhead. A 0.5× exact-tick wait timed out; later authoritative state
showed continued progression through tick 48. This observation is retained,
not reported as a precise four-second browser timing measurement. Automated
pacing tests verify the fixed 4/2/1-second deadlines and absence of catch-up.
Final code also ran at 0.5× from tick 43 to 48 over an observed 21688 ms,
including UI/automation overhead, then paused successfully. Speed was switched
to 2×; Play/Pause reached 49, and Step reached exactly 50. No control error was
present. A final restart while Running restored the gate and cleared receipts;
rejection followed by Step retained J1 on R1. Final browser warning/error logs
were empty. The last native launcher and both test databases were shut down
gracefully after capture; retained history was not deleted.

Changed source files: `evals/phase4/controller_test.go`, `controls_test.go`,
`controls_behavior_test.go`, `controls_integration_test.go`,
`controller_security_test.go`, `gateway_test.go`, `scenario_test.go`,
`start-demo.ps1`; `web/src/App.tsx`, `restart.tsx`, `SimulationControls.tsx`,
`SimulationControls.test.tsx`, `styles.css`; this guide and the DEMO-06 evidence
directory above. No recording was made. Remaining limits are sampled rather
than interpolated movement, possible missed samples on delayed/background tabs,
the 20-minute session, ephemeral simulator state, and no new full accessibility
or cross-engine/device audit. No DEMO-07 work was started.
