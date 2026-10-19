# PUB-02 — Public security and operational hardening

## Assessment and scope

**PASS for PUB-02 implementation and scoped local acceptance.** Critical production-path, isolation, security, archival, Linux race and regression checks passed. Deployment remains a PUB-03 task; the limitations below are not production deployment approval.

Branch: `phase8-pub02-public-hardening`. Starting HEAD:
`56964841f057021bddbc13a9a2b8bf859011e033` (merged PUB-01).
No commit, push, merge, branch change, deployment, paid inference or dependency
upgrade was performed. `docs/CODEX-CLAUDE-HANDOFF.md` remains excluded and unedited.

The owner approved production extraction after inspection established that PUB-01
was a build-tagged test harness, while the normal server still owned one shared
fleet service. This implementation makes the normal server serve isolated visitor
runtimes. The historical Phase 4/8 harnesses remain unchanged regression references.

Initial gaps: no production visitor entry point, HTTPS cookie/public origin/proxy
configuration, creation/request/connection limits, readiness, evidence quotas or
admission backpressure for retained schemas. Root bootstrap could replace invalid
credentials with a new session. Retiring resources were not included in capacity.

## Implemented boundaries

- `backend/visitor`: production session manager, fixed-preset factory, controller,
  gateway, resource policy, readiness, shutdown and verified database archiver.
  Public construction accepts configuration only; no proposer, simulator, clock,
  authorization, archive or service injection is exposed through configuration,
  HTTP, GraphQL or agent tools. Private fault seams are used only by package tests.
- `backend/sim/visitor_presets.go` and `backend/fleet/visitor.go`: four server-owned
  initial states. The existing PUB-01 fixture and production presets are compared
  directly. Each call constructs fresh simulator state, runtime identity, layout,
  canonical proposal records and lifecycle. No browser-supplied simulator config.
- `backend/cmd/server`: normal application uses `visitor.New`; no shared-service
  fallback for visitor requests. Shared automatic ticking is rejected. The real
  listener bounds connections; existing HTTP timeouts/header limits remain.
  Cleanup errors propagate to process exit, with sanitized structured events.
- Scripted proposal creation uses a separate, random proposer credential through
  authenticated GraphQL. Browser capabilities forward only their visitor's random
  operator credential. Go observation/preflight checks, independent approval,
  execution, poisoning and authorization are unchanged. GraphQL schema, safety
  rules, agent tool signatures and frozen fixtures were not modified.
- Proposal source is explicitly `go-scripted`, or `none` for no-action presets.
  No Mastra adapter, recording or live model is invoked. Existing frontend source
  classifications and unknown-source fallback remain authoritative. The UI now
  calls this a visitor demo and gives accurate expired-session renewal guidance.

Cookies are server-generated 256-bit opaque capabilities, host-only, HttpOnly,
SameSite=Strict, fixed 20-minute lifetime. Production adds Secure and a `__Host-`
name. Credentials never appear in HTML, status JSON, URLs, frontend storage or
application logs. Set-Cookie is their intended browser delivery channel. Duplicate,
malformed, unknown, expired or revoked capabilities are rejected; invalid root
credentials do not create a replacement visitor. Every protected route checks
session validity; refresh never extends lifetime.

Every request validates exact Host and configured Origin, rejects query parameters,
absolute-form request targets, hostile Fetch-Site values and ambiguous boundary
headers. Protected requests require JSON POST, the same-origin CSRF marker and no
browser Authorization header. Controls require exactly `{}` with a bounded read.
GraphQL retains its existing 64 KiB body, token, complexity, depth, selection,
single-mutation and ten-second context limits.

Forwarding is accepted only from the actual connected peer in configured CIDRs.
The supported proxy contract is one `X-Forwarded-Proto: https`; optional
`X-Forwarded-Host` must equal Host and `X-Forwarded-For` must be a single IP.
Forwarded chains, repeated values, standard `Forwarded` and `X-Real-IP` are rejected.
Forwarded client IPs never select an identity or bypass limits; rate accounting
uses the connected peer. The proxy must overwrite forwarding headers and preserve
the configured Host. Shared proxy/NAT traffic shares the peer budget.

Browser responses use no-store, no-referrer, nosniff, frame denial, restrictive
CSP and Permissions-Policy; production adds HSTS. Only the built index and bounded,
preloaded static asset types are served. No directory listings, symlinks, private
files, arbitrary simulator mutations or source-map routes are exposed.

The legacy `agent/scripts/verify-integration.ps1` launcher previously built the
shared `cmd/server` and therefore needed a compatibility correction. It now builds
`cmd/agentfixture` with the explicit `fleetops_test` build tag and opt-in flag. This
fixture binds only loopback, owns its fixed station-ready test state and retains
the same authenticated GraphQL/safety gates. Ordinary production builds exclude
it; there is no public legacy mode or simulator-construction HTTP endpoint. Its
real-backend TypeScript tests and Go/Node contracts are verified separately.

## Fixed operational limits

| Resource | Limit / failure behavior |
| --- | --- |
| Visitor ownership | Eight total active, initializing and draining visitors; failed/uncertain cleanup stops new admission. |
| Lifetime / workers | Twenty minutes; one 200 ms pulse worker per published visitor; expiry revokes before cleanup. |
| Database pools | Two connections per visitor plus two administrative connections: at most 18 application connections. |
| Connections / requests | 64 connections in the real listener; 32 active application requests, immediate overload response rather than an application queue. Excess connections are closed before HTTP handling. |
| Request rate | Global burst 128 / 64 requests per second; peer burst 96 / 64 per second; at most 256 peer entries, stale entries reclaimed after five minutes. |
| Creation rate | Global burst eight; one replenished creation per minute. |
| Visitor retained state | 2,048 protected operations excluding status, eight replacements, 1,200 ticks per generation; fixed simulator job/config bounds. |
| Evidence admission | 512 MiB / 10,000 entries, with a five GiB free-space reserve plus 32 MiB headroom. No evidence is deleted to reclaim quota. |
| Final archive | At most 16 MiB of application row JSON before aggregation; excess or uncertainty retains the schema. |
| HTTP deadlines | Header 5 s, read/write 15 s, idle 60 s, maximum headers 8,192 bytes. |
| Cleanup | Application shutdown caller deadline 30 s; in-flight bounded cleanup may finish if the caller times out. Initializers and database operations also have deadlines. |

These are admission/backpressure policies, not throughput benchmarks or a DDoS
resistance claim. In-flight final archives can consume headroom beyond the evidence
admission threshold; concurrency and per-archive size bound that overshoot. If
storage is insufficient, archival fails closed and the schema remains. Persistent
history is never automatically removed. Database growth is bounded by visitor and
operation budgets; PostgreSQL storage/connection capacity must also be provisioned
and monitored independently.

`/healthz` reports process liveness without a database dependency. `/readyz`
requires available capacity, healthy storage, a successful bounded PostgreSQL ping,
no unresolved retention failure and no shutdown. Both require the configured Host;
a plaintext backend health probe in production must arrive through the configured
trusted proxy contract. DB readiness recovers after actual connection loss; only a
read-only cleanup ping may retry once. Mutations are never automatically retried.

Signal cancellation revokes admission and sessions, drains serialized operations,
captures state, closes pools and archives. Shutdown uncertainty is reported and
never authorizes a forced schema drop. A killed process can leave schemas; startup
refuses existing owned `fleetops_visitor_` schemas for reconciliation. Use one
application process and a dedicated database/owner role; horizontal scaling,
resuming old simulators and automatic orphan reconciliation are outside this ticket.

## Evidence preservation and ownership

Each visitor has a cryptographically random private schema and evidence directory.
The archiver confirms its namespace name, OID and current-role ownership. A single
MVCC statement captures every application table's full records and migrations,
including old generations, denied attempts and agent-run records. Unknown/missing
tables, malformed inventory, unavailable reads or excess size prevent completeness
confirmation. The table inventory must exactly match the known migration tables.

Final files use exclusive creation, mode 0600, file sync and checked close, exact
readback and structural verification, then visitor and parent directory sync.
Only after verified evidence and pool shutdown does a second ownership confirmation
permit dropping that disposable visitor schema. Controller-capture failure alone
can still permit deletion if the separate database archive succeeds completely;
failed, truncated, uncertain or unconfirmed archival always retains the schema.

The archive is evidence, not a resumable simulator checkpoint or an independently
proven backup restoration procedure. File/directory fsync and readback establish
the application's durability checks; power-loss, underlying storage failure and
external backup restoration were not simulated. Use durable Linux storage that
honors these operations. Windows directory-sync uncertainty retains schemas rather
than weakening this rule. Docker Desktop bind mounts expose synthetic permissions;
the real production-mode integration uses a private native Linux directory.

For retained schemas: stop admission, preserve PostgreSQL storage and evidence,
identify namespace/OID/owner from administrative records, establish complete durable
archival and drained pools, and reconcile under owner control. This ticket supplies
no automatic deletion, pruning or broad cleanup endpoint. Restart does not repair
or reinterpret an unknown execution outcome.

## Configuration and deployment preparation

| Variable | Required value |
| --- | --- |
| `FLEETOPS_MODE` | Explicit `local` or `production`; no permissive default. |
| `FLEETOPS_PUBLIC_ORIGIN` | Exact scheme/host/optional port, without path/query/credentials. Local requires an HTTP loopback IP; production requires HTTPS and rejects placeholder hosts. |
| `FLEETOPS_DATABASE_URL` | Private connection for a dedicated DB/role with schema creation privileges; supply verified database TLS appropriate to the deployment. Never build credentials into an image. |
| `FLEETOPS_ASSETS_DIR` | Built `web/dist`, separate from evidence. |
| `FLEETOPS_EVIDENCE_DIR` | Persistent private writable directory, outside assets; production rejects group/world permissions and symlinks within evidence. |
| `FLEETOPS_TRUSTED_PROXIES` | Optional comma-separated actual peer CIDRs for the Application API; required operationally when the normal HTTP executable is behind a TLS proxy. Local mode rejects proxies. `/0` is invalid. |
| `PORT` | Existing integer 1–65535, default 8080. |

`FLEETOPS_AUTH_TOKENS` and `FLEETOPS_CHARGING_DEMO` no longer configure the public
visitor path. Identities and fixed presets are owned per visitor. Enabled
`FLEETOPS_AUTO_TICK` is rejected; approved controls own each visitor's progression.
The existing pure helpers remain covered for historical regression purposes.

The normal executable listens HTTP behind a TLS terminator; it does not load TLS
certificates. `visitor.Application` also supports an HTTPS listener, exercised by
the production TLS integration. Do not expose the plaintext backend publicly.
Configure/firewall the proxy peer boundary explicitly and use narrow CIDRs.

`Dockerfile` builds the existing pinned Node/Go projects and uses a dependency-free
scratch runtime with CA roots and uid/gid 65532. `.dockerignore` limits build context
to backend/web inputs and excludes private env files, handoff, dependencies, build
outputs and tests. `deploy/pub02/compose.example.yml` is preparation only: private
loopback publication, read-only root, no capabilities, resource limits, tmpfs and a
private durable evidence bind. Prepare its ownership for uid/gid 65532 and mode
0700 on a Linux filesystem. The placeholder environment file intentionally cannot
start the service as-is. Existing PostgreSQL Compose data/resources are unchanged.

PUB-03 must validate the final image and target host, actual TLS/proxy/firewall
routing and health probes, DB privileges/TLS/backups, persistent evidence ownership,
underlying free space and crash/reconciliation runbook before any deployment.
No hosting provider was chosen or public service deployed.

## Verification evidence

Run identifier: `0f3893f7787b48879a0cbd43d9a27c74`.
Ignored local evidence/log root:
`evals/dist/pub02/0f3893f7787b48879a0cbd43d9a27c74`.
Every guarded invocation has `<name>.result.json`, `.stdout.log` and `.stderr.log`:
exact argv, working directory, timestamps, exit status, reserve minimum and output.
The guard samples C: every second, stops its own command/runner below five GiB,
and never prunes or touches unrelated resources. Heavy suites run sequentially.

Docker was initially healthy, became stopped between sessions, and was restored by
normally starting the installed Docker Desktop executable. No reset, prune or
historical cleanup was used. One labeled PostgreSQL container/volume and one labeled
Go runner were created, using existing cached images and a read-only source/module
mount. The preserved `l3harris_windows` container was not changed.

Recorded failures are retained, not overwritten:

- Initial sandboxed cmd/server tests could not open loopback sockets; the first
  startup test also exposed an invalid-tick configuration validation regression.
- `native-unit-initial` failed on Windows controller directory sync. Controller
  captures retain file sync; destructive cleanup still requires the full separate
  database archive's directory durability verification. `native-unit-corrected`
  passed; database integrations were explicitly skipped without configured DB.
- `linux-production-initial` failed on one stale idle PostgreSQL connection during
  read-only capture and on a negative test sending a GET body to a missing route.
  Read-only probe retry and correctly formed negative requests fixed those cases.
  `linux-production-corrected` passed with the real database, HTTPS and shutdown.
- `linux-final-race-worktree` failed on the two pre-existing CRLF golden-file byte
  comparisons and on a new HTTPS test client cloning its cookie header while its
  cookie jar added another copy. The gateway correctly denied ambiguous cookies.
  The test now sends credentials once. No data race was reported.

For full simulator verification, `prepare-canonical.sh` copies source into the same
runner, checks every non-golden source byte against the worktree at copy time, verifies that
CRLF normalization exactly equals each golden's existing Git blob, and writes those
Git blobs only into the disposable copy. Repository fixtures are never modified or
regenerated. Hash manifests and both original/Git golden hashes are preserved.
`linux-final-race-canonical` passed all packages with those exact Git expectations.
After the bounded polling allowance changed, the affected visitor/server race tests
were rerun directly against the worktree; unrelated prior results are not presented
as additional reruns. Subsequent shutdown error-propagation changes are checked by
the final server race/vet/build/smoke invocation.

Final commands, counts, skipped tests and cleanup evidence follow below.

### Final results and exact commands

The guarded JSON records contain the exact executable, complete argv and cwd.
The meaningful nested commands are listed here; Go commands run from `backend`
unless the verified-copy working directory is explicitly stated.

| Evidence name | Actual command / result |
| --- | --- |
| `linux-final-race-canonical` | In `/var/tmp/pub02-backend`: `go test -race -p 1 -tags fleetops_test ./... -count=1 -v -timeout=8m` — PASS, 14 packages; 397 top-level passing tests and 16 explicit opt-in skips. |
| `linux-final-production-race` | Direct worktree: `go test -race -p 1 -tags fleetops_test ./visitor ./cmd/server -count=1 -v -timeout=5m` — PASS, 46 top-level tests, no skips; includes final polling/resource policy. |
| `final-server-vet-build-smoke` | `go test -race -p 1 -tags fleetops_test ./cmd/server -count=1 -v -timeout=5m`; `go vet -p 1 ./...`; `CGO_ENABLED=0 go build -p 1 -trimpath -o /var/tmp/pub02-server ./cmd/server`; then `final-server.sh` — all PASS. Includes cleanup-error propagation. |
| `web-tests` | `npm test -- --maxWorkers=2 --testTimeout=30000` — 143/143 PASS, 13 files. |
| `web-typecheck`, `web-build-final` | `npm run typecheck`; `npm run build` — PASS. |
| `agent-tests`, `agent-typecheck`, `agent-build` | `npm test -- --maxWorkers=2 --testTimeout=30000` — 279/279 PASS, 14 files; `npm run typecheck`; `npm run build` — PASS. |
| `evals-tests`, `evals-typecheck` | `npm test -- --maxWorkers=2 --testTimeout=30000` — 281/281 PASS, 11 files; `npm run typecheck` — PASS. |
| `fixture-unit`, `fixture-build` | Native Go: `go test -tags fleetops_test ./cmd/agentfixture -count=1 -v`; `go build -tags fleetops_test -o <run>/agentfixture.exe ./cmd/agentfixture` — PASS. |
| `agent-backend-integration` | Against the opted-in loopback fixture: `npm run test:integration -- --maxWorkers=1 --testTimeout=30000` — 6/6 PASS. |
| `native-agent-contracts` | `go test -p 1 -tags fleetops_test ./graphql ./fleet -run '^Test(AgentIncidentHTTPClient\|IntegrationAgentRunMastraHTTP\|IntegrationGraphQLReassignmentRecovery)$' -count=1 -v -timeout=3m` — initially FAIL: two fixture imports could not resolve emitted JavaScript; the Mastra HTTP provenance contract passed. |
| `agent-fixture-modules` | `node node_modules/typescript/bin/tsc -p tsconfig.fixtures.json` — PASS. Test fixtures are emitted separately; source fixture values and production build exclusions are unchanged. |
| `native-agent-contracts-corrected` | `go test -p 1 -tags fleetops_test ./graphql ./fleet -run '^Test(AgentIncidentHTTPClient\|IntegrationGraphQLReassignmentRecovery)$' -count=1 -v -timeout=3m` — both previously failing contracts PASS, including approval/rejection/expiry/stale cases. The already-passing Mastra HTTP contract was not repeated. |
| `preparation-validation` | `python <run>/validate-preparation.py` — PASS: YAML, frontend/evidence inputs, unchanged scanner job, restricted Compose and image recipe. |
| `compose-validation` | `docker compose -f deploy/pub02/compose.example.yml config --quiet` with placeholder-only environment — PASS; no service creation. |
| `production-boundary.stdout.log` | Ordinary `go list ./...` excludes the test fixture; `go tool nm` confirms the production binary contains no test-only fleet/simulator constructors — PASS. |
| `pinned-gitleaks` | Official and pinned SHA-256 verified for Gitleaks 8.30.1; `gitleaks git --redact --exit-code 1 /repo` — 35 commits, no leaks. Its subsequent overbroad directory pass is described below. |
| `intended-source-gitleaks` | `gitleaks dir --redact --exit-code 1 <exact-intended-source-copy>` — PASS, no leaks, unchanged scanner rules. |

Production binary SHA-256:
`88f15cd6e5390c70ac5a4e36ca5c308f65706649fcf69c94bf5b09e68e9b2b70`.
The real listener smoke test opened two independent visitors, verified Go-scripted
pending decisions, delivered SIGTERM, checked zero exit status and two final
archives, and checked that cookie capabilities were absent from logs, HTML and
status JSON. The full script and exact output are retained. Final frontend assets
were subsequently rebuilt and passed typecheck/tests; no production visitor/server Go logic changed
following the final production binary verification.

The legacy test launcher now emits its fixture modules before its cross-language
checks. Two `.mjs` checks import emitted fixture JavaScript instead of relying on
Node to remap TypeScript `.js` imports. This fixes test execution without changing
frozen fixture data or adding dependencies. Integration CI now builds the frontend,
creates private evidence directories, and supplies the paths needed by the normal
application tests. The Gitleaks job is structurally identical to HEAD. Remote
GitHub checks were not run because no commit or push was authorized.

The first directory scan mistakenly used Linux Git to identify changed files in
a Windows CRLF checkout, including unrelated unchanged files. It reported one
false positive spanning two empty API-key assignments in `agent/.env.example`.
Neither assignment contains a credential; no suspected value was printed. That
file is unchanged. The corrected manifest comes from Windows Git and includes
exactly the PUB-02 changes, excluding the pre-existing handoff. Full-history
scanning was clean independently. No suppression, ignore file, scanner rule
change or history rewrite was used. The failed scan and redacted metadata remain.

### Isolation, failure and archival evidence

`TestPUB02TwoVisitorIntegration` verifies distinct session/runtime/observation/
proposal identities and private schema ownership; no foreign proposal, agent
provenance or audit access; independent approval and Step; isolated scenario
selection/restart; stale-generation denial; expiry revocation and real schema
cleanup without altering the second visitor. Concurrent restarts and a poisoned
visitor's database failure are exercised separately.

`TestPUB02FourPresetsIntegration` verifies routine charging, healthy operations,
blocked-no-candidate and blocked reassignment with their original ticks, proposal
sources and outcomes. Pending proposals block progression until an independent
human decision; charging and reassignment still execute through existing Go gates.
`TestVisitorPresetsMatchPUB01Integration` checks exact preset state compatibility.

Actual PostgreSQL archival tests cover unavailable audit reads, failed writes,
truncated readback, uncertain commit acknowledgement and ownership mismatch.
They assert retained committed proposal evidence and closed pools, then reconcile
only their positively identified fixtures through verified production archival.
A separate test proves failed controller capture can still allow verified complete
DB archival and safe owned-schema removal. No test weakens retention to pass.

The production HTTPS integration verifies Secure/HttpOnly/Strict host cookies,
Go provenance, pending approval, oversized GraphQL rejection, absence of an
arbitrary simulator mutation, bounded shutdown, immediate capability revocation
and a final archive with the pending proposal/audit. The real TCP tunnel outage
check drops pgx connections without stopping unrelated PostgreSQL, verifies live
versus ready behavior and failed admission, then readiness and admission recovery.
Host/origin/proxy ambiguity, all protected-route expirations, body bounds,
rate/capacity recovery, draining capacity, operation/tick quotas, storage pressure,
capability issuance failure and request/runtime panic containment also pass.

### Explicit skips and limits

The full race command reported these 16 opt-in skips. They are not counted as
passes. Two cross-language skips were subsequently executed successfully in the
separate native Go/Node checks noted above.

- `TestAgent07PairedEvaluation`: agent07_integration_test.go:332: opt in to isolated agent07 evaluation
- `TestIntegrationAgentRunMastraHTTP`: agent_http_integration_test.go:25: opt in to the built-agent HTTP contract check
- `TestDemo08ExportObservations`: demo08_evidence_test.go:24: set FLEETOPS_DEMO08_EXPORT to export read-only demo observations
- `TestAgentIncidentHTTPClient`: incidents_test.go:150: run agent/scripts/verify-integration.ps1 for the Node contract check
- `TestStabilizationLegacyCompatibility`: autocharge_test.go:51: requires -stabilization-legacy reference
- `TestStabilizationS1Diagnostics`: autocharge_test.go:109: requires -stabilization-diag
- `TestIdleBlockerS1Diagnostics`: clearing_test.go:16: requires -idle-blocker-diag
- `TestIdleBlockerSeed42RelocationDiagnostics`: clearing_test.go:26: requires -idle-blocker-diag
- `TestReliabilityDiagnosis`: diagnosis_test.go:505: requires -diag
- `TestReliabilityDiagnosisClassifier`: diagnosis_test.go:537: requires -diag
- `TestReliabilityDiagnosisProbes`: diagnosis_test.go:568: requires -diag
- `TestReliabilityDiagnosisAmendmentGap`: diagnosis_test.go:620: requires -diag
- `TestFinalAcceptanceResolutionDiagnostics`: diagnosis_test.go:652: requires -diag
- `TestFinalSeed99PreventiveClearanceProbe`: diagnosis_test.go:797: requires -diag
- `TestStabilizationReserveSweep`: energy_test.go:27: requires -stabilization-sweep
- `TestApprovedTuningDiagnostics`: soak_test.go:22: explicit diagnostic parameter sweep

The remaining optional agent evaluation/export and simulator diagnostic/reference
runs were not enabled. No paid live-model evaluation was run. The old full
`agent/scripts/verify-integration.ps1` wrapper was not invoked wholesale, because
it would create another PostgreSQL/build environment; its repaired fixture build,
six real-backend tests and the relevant Go/Node contracts were exercised using
this run's existing isolated database instead.

A Docker image build, actual reverse proxy/hosting deployment, full browser
interaction recording, destructive crash/power-loss simulation and backup restore
into a new database were not executed. Image recipe/Compose validation and a real
production binary smoke test do not substitute for those PUB-03 checks. Native
Windows directory durability and automatic orphan recovery are not claimed.
Cross-site Fetch-Site values remain rejected under the preserved strict policy;
entry via a direct navigation is supported, while external-link navigation policy
must be considered before public launch. No change to that security policy was
silently introduced here.

### Resource audit and finalization

All guarded commands stayed above five GiB free on C:. Late in verification,
capacity approached the reserve. Only the newly added Go build cache inside the
labeled runner and this run's disposable native fixture executable were removed.
Cache ownership was confirmed by container labels/mounts and added-layer paths;
the native executable's path, size and hash are recorded before removal. No
existing images, volumes, user caches, repositories or historical evidence were
pruned. No guard interruption or reserve violation occurred.

Both owned verification databases have zero `fleetops_visitor_` schemas after
successful cleanup. Their custom-format `pg_dump` files have readable inventories
and matching container/host SHA-256 values (`database-export.jsonl`). The original
PostgreSQL volume is retained as well. Private Linux production evidence was
exported byte-for-byte with matching SHA-256 manifests; source evidence remains
in the runner until any later owner-approved removal.

Final container/volume labels, stopped/running state, preserved-container state,
free bytes and the final Git state are recorded in `cleanup.json` and the final
Git audit under the same evidence root. Only this verification run's containers
are stopped during finalization. Container/volume evidence is retained; no
unrelated container or schema is deleted. The pre-existing `l3harris_windows`
container remains preserved.

## Complete changed-file list

- `.dockerignore`
- `.github/workflows/ci.yml`
- `Dockerfile`
- `Makefile`
- `README.md`
- `agent/scripts/check-incident-fixture.mjs`
- `agent/scripts/check-reassignment-fixture.mjs`
- `agent/scripts/verify-integration.ps1`
- `agent/tsconfig.fixtures.json`
- `backend/cmd/agentfixture/main.go`
- `backend/cmd/agentfixture/main_test.go`
- `backend/cmd/server/application.go`
- `backend/cmd/server/application_integration_test.go`
- `backend/cmd/server/application_test.go`
- `backend/cmd/server/listener.go`
- `backend/cmd/server/listener_test.go`
- `backend/cmd/server/main.go`
- `backend/fleet/visitor.go`
- `backend/sim/visitor_compatibility_test.go`
- `backend/sim/visitor_presets.go`
- `backend/sim/visitor_presets_test.go`
- `backend/visitor/application.go`
- `backend/visitor/archive.go`
- `backend/visitor/budget.go`
- `backend/visitor/config.go`
- `backend/visitor/controller.go`
- `backend/visitor/controller_test.go`
- `backend/visitor/controls.go`
- `backend/visitor/disk_unix.go`
- `backend/visitor/disk_windows.go`
- `backend/visitor/factory.go`
- `backend/visitor/gateway.go`
- `backend/visitor/hardening_test.go`
- `backend/visitor/integration_test.go`
- `backend/visitor/outage_integration_test.go`
- `backend/visitor/production_integration_test.go`
- `backend/visitor/scenario.go`
- `backend/visitor/security_test.go`
- `backend/visitor/sessions.go`
- `deploy/pub02/compose.example.yml`
- `deploy/pub02/production.env.example`
- `docs/pub-02.md`
- `web/src/App.tsx`
- `web/src/demo.test.tsx`
- `web/src/demo.ts`
- `web/src/restart.test.tsx`
- `web/src/restart.tsx`

Generated builds, temporary source copies, SQL backups, cookie jars, hashes, scan
reports and test logs are excluded from Git. No files are staged. The known Codex
handoff is outside the intended file list and remains untracked. Work is limited
to PUB-02 and its affected verification/deployment preparation paths.

Final source-scan preparation initially rejected a generated CRLF file manifest
(`final-source-gitleaks`); its LF encoding was corrected without changing product
source. The corrected source scan passed (`final-source-gitleaks-corrected`).
A report encoding check then normalized its UTF-8 punctuation; the completed
report is included in `final-source-gitleaks-report`. All outputs remain retained.
Final source integrity is recorded in `final-git-audit.json` and
`final-intended-source.sha256`.
