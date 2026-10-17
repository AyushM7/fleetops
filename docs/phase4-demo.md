# Phase 4 repeatable local recovery review

This is a **seeded Go simulation with real PostgreSQL, GraphQL and React**.
The default reasoning demonstration uses Mastra's **deterministic local model
adapter, not a live LLM**. It does not establish live provider reasoning quality.
No paid request is authorized or made by these commands. Normal server behavior,
safety rules, schema and dashboard code are unchanged.

## Start

Use PowerShell 7, Docker, Node **24.21.0** and the repository Go toolchain
(**1.27.1**). From the repository root, with dependencies already installed:

```powershell
Set-Location C:\Users\Ayush\Desktop\Projects\fleetops
node --version  # must be v24.21.0
pwsh -NoProfile -File evals/phase4/start-demo.ps1 -Submit
```

For a clean checkout, first run `npm ci --prefix agent` and `npm ci --prefix web`.
The runner builds both apps, creates its own loopback-only PostgreSQL container
and temporary backend test copy, and reuses P4-03/P4-04's trusted scenario helpers.
There are no new product features, test-control HTTP endpoints or UI fault controls.
`-Submit` is mandatory: without it the runner refuses to submit. No provider API
key is needed. Do not add `--live` or configure a paid provider for this guide.

Wait for `READY: http://127.0.0.1:... · proposal ...`. In a second PowerShell:

```powershell
Set-Location C:\Users\Ayush\Desktop\Projects\fleetops
$session = Get-Content evals/dist/p405/session.private.json -Raw | ConvertFrom-Json
Set-Clipboard -Value $session.operatorToken
Start-Process $session.url
```

The private file is ignored by Git and removed on shutdown. Do not print its
contents, paste it into chat, or share the clipboard. Use only these ephemeral
demo credentials, not production credentials.

## Human review

1. Paste the operator token into the masked **Bearer token** field and connect.
   It has a different subject from the proposer. The runner never approves.
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

The requested proposal expiry is 30 minutes; the session closes after 20 minutes.
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

The Go HTTP listener closes. After polling fails, the dashboard must retain an
explicitly stale snapshot, warn that incident/job progress cannot be confirmed,
and disable decisions. Click **Disconnect**: the map, job data, inbox/receipt and
token session disappear. For viewer monitoring in a fresh run, copy
`$session.viewerToken`; fleet/incident/job reads work but decisions are unavailable.

Gracefully stop from the second terminal:

```powershell
New-Item evals/dist/p405/stop -ItemType File -Force | Out-Null
Set-Clipboard -Value ''
Remove-Variable session -ErrorAction SilentlyContinue
```

The runner closes HTTP, drops its private test schemas, removes private session
credentials and the temporary backend copy, and stops only its own container.
Sanitized reasoning/evidence remain in ignored `evals/dist/p405`. No existing
Compose service or volume is stopped. Close the browser test tab.

If the recovery session has already ended, preserve its sanitized evidence before
starting a new run: `evidence.json`, `reasoning.json` and `infrastructure.json` are
overwritten by the next run. For an outage-only repeat, start a fresh session,
connect as operator, leave its proposal pending, then create the `stop` file while
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
pwsh -NoProfile -File evals/phase4/start-demo.ps1 -Submit -CheckOnly
```

The first harness runs the full tagged Go race suite, both golden comparisons
and Phase 2 S1–S4 checks using an ephemeral LF-normalized copy of Windows golden
files. The second checks real dashboard client/rendering against Go/PostgreSQL,
including rejection, expiry, stale state and authorization. CheckOnly validates
the new demo setup, local Mastra generation, pending proposal, committed create
audit and zero preapproval execution, then shuts down. It does **not** approve
or claim a human or live-LLM demonstration.

A live LLM run remains a separate explicitly authorized step with private model
configuration/API key, snapshot data sent to that provider, and possible charges.
The existing agent limits it to one model step, at most 2400 output tokens and a
30-second timeout. These are request bounds, not a dollar-cost guarantee. Do not
mark live-LLM acceptance passed based on this deterministic adapter.
