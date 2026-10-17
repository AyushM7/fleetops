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
DEMO-04 adds a fixed Restart Scenario operation only inside this temporary local
harness. There are no public GraphQL lifecycle mutations or UI fault controls.
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
current generation header; lifecycle bodies must be exactly `{}`. No cross-origin access is enabled. Cookie names and
sessions are unique per run; sessions and backend credentials stay in memory.
This explicitly enabled local simulation gives visitors the shared demo operator
identity; it is not suitable for a public or multi-user deployment.

## Human review

1. Confirm **Demo Mode** and the warehouse. The server uses the independent
   operator identity; Mastra submits as proposer. The runner never approves.
2. Confirm tick **42**: R1 holds uncollected **J1**, battery **100%**, blocked,
   waiting **42 ticks**. Go reports high-severity traffic blockage. R2 is offline
   at the pocket exit. This is not a low-battery incident. R3 is healthy/idle.
3. In **Approval inbox**, inspect `ReassignUnstartedJob`: J1, **R1 â†’ R3**, pending,
   submitted by `proposer`. Job progress must still show J1 assigned to R1.
   The current run’s `reasoning.json` below `evals/dist/p405/history/` contains the exact Go snapshot, snapshot-scoped
   references and validated **local-adapter** output; it contains no credentials.
4. As the human operator, click **Approve** once. A transfer-success receipt must
   say this is **not delivery completion**. Source ownership clears, recipient
   R3 owns J1, and J1 initially remains **assigned / awaiting pickup**.
5. The trusted test process advances existing Go dynamics every two seconds for
   three ticks. Watch R3 move, **Picked up Â· delivering**, then **Delivered** at
   tick **45**. The comparison then advances to tick **242**: intervention J1 is
   done; the identical automatic baseline still holds J1 on blocked R1.

The requested proposal expiry is 30 minutes; the session closes after 20 minutes. Rejection stays visible without executing;
create the stop file when finished reviewing its outcome.
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
and Phase 2 S1â€“S4 checks using an ephemeral LF-normalized copy of Windows golden
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
