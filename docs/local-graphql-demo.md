P3-06 verified the actual Go server over local HTTP with Compose PostgreSQL
(`postgres:18.6`) and Go 1.27.1 on Windows. The complete demonstration passed.
No simulator, API, or authentication behavior was changed.

Run the following in one PowerShell 7 terminal. The existing private `.env` must
contain the Compose database credentials. Credentials and bearer tokens stay in
memory and are never printed. The Go executable below is the installed 1.27.1
toolchain used for verification; adjust only its location on another machine.
Port 8080 must be free.

```powershell
Set-Location C:/Users/Ayush/Desktop/Projects/fleetops
$ErrorActionPreference = 'Stop'
docker compose up -d --wait postgres
if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL startup failed' }

$demoEnv = @{}
foreach ($line in Get-Content -LiteralPath .env) {
    if ($line -match '^(POSTGRES_USER|POSTGRES_PASSWORD|POSTGRES_DB|POSTGRES_HOST_PORT)=(.*)$') {
        $demoEnv[$Matches[1]] = $Matches[2].Trim().Trim('"').Trim("'")
    }
}
$databasePort = if ($demoEnv['POSTGRES_HOST_PORT']) { $demoEnv['POSTGRES_HOST_PORT'] } else { '5432' }
$env:FLEETOPS_DATABASE_URL = 'postgresql://' + [uri]::EscapeDataString($demoEnv['POSTGRES_USER']) + ':' +
    [uri]::EscapeDataString($demoEnv['POSTGRES_PASSWORD']) + '@127.0.0.1:' + $databasePort + '/' +
    [uri]::EscapeDataString($demoEnv['POSTGRES_DB']) + '?sslmode=disable'

$demoTokens = @{}
$credentials = @(foreach ($role in @('viewer', 'proposer', 'operator')) {
    $token = [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(32)).ToLowerInvariant()
    $demoTokens[$role] = $token
    $digest = [Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes($token))
    @{ subject = "demo-$role"; role = $role; sha256 = [Convert]::ToHexString($digest).ToLowerInvariant() }
})
$env:FLEETOPS_AUTH_TOKENS = ConvertTo-Json -InputObject $credentials -Compress
$env:FLEETOPS_CHARGING_DEMO = '1'
$env:PORT = '8080'
$go = 'C:/Users/Ayush/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.1.windows-amd64/bin/go.exe'
& $go version
$demoLogPrefix = Join-Path ([IO.Path]::GetTempPath()) ('fleetops-demo-' + [guid]::NewGuid().ToString('N'))
$serverProcess = Start-Process -FilePath $go -ArgumentList @('run', './cmd/server') -WorkingDirectory (Join-Path $PWD 'backend') -WindowStyle Hidden -RedirectStandardOutput ($demoLogPrefix + '.out') -RedirectStandardError ($demoLogPrefix + '.err') -PassThru

$ready = $false
for ($attempt = 0; $attempt -lt 120; $attempt++) {
    if ($serverProcess.HasExited) { Get-Content ($demoLogPrefix + '.err'); throw 'Backend exited' }
    try {
        $health = Invoke-RestMethod 'http://127.0.0.1:8080/healthz' -TimeoutSec 1
        if ($health.status -eq 'ok') { $ready = $true; break }
    } catch {}
    Start-Sleep -Milliseconds 500
}
if (!$ready) { throw 'Backend readiness timed out; inspect the local stderr log' }
```

This is the documented, opt-in station-ready profile: R1 is idle at S1 `(0,0)`,
with battery 100%, no job, and an available station. It satisfies the frozen
battery minimum of 30%. The ordinary simulator defaults are unchanged. The server
does not automatically load `.env` or advance simulator ticks.

Use the actual schema through this HTTP request helper. All calls below use
`POST http://127.0.0.1:8080/graphql`, `Content-Type: application/json`, and
`Authorization: Bearer <redacted>`; tokens are supplied from memory.

```powershell
function Invoke-DemoGraphQL($Role, $Query, $Variables = @{}) {
    $body = @{ query = $Query; variables = $Variables } | ConvertTo-Json -Depth 8 -Compress
    $response = Invoke-RestMethod -Uri 'http://127.0.0.1:8080/graphql' -Method Post -ContentType 'application/json' -Headers @{ Authorization = "Bearer $($demoTokens[$Role])" } -Body $body
    if ($response.errors) { throw 'GraphQL protocol error during demo' }
    return $response.data
}

$stateQuery = '{fleetState{tick poisoned robots{id x y battery status jobId} stations{id x y occupiedBy reservedBy}}}'
Invoke-DemoGraphQL viewer $stateQuery | ConvertTo-Json -Depth 8

$submitQuery = 'mutation($input:SetChargingInput!){submitProposal(input:$input){proposalId state execution auditCommitted executionAttempted error}}'
$submitted = Invoke-DemoGraphQL proposer $submitQuery @{ input = @{ robotId = 'R1'; stationId = 'S1'; expiresAt = [DateTimeOffset]::UtcNow.AddMinutes(5).ToString('o') } }
$submitted | ConvertTo-Json -Depth 8
$proposalId = $submitted.submitProposal.proposalId
if ($submitted.submitProposal.error -or !$submitted.submitProposal.auditCommitted -or $proposalId -notmatch '^P[0-9a-f]{32}$') { throw 'Submission failed' }

$approveQuery = 'mutation($id:ID!){approveProposal(id:$id){proposalId state execution auditCommitted executionAttempted error}}'
# Denied, audited, and R1 remains idle.
Invoke-DemoGraphQL proposer $approveQuery @{ id = $proposalId } | ConvertTo-Json -Depth 8
Invoke-DemoGraphQL viewer $stateQuery | ConvertTo-Json -Depth 8
# Independent operator: execution succeeds and R1 enters charging.
Invoke-DemoGraphQL operator $approveQuery @{ id = $proposalId } | ConvertTo-Json -Depth 8
Invoke-DemoGraphQL viewer $stateQuery | ConvertTo-Json -Depth 8
Invoke-DemoGraphQL viewer 'query($id:ID!){proposal(id:$id){id action robotId stationId state execution submittedBy currentRuntime createdAt expiresAt}}' @{ id = $proposalId } | ConvertTo-Json -Depth 8
# Duplicate: rejected without a second execution.
Invoke-DemoGraphQL operator $approveQuery @{ id = $proposalId } | ConvertTo-Json -Depth 8
```

Run these read-only SQL queries from the same terminal to verify committed records
independently of the HTTP response. The generated ID was checked above before
inclusion in SQL. No database password is placed in command arguments.

```powershell
@"
SELECT id,state,execution,submitted_by FROM fleet_proposals WHERE id='$proposalId';
SELECT attempt,operator_subject,accepted,reason FROM fleet_audit WHERE proposal_id='$proposalId' ORDER BY id;
SELECT count(*) AS execution_events, count(*) FILTER (WHERE accepted) AS successful_executions FROM fleet_audit WHERE proposal_id='$proposalId' AND attempt='execute';
"@ | docker compose exec -T postgres sh -c 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
```

Observed on 2026-10-16 for proposal `P4752c3a553bb0a0a79c77938c8c99dec`:

| Step | Actual result |
|---|---|
| Initial fleet query | R1 idle, battery 100, `(0,0)`; S1 available; tick 0; runtime unpoisoned |
| Submission | pending; execution none; auditCommitted true; error null |
| Proposer approval | error unauthorized; executionAttempted false; auditCommitted true; R1 still idle |
| Operator approval | approved; execution succeeded; executionAttempted true; auditCommitted true |
| Fleet query | R1 charging; S1 occupiedBy R1; tick remains 0 |
| Proposal query | approved/succeeded; submittedBy demo-proposer; currentRuntime true |
| Duplicate approval | error proposal already decided; executionAttempted false; auditCommitted true |
| PostgreSQL | approved/succeeded; five ordered audit records; exactly one execution event, accepted |

The five actual audit records were create/accepted by demo-proposer,
approve/denied for demo-proposer, approve/accepted by demo-operator,
execute/accepted by demo-operator, and approve/denied for demo-operator.
All live assertions completed with exit code 0. A duplicate response retains the
historical `execution=succeeded`; `executionAttempted=false` describes the current
attempt. A denied unauthorized response has an empty state field; use the proposal
query for its persisted status. HTTP 200 alone does not indicate command success.

Clean up after the last completed request. PostgreSQL's volume and committed
demo audit are preserved; no rows are deleted. On a later run, submit a fresh
proposal: an older runtime's proposal is deliberately not executable.

```powershell
if ($serverProcess -and !$serverProcess.HasExited) {
    $serverProcess.Kill($true)
    $serverProcess.WaitForExit()
}
foreach ($suffix in @('.out', '.err')) {
    if (Test-Path -LiteralPath ($demoLogPrefix + $suffix)) { Remove-Item -LiteralPath ($demoLogPrefix + $suffix) }
}
Remove-Item Env:FLEETOPS_DATABASE_URL, Env:FLEETOPS_AUTH_TOKENS -ErrorAction SilentlyContinue
$demoTokens.Clear()
$token = $null
$demoEnv.Clear()
docker compose down
```

No blocker was found for this charging demonstration using the documented demo
profile. It is a stationary command demonstration: there is no background ticking
or travel-to-charge command. Without the demo profile, the default idle robots
start away from the stations, and SetCharging preflight rejects their positions.
The existing Phase 2 soak failure and its acceptance criteria were not changed
or rerun for this verification-only ticket.

The smallest next feature for an AI-assisted demonstration is a proposer-only
CLI adapter that reads fleetState, explains an eligible SetCharging proposal,
and submits it through this API. A separate authenticated operator would still
approve it. This needs no new simulator command; it remains limited to robots
already eligible at a charging station. No AI adapter was implemented here.
