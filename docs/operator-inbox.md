P3-09 adds an operator approval inbox below the live warehouse dashboard.
The warehouse visualization and polling remain available to all authenticated
roles. Use Go 1.27.1 and Node 24.21.0.

Start PostgreSQL and configure the existing private database URL and hashed
credentials using the [local GraphQL walkthrough](local-graphql-demo.md). That
walkthrough generates distinct viewer, proposer, and operator subjects. For this
demonstration, keep the existing station-ready profile and disable ticking:

```powershell
docker compose up -d --wait postgres
$env:FLEETOPS_CHARGING_DEMO='1'
$env:FLEETOPS_AUTO_TICK='0'
go -C backend run ./cmd/server
```

In another terminal, start the existing frontend:

```powershell
npm ci --prefix web
npm --prefix web run dev
```

Open **http://127.0.0.1:5173** and connect with the private operator token.
The token remains in memory, clears on disconnect/refresh/authentication failure,
and is not placed in URLs, browser storage, or logs. Viewer and proposer tokens
keep fleet monitoring available and show **Operator access required** in the
inbox. The backend enforces this restriction independently of React.

Submit a proposal using the existing API and the private proposer credential.
This PowerShell example uses `$demoTokens` from the linked credential setup and
prints only the API response. Keep credentials private; do not store them in
scripts or frontend environment files.

```powershell
$body = @{
    query = 'mutation($input:SetChargingInput!){submitProposal(input:$input){proposalId state execution auditCommitted executionAttempted error}}'
    variables = @{ input = @{
        robotId = 'R1'; stationId = 'S1'
        expiresAt = [DateTimeOffset]::UtcNow.AddMinutes(5).ToString('o')
    } }
} | ConvertTo-Json -Depth 6 -Compress
Invoke-RestMethod 'http://127.0.0.1:8080/graphql' -Method Post -ContentType 'application/json' -Headers @{ Authorization = "Bearer $($demoTokens['proposer'])" } -Body $body
```

The inbox refreshes about every two seconds. It displays the robot, station,
SetCharging action, persisted state, submitter, expiration, and exactly
**No rationale recorded**: no rationale is present in the existing stored
proposal contract. Review the request and select **Approve** or **Reject**.
The Go service retains responsibility for authorization, self-approval prevention,
current-state validation, execution-time revalidation, command execution, and audit.

After a decision, the app refreshes fleet state, the pending list, and the selected
proposal's stored status. A receipt displays the actual response state, execution
outcome, auditCommitted flag, executionAttempted flag, and server error. The full
response is available under **Server response**. Confirmed, committed closed
states are removed from the pending cache immediately, and the list is then
refreshed from the server. Unconfirmed outcomes are not treated as closed.
The latest receipt stays visible
for the session; it is not a durable client audit store.

Create separate proposals while R1 is idle to demonstrate rejection before
approval. Reject one: it becomes rejected, no command executes, and R1 remains
idle. Approve another: R1 enters charging and the station reports occupancy.
While R1 remains charging with ticking disabled, another previously valid
proposal is stale; Go rejects its approval. Further submissions for that robot
may also be ineligible under existing safety validation.

For duplicate decisions, two operators can review the same pending proposal in
different tabs. If one approves while the other still has a stale view, the
second receives **proposal already decided**. Historical execution can still be
`succeeded`, while `executionAttempted=false` describes the denied repeat.
An expired proposal is denied by Go and receives its persisted expired state.
The UI's expiration-time warning is advisory; it does not replace Go validation.

Decisions are disabled while a decision/status refresh is in flight or while the
fleet/inbox is unavailable. Backend disconnection retains an explicitly stale
snapshot and pending list with decisions disabled. Read polling retries; mutations
are never retried automatically. If a decision response is lost or an audit commit
is not confirmed, the receipt warns that the outcome is unknown and calls for
status refresh/reconciliation. A failed HTTP request does not establish that the
command failed or that its transaction rolled back. A poisoned runtime continues
to prevent further mutations through existing service logic.

The only new operation is:

```graphql
query {
  pendingProposals {
    id action robotId stationId state execution
    submittedBy currentRuntime createdAt expiresAt
  }
}
```

It returns at most **50** rows with persisted state pending from the current
runtime. Ordering is creation time (PostgreSQL timestamp precision), then ID with
C collation. There is no pagination or caller-controlled limit; at 50 entries,
the UI explains that resolving proposals and refreshing reveals more. Existing
`proposal(id)`, `fleetState`, and all three mutations are preserved. The query
uses the existing service and PostgreSQL tables without migrations, simulator
mutation, lifecycle transitions, or audit writes. Read failures do not poison
the simulator. Unauthorized reads return GraphQL `FORBIDDEN`; invalid bearer
credentials still fail at the existing HTTP authentication boundary.

Pending expiration is evaluated on decisions under the existing lifecycle;
reading the inbox does not sweep or change expired records. Prior-runtime
proposals are excluded. The old runtime's requests remain non-executable under
the existing execution boundary. No auth-context query, rationale field,
simulator controls, or new command was introduced.

Verification on 2026-10-16 used the actual Go server, React/Vite app, Edge,
and Compose PostgreSQL. The latest run produced these committed outcomes:

| Case | Proposal | Persisted outcome |
|---|---|---|
| Approved | `P11f5193281ce33b035af9817893ffb8c` | approved / succeeded |
| Rejected | `P523030d9af53e22f3fefe528cdc7c3b3` | rejected / none |
| Expired | `Pebe6bdd00067c1654292d86616346f79` | expired / none |
| Stale after charging | `P820507cf4876cbc186b268755164b24c` | pending / none; approval denied |

The browser verified automatic inbox discovery, review, rejection leaving R1
idle, approval changing R1 to charging, automatic fleet/status refresh, and a
duplicate approval denied in a stale operator tab. To reproduce that race, the
browser harness delayed subsequent real inbox reads in the second tab; it did
not fabricate responses. It also verified expired and stale-request denial,
viewer/proposer access denial in both the UI and API, disabled controls after
backend disconnection, empty browser storage, session clearing, a 390 px mobile
viewport, and zero browser runtime errors. Distinct subjects were
`inbox-proposer`, `inbox-operator`, and `inbox-viewer`.

Independent SQL queries confirmed 12 committed audit events for these four
requests, including viewer/proposer denials and the duplicate denial. Exactly
one execution event existed, for the approved request; rejected, expired, and
stale requests had no execution event. Earlier demo attempts remain in the
database with their own runtime identities. One initial expiration check ran
before its deadline and correctly executed; the verification harness was fixed
to wait past expiration before asserting denial. No application rule changed.

| Verification | Exit |
|---|---|
| `npm --prefix web test` — 25 tests | 0 |
| `npm --prefix web run typecheck` | 0 |
| `npm --prefix web run build` | 0 |
| `go test ./fleet ./graphql/... ./cmd/server -skip Integration -count=1` | 0 |
| `make test-integration` — required real PostgreSQL, race detector | 0 |
| `go test -race ./fleet ./graphql/... ./cmd/server -skip Integration -count=1` | 0 |
| `go test ./... -skip TestPhase2Soak -count=1` | 0 |
| `go vet ./...` | 0 |
| Real browser workflow and independent committed-audit assertions | 0 |

Frontend checks used Node 24.21.0; Go checks used Go 1.27.1, with Linux for the
race detector. Database cases skipped in focused tests without a test URL and
ran in the required-database integration run. The known Phase 2 S3/seed99 soak
failure remains enabled with unchanged acceptance criteria; the soak suite was
not rerun. No dependencies or database migrations were added.

P3-09 files, excluding preserved work from earlier tickets:

```text
backend/fleet/inbox.go
backend/fleet/inbox_test.go
backend/graphql/inbox.go
backend/graphql/inbox_integration_test.go
backend/graphql/resolver.go
backend/graphql/schema.graphqls
backend/graphql/schema.resolvers.go
backend/graphql/generated.go
backend/graphql/handler_test.go
web/src/App.tsx
web/src/ApprovalInbox.tsx
web/src/ApprovalInbox.test.tsx
web/src/proposals.ts
web/src/proposals.test.ts
web/src/styles.css
web/README.md
docs/operator-inbox.md
```
