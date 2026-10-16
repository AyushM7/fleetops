P3-07 adds opt-in automatic simulator dynamics to the existing Go server.
The existing GraphQL queries, proposal lifecycle, database schema, and simulator
behavior are preserved.

Use the existing private database and hashed bearer-token setup in the
[local GraphQL walkthrough](local-graphql-demo.md). In that walkthrough's startup
block, replace the charging-demo and automatic-tick assignments with these lines
before `Start-Process`:

```powershell
$env:FLEETOPS_CHARGING_DEMO = '0'
$env:FLEETOPS_AUTO_TICK = '1'
$env:FLEETOPS_TICK_INTERVAL = '500ms'
```

`FLEETOPS_AUTO_TICK` defaults to false. Enabling it without an interval uses
500 ms. `FLEETOPS_TICK_INTERVAL` accepts positive Go durations such as `100ms`
or `1s`; invalid booleans or durations fail startup with a redacted configuration
error. These are process environment variables; the backend does not load `.env`.
The live profile uses the existing default simulator configuration and seed 42.

Poll in the same PowerShell terminal after startup. `$demoTokens` is created by
the linked walkthrough and is never printed. The viewer token cannot mutate the
fleet. Requests use only existing schema fields:

```powershell
$body = @{ query = '{fleetState{tick poisoned robots{id x y battery status jobId waitTicks blocked}}}' } | ConvertTo-Json -Compress
1..10 | ForEach-Object {
    $response = Invoke-RestMethod 'http://127.0.0.1:8080/graphql' -Method Post -ContentType 'application/json' -Headers @{ Authorization = "Bearer $($demoTokens['viewer'])" } -Body $body
    if ($response.errors) { throw 'GraphQL query failed' }
    $response.data.fleetState | ConvertTo-Json -Depth 6
    Start-Sleep -Seconds 1
}
```

Watch `tick`, robot coordinates, status, and job assignments change. Job records
and completed-job counts are not exposed by the current schema. Completion is
verified against the owned service snapshot in the runtime test, rather than
inferred solely from a disappearing job ID.

Stop the foreground backend with Ctrl+C. For the background `Start-Process`
walkthrough, use its cleanup block. There is no GraphQL pause, Tick, fault-injection,
or simulator-control mutation. Configuration changes take effect at startup;
to return to the charging demo, restart with:

```powershell
$env:FLEETOPS_AUTO_TICK = '0'
$env:FLEETOPS_CHARGING_DEMO = '1'
```

The ticker calls only `fleet.Service.Tick`, which shares the existing service
mutex with submission, approval, execution, and snapshot reads. Validation,
execution-time revalidation, SetCharging, and the database commit remain within
the existing approval operation. A proposal made ineligible by an earlier tick
is denied by existing validation. A poisoned runtime refuses Tick and the
automatic loop stops; errors are logged without dependency details.

Shutdown cancels and joins the ticker before closing the database pool. An
in-flight operation may finish while shutdown waits; no tick runs after the
stop function returns. Pulses arriving after cancellation are not processed.
The loop stops on its first Tick error. Slow operations can cause `time.Ticker`
to drop pulses; there are no parallel ticks or wall-clock catch-up mutations.

Ticker timestamps do not enter simulator state. The same configuration, seed,
ordered ticks, commands, and clock inputs produce the same simulator states.
Wall-clock scheduling does not guarantee the same command interleaving or tick
count in separate live runs. No tick journal or runtime restoration was added;
existing prior-runtime proposal rejection remains in force.

Verification uses Go 1.27.1. The runtime tests cover configuration, disabled
operation, cancellation, in-flight shutdown, loop failure, fresh authenticated
HTTP snapshots, and job completion (13 completed jobs at tick 100 with the
default configuration and seed 42). PostgreSQL integration tests cover Tick
serialization with approval and replay of the same tick/command sequence. Existing
integration tests retain stale-proposal rejection after battery-draining ticks.
No simulator or frozen safety files are changed.

Local verification on 2026-10-16 used the actual server and Compose PostgreSQL.
At a configured 100 ms interval, authenticated GraphQL polling observed tick 28
advance to 141. R1 moved from `(4,3)` at tick 32 to `(19,3)` at tick 54 and
`(13,9)` at tick 76, with changing job assignments. Stopping the backend made
the health endpoint unavailable. The stationary charging walkthrough was then
rerun with automatic ticking disabled: tick stayed 0, R1 entered charging after
operator approval, duplicate approval was denied, and PostgreSQL retained one
successful execution event.

| Verification command or demonstration | Exit |
|---|---|
| `go test ./cmd/server ./fleet ./graphql -count=1 -v` | 0 |
| `make test-integration` with required Compose PostgreSQL, including `-race` | 0 |
| `go test -race ./cmd/server ./fleet ./graphql -skip Integration -count=1 -v` | 0 |
| `go test ./... -skip TestPhase2Soak -count=1` | 0 |
| `go vet ./...` | 0 |
| Live automatic-tick HTTP demonstration | 0 |
| Disabled-tick charging HTTP demonstration and PostgreSQL audit check | 0 |

Native focused tests initially encountered sandbox loopback socket restrictions;
the rerun with local-network access passed. Database integration cases skip in
the native focused run without a test URL; they ran against real PostgreSQL in
the required-database integration run. Linux Go 1.27.1 was used for the race
detector. The full soak suite was not rerun, and its known S3/seed99 failure
remains enabled with the same acceptance criteria.
