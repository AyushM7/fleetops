# Live warehouse dashboard

## P4-04 incidents and job reassignment

The dashboard uses the existing P4-03 API. No backend schema, simulator, safety,
database or lifecycle changes are needed. The fleet query now includes current
Go-detected `activeIncidents` and runtime `jobs` alongside positions and layout.
Incidents show robot, type, severity, battery, status, coordinates, job, wait ticks
and blocked flag. They are observations from a snapshot, not incident history or
permission to execute a command. Missing or contradictory incident data fails
closed into the existing stale/unavailable state.

The approval inbox supports both `set_charging` and `reassign_unstarted_job`.
Reassignment cards show the job, source, recipient, pending status, submitter and
expiration. Authorized operators use the existing Approve/Reject mutations;
viewer and proposer inbox access remains denied by Go while monitoring works.
Prior-runtime, expired, busy and stale/unavailable decisions are disabled. Go
still validates eligibility, expiry, independent approval and execution.

After a decision, the app refreshes fleet, inbox and stored proposal status. The
last decision receipt retains its action details after it leaves the pending
list, with execution/audit flags and sanitized errors. Transfer success is
explicitly distinguished from delivery. The Job progress table and map job labels
show current ownership and recipient movement; only Go's `done` job status is
labeled Delivered. Rejection, expiry, stale preflight, duplicate decisions,
failed execution and unconfirmed persistence never imply delivery. Lost responses
are not retried automatically. Failed status refreshes visibly mark retained
status as stale.

There are no proposal submission, tick, execution or fault-injection controls in
React. Tokens retain the existing tab-memory-only handling. No provider keys,
model calls or dependencies are added. The inbox remains bounded to 50 current
pending proposals and the last decision receipt in this session; it is not a
persisted activity-history view.

Run the existing frontend tests, checks and build, then the new isolated real
backend integration from the repository root using Node 24.21.0 and PowerShell 7:

```powershell
npm --prefix web test
npm --prefix web run typecheck
npm --prefix web run build
pwsh -NoProfile -ExecutionPolicy Bypass -File web/scripts/verify-integration.ps1
```

The integration harness requires Docker, PostgreSQL 18.6 and the existing Go
toolchain. It bundles the actual frontend client and React views, uses a temporary
backend copy and the existing `fleetops_test` fixture, and runs both charging and
reassignment through real authenticated Go GraphQL and PostgreSQL. Trusted test
process messages advance the simulation; no tick/injection HTTP endpoint is
added. It checks independent operator permissions, rejection, stale/duplicate/
expired decisions, live pickup/delivery and the displayed states. Its private
schemas, container and temporary backend copy are removed afterward. Run the
harness in a separate PowerShell process so its test environment does not persist.

See [P4-04 implementation report](P4-04-REPORT.md) for results and the changed-file
inventory. The existing local connection instructions follow.

Use Node **24.21.0** and Go **1.27.1**. The dashboard is the existing React,
TypeScript, and Vite app with no additional dependencies.

Start PostgreSQL and configure the backend's private database URL and hashed
bearer-token credentials using [the local GraphQL walkthrough](../docs/local-graphql-demo.md).
Use viewer credentials for monitoring and distinct operator credentials for
the approval inbox. See [operator inbox demonstration](../docs/operator-inbox.md).
Before starting the backend,
set these environment variables in its terminal:

```powershell
docker compose up -d --wait postgres
$env:FLEETOPS_CHARGING_DEMO='0'
$env:FLEETOPS_AUTO_TICK='1'
$env:FLEETOPS_TICK_INTERVAL='500ms'
Set-Location backend
go run ./cmd/server
```

If following the linked background-start walkthrough, replace its charging-demo
and automatic-tick assignments before `Start-Process` with those above. Go does
not load `.env` automatically. Preserve the private `FLEETOPS_DATABASE_URL` and
`FLEETOPS_AUTH_TOKENS` configuration; never put bearer tokens into a file, URL,
`VITE_*` variable, or source code. The normal charging demo still works with
`FLEETOPS_AUTO_TICK=0` and `FLEETOPS_CHARGING_DEMO=1`.

From the repository root in another terminal:

```powershell
npm ci --prefix web
npm --prefix web run dev
```

Open **http://127.0.0.1:5173**, paste your viewer or operator bearer token, and select
**Connect to fleet**. If the GraphQL walkthrough generated `$demoTokens`, use
its private viewer token. The password field is cleared after connection. The
browser retains the token only in React memory for the current tab: refresh,
Disconnect, or an authentication failure clears the session. Neither localStorage
nor sessionStorage is used. Disconnect also clears the displayed snapshot.

The dev server listens only on loopback and proxies `/graphql` to
`http://127.0.0.1:8080`. The fleet poller reads the same-origin `/graphql`
endpoint. The operator inbox polls `pendingProposals` and sends the existing
approval/rejection mutations only on an explicit decision. There are no direct
simulator controls. Use HTTPS at a trusted
reverse proxy for a deployed application; this ticket provides the local demo.

The map uses the service's actual `layout { width height cells { x y kind } }`
inside `fleetState`, with current robot and station positions from the same read.
The service builds immutable layout data from the configuration used to construct
its owned simulator, using the existing cell classification. The browser has
no default warehouse dimensions, wall locations, aisles, station locations, or
sample robots. Unit-test protocol fixtures are confined to test files.

Polls run about once per second after the previous response completes. Slow
requests time out after eight seconds. The screen shows the simulation tick,
robot IDs, positions, status, battery percentage, job assignment, and blocked
wait time. Blocked robots have amber outlines and a text warning. Faulted and
offline robots have red markers plus explicit status and explanation text.
Offline markers also have dashed outlines. The map scrolls horizontally on
narrow screens rather than removing warehouse cells.

For invalid/expired credentials, the app clears the session and shows an
**Authentication failed** message. For backend disconnection or an incompatible
response, it shows **Backend unavailable. Retrying…**, retaining the last received
snapshot with an explicit stale-data warning. It retries without overlapping
requests and resumes on successful responses. A poisoned runtime is reported as
requiring reconciliation. Internal dependency errors and credentials are not
displayed; the inbox shows the API's existing sanitized decision responses.

To observe failures, try an invalid token, then connect with the valid viewer
token and stop the Go backend. Refresh the page to verify that it requires a
new token. Stopping Vite and Go with Ctrl+C and running `docker compose down`
preserves the PostgreSQL volume. Polling cannot advance or pause the simulator.

Checks from the repository root:

```powershell
npm --prefix web test
npm --prefix web run typecheck
npm --prefix web run build
```

Production build output is in ignored `web/dist`. A static deployment must proxy
`/graphql` to Go; `vite preview` alone does not provide that API proxy. The
backend's existing proposal/approval mutations are preserved. The API adds the
read-only layout field and the operator-only pending-proposals query.
No simulator interfaces, safety rules, or behavior change.

P3-08 verification used Node 24.21.0, Go 1.27.1, real Compose PostgreSQL and an
Edge browser running the actual Vite app. The browser observed ticks 21 → 31,
changing robot coordinates, R1 battery 80% → 70%, and R4 Moving → Idle. The
rendered layout contained exactly 240 cells, 30 aisle cells, two station cells,
and no walls, matching the backend's default configuration. Invalid authentication,
refresh clearing the token, backend disconnection/stale data, and a 390 px mobile
viewport passed. No browser runtime errors or GraphQL mutations occurred.

The 10 frontend unit tests, TypeScript check, production build, affected Go tests,
real PostgreSQL integration tests, Go race tests, non-soak regressions, and vet
all passed. The known Phase 2 soak failure was not changed or rerun. Existing
approved P3-07 working-tree changes are preserved. No new package dependencies
or lockfile changes are needed.
