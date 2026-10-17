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
There are no new product features, test-control HTTP endpoints or UI fault controls.
`-Submit` is mandatory: without it the runner refuses to submit. No provider API
key is needed. Do not add `--live` or configure a paid provider for this guide.

Wait for `READY: http://127.0.0.1:... / proposal ...` and open that URL.
The warehouse loads immediately with a **Demo Mode** banner. No login form,
clipboard step, or bearer token is used. `evals/dist/p405/session.json` contains
only the non-secret URL and is removed at shutdown.

The gateway binds to loopback and validates the exact Host and request Origin.
It issues a random, host-only, HttpOnly, SameSite=Strict session cookie with a
20-minute deadline. HTTP is intentional for loopback; the cookie has no backend
credentials. Every GraphQL POST requires the cookie, exact Origin, JSON content
type and a custom header. No cross-origin access is enabled. Cookie names and
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
   `evals/dist/p405/reasoning.json` contains the exact Go snapshot, snapshot-scoped
   references and validated **local-adapter** output; it contains no credentials.
4. As the human operator, click **Approve** once. A transfer-success receipt must
   say this is **not delivery completion**. Source ownership clears, recipient
   R3 owns J1, and J1 initially remains **assigned / awaiting pickup**.
5. The trusted test process advances existing Go dynamics every two seconds for
   three ticks. Watch R3 move, **Picked up · delivering**, then **Delivered** at
   tick **45**. The comparison then advances to tick **242**: intervention J1 is
   done; the identical automatic baseline still holds J1 on blocked R1.

The requested proposal expiry is 30 minutes; the session closes after 20 minutes. Rejection stays visible without executing;
create the stop file when finished reviewing its outcome.
Expired/rejected review cannot be silently retried. Shut down and start a fresh
session if needed. Do not call an approval mutation from a script and label it
human approval.

## Verify and shut down

After `RECOVERY VERIFIED`, inspect the non-secret evidence:

```powershell
$e = Get-Content evals/dist/p405/evidence.json -Raw | ConvertFrom-Json
$e | Select-Object ModelMode, ProposalID, NoExecutionBeforeApproval, OperatorDecisionObserved
$e.Progress | ForEach-Object { $_ | Select-Object tick, jobs, robots }
$e.Audit | Format-Table Attempt, Subject, Accepted, Reason
$infra = Get-Content evals/dist/p405/infrastructure.json -Raw | ConvertFrom-Json
$sql = 'SELECT attempt,operator_subject,accepted,reason FROM "{0}".fleet_audit ORDER BY id' -f $e.AuditSchema
docker exec $infra.container psql -U postgres -d $infra.database -c $sql
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

The runner closes HTTP, drops its private test schemas, removes the non-secret
URL file, in-memory sessions/credentials and temporary backend copy, and stops
only its own container.
Sanitized reasoning/evidence remain in ignored `evals/dist/p405`. No existing
Compose service or volume is stopped. Close the browser test tab.

If the recovery session has already ended, preserve its sanitized evidence before
starting a new run: `evidence.json`, `reasoning.json` and `infrastructure.json` are
overwritten by the next run. For an outage-only repeat, start a fresh session,
open the URL, leave its proposal pending, then create the `stop` file while
the dashboard stays connected. Its polling must show the same stale warnings and
disabled decisions. This repeat is not another approved recovery demonstration.

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
delivery and committed audits. It does not approve the interactive scenario or
claim a human or live-LLM demonstration.

A live LLM run remains a separate explicitly authorized step with private model
configuration/API key, snapshot data sent to that provider, and possible charges.
The existing agent limits it to one model step, at most 2400 output tokens and a
30-second timeout. These are request bounds, not a dollar-cost guarantee. Do not
mark live-LLM acceptance passed based on this deterministic adapter.
