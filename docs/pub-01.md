# PUB-01 — Isolated visitor sessions

PUB-01 adds a separately runnable, loopback-only multi-visitor service using the existing DEMO-09 Go controller, trusted scenario presets, GraphQL handler, fleet service and PostgreSQL migrations. Run one verification/launcher at a time because the existing frontend tools share `web/dist`.

**Final acceptance, 2026-10-18 (America/Chicago): PASS for the PUB-01 loopback test harness.** The final source passed 16 noninteractive Linux race tests, the affected Go/PostgreSQL regressions, all 142 frontend tests, and the real frontend HTTP integrations. Earlier failures, opt-in skips, platform limits and verified cleanup are recorded below; historical results are kept separately.

```powershell
# Full Go/PostgreSQL regressions, including the local DEMO-09 integrations
powershell -NoProfile -ExecutionPolicy Bypass -File evals/phase8/verify.ps1

# All PUB-01 API, security and concurrency tests under the Linux race detector
powershell -NoProfile -ExecutionPolicy Bypass -File evals/phase8/verify.ps1 -Race -AffectedOnly

# Explicitly start the development service; use its printed READY URL
powershell -NoProfile -ExecutionPolicy Bypass -File evals/phase8/verify.ps1 -Serve
```

The launcher owns a disposable PostgreSQL 18.6 container published only on `127.0.0.1`. The HTTP listener is also loopback-only. The race option uses the existing cached Go 1.27.1 and Node 24.21.0 images, cached Go modules, and a network namespace shared only with its test PostgreSQL container. It installs no packages. The original local Mastra regressions run on Windows; the focused Linux race command runs the visitor tests without an agent subprocess.

The service remains behind the existing `fleetops_test` harness boundary: the current trusted fixture factories and lifecycle controller are test-only. This ticket does not expose a production server, configure hosting, or implement PUB-02/PUB-03. The development server ends after 20 minutes; writing a `stop` file beside its `ready.json` requests graceful shutdown sooner.

## Ownership and authentication

1. A root-page visit reserves one of eight admission slots, including initializations in progress. Initialization has a one-minute deadline. Failure returns 503 and issues no cookie.
2. The server creates a private PostgreSQL schema and a two-connection pool for that visitor, applies existing migrations, creates distinct random proposer/operator credentials and subjects, and prepares a fresh Go runtime. Neither role shares the existing local operator identity.
3. Only after successful preparation does the server publish a random 256-bit cookie capability. The cookie is HttpOnly, host-only, path `/`, and SameSite=Strict. Its fixed lifetime is 20 minutes; refresh does not extend it or reset the warehouse. HTTP cookies are deliberately limited to this loopback development service.
4. The server uses its own session map to resolve the browser cookie and routes each request only to that visitor's controller. GraphQL and control requests reject browser-provided Authorization headers, repeated session cookies, unrecognized session cookies, query-string configuration, foreign origins, hosts or peers, and malformed control bodies. Only the server supplies backend authorization values.
5. Every forwarded request carries a context deadline bounded by session expiry. Each visitor has one tick worker, its own lifecycle gate and its own runtime/generation/observation binding. Restart drains that visitor's operations, captures evidence, replaces its runtime and invalidates its previous generation. Other visitors keep their existing controller and runtime.

The existing exact Origin, custom CSRF header, Fetch-Site, loopback peer, Host, JSON POST, generation and Step-tick checks remain enforced. The server accepts only the four fixed scenarios and existing controls. No client-provided session or runtime identifier selects an owner.

## Isolation and evidence

The private schema is retained across a visitor's restarts so their historical proposals and audits remain available. This is necessary because some existing single-owner readers and evidence captures intentionally query their entire database namespace. They now see only that visitor's schema. Fleet's current-runtime and canonical-proposal checks still deny execution of an old proposal after restart, even with the new generation header.

Simulator state, robot telemetry, jobs, scenarios, controls, proposals, decisions, agent-run rows and captured history have separate owners. A browser cannot choose a schema or access the filesystem evidence directories. Agent-run reads remain filtered by the current runtime, with no fallback to another visitor or an archived run. The existing dashboard continues to label local-demo provenance as unavailable rather than fabricate AGENT-05 records.

Public scenario initialization uses a clearly labeled scripted Go preset. It submits a pending proposal through the same authenticated GraphQL handler as a proposer, with a fresh observation ID and independent proposer identity. It never approves or executes. The original local Mastra adapter remains available for DEMO-09 regression tests. Its NodeNext source-fixture import resolution was repaired using the same approach already used by the evaluation runner.

The read-only `/demo/status` evidence envelope now supplies authoritative `proposalSource` metadata: `go-scripted`, `mastra-deterministic`, `recorded-model`, `live-model`, `none`, or `unknown`. The public visitor factory explicitly selects `go-scripted`; no-action presets select `none`. Only the existing default local adapter path selects Mastra implicitly; an unclassified custom proposer is `unknown`. The frontend normalizes absent, invalid or unrecognized values to `unknown`, including a contradictory `none` attached to a proposal. Both the inbox summary and expanded evidence use this classification. Go-scripted output makes no claim that a Mastra reasoning schema, model grounding or model policy check ran. Recorded and live classifications have distinct labels and do not claim independent provider verification. Their rendering/classification is tested without invoking a model.

There are no model calls on visit, restart, scenario selection or controls; no paid-model endpoint; and no browser API keys. Scripted records explicitly say `scripted fixed demo preset; no model inference`. They are not independently verified live inference. The boundary remains **LLM proposes. Go validates. Human approves. Go executes.** Scripted demo evidence substitutes for the proposal source only.

## Expiry, shutdown and failures

Expiry removes the cookie capability from the manager before draining the controller. Tick and expiry timers stop, the controller is closed, credentials are cleared and the pool closes. Successful cleanup saves private committed proposal, audit, agent-run and migration evidence, then drops only that visitor's schema. Shutdown also waits for initializers and workers, and rejects newly initialized sessions if shutdown won the race.

Initialization and replacement failures never select another session as a fallback. A poisoned or panicking runtime becomes unavailable within its own owner. Failed database/capture operations retain the existing fail-closed behavior. Cleanup still revokes capabilities and closes the pool when capture fails; if final database evidence cannot be saved, the schema is retained for reconciliation and the launcher reports failure. A failed schema drop is also reported. These retained schemas require local reconciliation; no live simulator or pooled connection is kept for them.

Controller capture failure alone does not require retention when the separate database archive succeeds. The archive captures all four existing tables in one MVCC statement, including every column and migration record, and inventories base tables; unexpected tables fail completeness verification. It binds the schema name and its server-captured namespace OID, checks current-user ownership, uses an exclusive fresh archive file, checks file write/Sync/Close, verifies exact readback and archive identity/completeness, and checks directory Sync/Close. The pool closes before a second ownership check and schema drop. Any failed or uncertain archive/durability outcome retains the schema and reports `visitor schema retained for reconciliation`. Failed migration/open initialization also retains the created schema and reports reconciliation instead of silently discarding it.

The session map is in memory. A process restart invalidates every old capability; it does not recover a warehouse from an untrusted identifier. Crash-retained schemas/evidence are not automatically attached to new visitors. Separate schema pools add connection/migration overhead; the eight-visitor cap bounds active pool capacity to 16 visitor connections, plus the administrator pool. Durable session recovery, public abuse controls, disk-retention policy, production packaging and deployment remain outside PUB-01.

## Final acceptance evidence — 2026-10-18

Branch: `phase8-pub01-visitor-isolation`. HEAD: `0615f8c04c43c4d629e0bd276911ee0d23b8d77e`. Tests ran against the dirty working tree, not just HEAD. No commit, push, merge, checkout, reset or branch change was performed. Existing owner files, including `docs/CODEX-CLAUDE-HANDOFF.md`, were preserved.

Exact evidence directory:

```text
evals/dist/pub01/acceptance-ed1c211a839b434298a24e756dab1f2c/
```

Each monitored command has `<name>.result.json` with its exact executable, argv, working directory, UTC start/end, exit status and sampled free space, plus separate complete `.stdout.log` and `.stderr.log`. `results-summary.json` lists all command outcomes, failures and Go skips. `final-source-hashes.json` and `source-copy-verification.json` identify the final files and verify that the staged Go files match the repository. Both staged simulator goldens remained byte-identical to the repository; no fixtures or expected outputs were rewritten for this run.

| Final check / artifact prefix | Actual result |
| --- | --- |
| `linux-pub01-race-complete` | Exit 0; **16 passed**, only `TestPUB01Serve` skipped by its explicit-launcher guard; fleet **13.438s**; no race reports. |
| `linux-go-regression` | Exit 0 under `-race`; **79 top-level tests passed**: auth 1, db 17, fleet 46, GraphQL 15. Package times 1.021s, 1.784s, 11.274s and 4.707s respectively. Three unrelated opt-in agent tests skipped, listed below. |
| `native-demo-complete` | Exit 0; **24 passed**, one read-only observation exporter skipped; fleet **24.249s**. Includes actual deterministic Mastra subprocesses and PostgreSQL. |
| `web-go-postgres-http` | Exit 0; charging, reassignment and agent profiles all passed; fleet **5.297s**. The agent profile exercised actual scripted Mastra investigation, PostgreSQL provenance, authenticated frontend parsing/rendering, independent approval and Go receipt, with zero paid inference. |
| `web-tests-complete` | Exit 0; **13 files, 142 tests passed**. Covers four source classes, missing/unknown/contradictory metadata, no-action evidence and unchanged approval controls. |
| `web-typecheck-final`, `web-build-final` | Exit 0 for both; TypeScript check and Vite production build passed. |
| `agent-build`, `web-contract-build` | Exit 0 for both; existing deterministic adapter and SSR HTTP contract bundle built. |
| `owned-schema-reconciliation` | Exit 0 under `-race`; all **14** schemas retained by this run's initial failed archive check were separately archived and verified, pools closed, ownership rechecked and schemas removed. This operational artifact is not counted among the 16 regression tests. |
| Final syntax/format checks | Changed/new Go files are gofmt-clean; modified Node script passes `node --check`; PowerShell launcher parses; `git diff --check` passes. Unchanged Phase 4 files were not reformatted. |

The final race suite proves independent HTTP cookie jars and real PostgreSQL ownership across cross-visitor approve/reject attempts, independent human decisions and execution, reconnect, restarts, stale generations/proposals, expiry with schema removal, concurrent lifecycle operations, database poison, runtime panic and request deadlines. All four public presets passed: routine charging, healthy operations, blocked/no candidate, and blocked reassignment. Actionable presets reported Go-scripted provenance; no-action presets reported no source invocation. B remained unchanged by A's scenario changes and failures.

The new real PostgreSQL retention test commits a real pending proposal and audit row, then exercises database-read failure, write failure, truncated on-disk readback, uncertain commit acknowledgement, and namespace-OID mismatch. Every case proves pool shutdown and continued availability of the committed row in the retained owned schema. The uncertain-commit case also fails controller capture and proves capability revocation and reporting of both errors. A separate test fails controller capture while allowing a complete durable database archive, then proves schema removal and preserved committed records. Fault fixtures are reconciled through the healthy archive path after their injected fault is removed.

Commands executed sequentially through the storage monitor (full executable paths and environment are retained in the run artifacts):

```powershell
$runner = '960f0dc13a7f043439a60ac7a37c337433494d7122330257b64304feed5e6fe0'
docker exec $runner go test -race -tags fleetops_test ./fleet -run '^TestPUB01' -json -count=1 -timeout=5m
docker exec $runner go test -race -tags fleetops_test ./auth ./db ./fleet ./graphql -skip '^(TestPUB01|TestDemo|TestPhase4|TestDashboard)' -json -count=1 -timeout=5m

# Native commands used the same staged backend and the same owned PostgreSQL.
$go127 = 'C:\Users\Ayush\go\pkg\mod\golang.org\toolchain@v0.0.1-go1.27.1.windows-amd64\bin\go.exe'
# cwd: <evidence-directory>/work/backend
& $go127 test -tags fleetops_test ./fleet -run '^TestDemo' -json -count=1 -timeout=5m
& $go127 test -tags fleetops_test ./fleet -run '^TestDemoRestartIntegration$' -json -count=1 -timeout=5m
& $go127 test -tags fleetops_test ./fleet -run '^TestDashboardHTTPIntegration$' -json -count=1 -timeout=5m

# cwd: repository, unless stated otherwise
npm --prefix web test -- --maxWorkers=2
npm --prefix web run typecheck
npm --prefix web run build
npm --prefix agent run build
# cwd: web
npm exec -- vite build --ssr scripts/contract-entry.ts --outDir dist/contract
```

Native environment: `FLEETOPS_TEST_DATABASE_URL=postgres://postgres@127.0.0.1:56791/fleetops_pub01?sslmode=disable`, `FLEETOPS_REQUIRE_DB=1`, `FLEETOPS_P405_ROOT=<repository>`, `FLEETOPS_P404_WEB_ROOT=<repository>/web`, `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off`, `GOMAXPROCS=2`. The Linux runner used the same database over its shared network namespace at port 5432, `/repo` read-only, cached modules read-only, CGO enabled, and `FLEETOPS_PUB01_EVIDENCE=/artifacts/evidence`. It used the cached `golang:1.27.1` image and PostgreSQL **18.6**; no packages or images were downloaded. One backend staging directory, one Go runner and one PostgreSQL instance were reused. The original launcher was not used for this acceptance run because it creates extra temporary environments and normalizes copied goldens.

Failures and exclusions are preserved:

- `web-tests` initially failed one stale design assertion that assumed a deterministic adapter without evidence. The corrected assertion requires unknown provenance while retaining canonical-parameter and human-approval checks. Subsequent frontend runs passed 141 tests, then the complete 142-test run passed after the final no-action/contradictory-source cases.
- `linux-pub01-race` initially failed 6 top-level tests because PostgreSQL serializes `oid` as a JSON string. Archive verification correctly retained 14 schemas. An explicit `oid::bigint` cast corrected the envelope type; completeness/ownership assertions were retained. `linux-pub01-race-final` passed 16 tests before the combined capture/uncertain-archive assertion; `linux-pub01-race-complete` passed against the final source including that assertion.
- Two native launcher attempts failed before running tests: the system Go executable was 1.26.0 with `GOTOOLCHAIN=local`, then automatic cached-toolchain selection rejected checksum verification with `GOSUMDB=off`. Directly executing the already installed Go 1.27.1 binary resolved this without downloads.
- `native-demo-cached-toolchain` had 23 passing tests and one failure: unchanged `TestDemoRestartIntegration` exceeded its 12-second progression deadline at tick 45 with J1 already completed. The unchanged isolated rerun passed (14.700s package time), and the unchanged complete demo suite then passed. The deadline, simulator expectations, fixtures and execution code were not relaxed. This observed timing variability remains a test-environment limitation.
- `TestPUB01Serve` remains an explicit serving opt-in. The three core-suite skips were `TestAgentIncidentHTTPClient` (Node HTTP contract opt-in), `TestAgent07PairedEvaluation` (isolated AGENT-07 opt-in), and `TestIntegrationAgentRunMastraHTTP` (built-agent HTTP opt-in). The native demo skip was `TestDemo08ExportObservations`, which requires `FLEETOPS_DEMO08_EXPORT`. None is claimed as rerun coverage. Tests deliberately excluded by the core-suite `-skip` filter were covered by the separate PUB-01/demo/HTTP commands, except the explicit `TestPhase4AcceptanceDemo` serving target, whose earlier result remains historical.
- No new browser smoke, full simulator suite, standalone 279-test agent suite or 281-test evaluation suite was run in this final review. Their previous results below remain historical. This change affects harness provenance/evidence and frontend labels; the relevant authenticated HTTP integrations and existing deterministic adapter build were rerun. Successful durable archival was verified in the Linux runner on the current mounted filesystem. Other platform/filesystem flush behavior is not certified; failed or uncertain Sync/Close retains the schema.

Environment and cleanup:

- C: was rechecked before testing. Docker was initially stopped on recheck and ordinary Desktop startup restored the Linux engine; no reset, prune or repair was used. The only pre-existing container was the preserved stopped `l3harris_windows`, ID `fbaf2feec74d98d7aa2aba6152602b0bbcdfec93580e4cbed84fa0333d3a3e9d`.
- Every build/test was sequential and sampled free space each second. The lowest recorded free space was **11,096,686,592 bytes** (about **10.33 GiB**), above the **5 GiB** guard throughout. No guard interrupted a command. Final free space after cleanup was **11,274,768,384 bytes** (about **10.50 GiB**).
- This run created PostgreSQL container `a8e7cdeb11f82d5e97e703079e0b573e622bfe467513251f937ac243f2b4e7b9`, Go runner `960f0dc13a7f043439a60ac7a37c337433494d7122330257b64304feed5e6fe0`, and volume `fleetops-pub01-db-acceptance-ed1c211a839b434298a24e756dab1f2c`. Both containers and the volume carried `fleetops.verification=acceptance-ed1c211a839b434298a24e756dab1f2c`; containers also carried `fleetops.ticket=PUB-01`. Labels, exact IDs, image and mounts were inspected before cleanup, rather than inferring ownership from names.
- The 14 retained schemas were matched exactly to this run's failed log and namespace OIDs before reconciliation. `reconciliation-owners.json`, `reconcile_verification_test.go`, `owned-schema-reconciliation.*` and `reconciliation-archive-index.json` preserve the procedure, outcomes, file hashes and record counts. All archives remain in `reconciliation/`; other test captures remain in `evidence/`. Final database inspection found no non-system schemas except `public` before container disposal.
- Only those two owned containers were stopped and removed, and only that owned volume was removed. `cleanup.json` records each command and exit 0. Final `docker-final.jsonl` contains only the unchanged, stopped `l3harris_windows` container. All five previously reported PUB-01 container names listed in the historical section were absent both before and after this run. Their earlier removal method is not independently asserted, and this run did not delete resources based on those names.
- No existing image, volume, repository, compiler cache, historical evidence or unrelated resource was deleted. The new run's source copy, logs, failed results and archives remain available for review.

All requested PUB-01 acceptance checks are satisfied within the existing test-only loopback boundary. Remaining limitations are the observed unchanged demo timing flake, the explicitly unrerun opt-ins/suites, and unverified platform/filesystem durability behavior described above. Frozen Go safety rules, GraphQL authorization, independent approval and execution semantics remain unchanged.

## Historical verification — before final acceptance corrections

The two-visitor proof uses two independent HTTP cookie jars against real HTTP listeners and PostgreSQL, rather than React-only state. Tests verify distinct runtime/generation/observation/proposal identities, reconnect without reset, private proposal/provenance reads, cross-owner approve/reject denial, independent approval and stepping, scenario selection, restart, stale generations and stale proposals, and expiry with actual schema deletion while B remains unchanged.

Additional tests cover all four presets through the public gateway, the charging and reassignment effects after approval, pending-decision tick gates, concurrent restarts, concurrent admission and capacity reservations, initialization versus shutdown, failed capability issuance, hostile request headers/cookies/routes, database failure and runtime poison, runtime panic containment, capture failure cleanup and request deadlines bounded by expiry. The provenance isolation fixture is an explicitly mocked, incomplete database record; it makes no live-model claim.

Previously measured verification results (not substituted for the final-source checks above):

| Check | Real result |
| --- | --- |
| `verify.ps1` full backend/PostgreSQL regressions | Exit 0; every backend package passed, including fleet (68.882s), GraphQL (19.978s) and simulator (37.257s). The final visitor deadline guard was subsequently checked by the focused native run below. |
| Final `go test -tags fleetops_test ./fleet -run '^TestPUB01' -v -count=1 -timeout 2m` | Exit 0; all 13 non-interactive PUB-01 tests passed, including all four presets, expiry deadlines and capture failure; `ok .../backend/fleet 8.782s`. Only the explicit serving target was skipped. |
| Earlier `verify.ps1 -Race -AffectedOnly` | Exit 0; all 10 visitor tests then present passed with no race reports; `ok .../backend/fleet 5.898s`. |
| Final race rerun | **Unfinished:** Docker stalled creating the test container after the system drive filled. No final race result is claimed for the added four-preset, capture-failure and deadline checks. The final native run above passed after freeing task-owned files. |
| Real frontend/Go/PostgreSQL HTTP integration | Exit 0; charging, reassignment and scripted agent-provenance profiles all passed; `ok .../backend/fleet 12.193s`. |
| Original `TestPhase4AcceptanceDemo`, explicit local/submit/check-only opt-ins | Exit 0; fresh tick-42 fixture, committed pending proposal and no execution; `ok .../backend/fleet 4.274s`. This target does not claim to test a human decision. |
| Browser smoke and graceful serving shutdown | Initial decision gate displayed; selected Healthy fleet operations; stepped to tick 1; browser refresh preserved tick 1; `TestPUB01Serve` passed and the listener shut down, `ok .../backend/fleet 69.417s`. |
| `npm --prefix web test` | 13 files, 133 tests passed. |
| `npm --prefix agent test` | 14 files, 279 tests passed. |
| `npm --prefix evals test -- --maxWorkers=2 --testTimeout=30000` | 11 files, 281 tests passed. The default 5-second CLI tests timed out under concurrent verification load; the explicit rerun passed. |
| Web, agent and evals typechecks | All passed. |
| Agent build and web production build | Passed. |
| Final PowerShell parser, Node script syntax, Go formatting | Passed. |

The final focused native and frontend integrations reused the existing isolated test database on loopback after Docker's API stalled. Their schemas remained separately generated and owned. The final focused output is saved at `evals/dist/pub01/final-native.log`. Browser proof is saved at `evals/dist/pub01/browser-smoke.jpg`.

The first sandboxed Vitest runs failed on temporary import files; rerunning outside that temporary-file mapping passed. An overlapping frontend build initially removed another verification's disposable assets; subsequent builds were run sequentially. The shared local adapter import issue and stale DEMO-09 observation comparison were fixed and their integrations then passed.

Only task-created compiler caches and duplicated Linux Node binaries were deleted to recover disk space. The race launcher now deletes its copied Node runtime on exit and no longer retains a host compiler cache. Docker was not restarted and no unrelated containers or files were changed. Container cleanup status is recorded below; failed-run evidence remains local.

At the end of that session the bounded Docker stop attempt timed out without a response. Cleanup was **unconfirmed** for these reported test-container names (the final one may never have completed creation). The final review above confirms all five are now absent:

```text
fleetops-pub01-df31e932980f416da69f318d77999b02
fleetops-pub01-e67ceb56c0dc4f7a9a7fbfbd44ef4cda
fleetops-pub01-cd273197a59247d99b4fdc4d28646184
fleetops-pub01-2a02464b441849deafca9e9117624b6f
fleetops-pub01-4331bc983fa44916b13e4916af74d2bf
```

The report described these as PostgreSQL test containers configured for loopback publication. The browser smoke HTTP service was confirmed stopped. Rerunning the final visitor race suite and checking those containers were outstanding at that point; both are resolved by the final review above. No requirement was filled in with an assumed authorization identity or a fallback runtime.

The launcher normalizes CRLF to LF only in the two disposable copied simulator golden files. Both repository golden files were independently compared to actual simulator output and were identical after newline normalization. Simulator values, source fixtures and algorithms were not regenerated or changed.

## Exact source files changed

| File | Change |
| --- | --- |
| `agent/scripts/phase4-demo-reasoning.mjs` | Resolve NodeNext source fixture imports for the existing deterministic local adapter. |
| `evals/phase4/controller_test.go` | Typed proposal-source classification in read-only evidence metadata. |
| `evals/phase4/scenario_test.go` | Reuse trusted factory with private subjects and authoritative proposal-source classification; preserve local defaults. |
| `evals/phase4/scenarios_integration_test.go` | Require fresh observation identity on restart while comparing identical simulator state. |
| `evals/phase8/sessions_test.go` | Server-side admission, ownership, expiry, request deadlines, tick workers, panic containment and shutdown. |
| `evals/phase8/gateway_test.go` | Cookie-to-owner routing and preserved local browser-request protections. |
| `evals/phase8/factory_test.go` | Private schema/pool/credentials, explicit Go-scripted source, pool shutdown and ownership-checked database cleanup. |
| `evals/phase8/archive_test.go` | Complete, readback-verified, durable database archival and fail-closed schema retention. |
| `evals/phase8/serve_test.go` | Explicit loopback multi-visitor development launcher target. |
| `evals/phase8/security_test.go` | Hostile input, concurrency, cleanup and failure tests. |
| `evals/phase8/integration_test.go` | Actual two-session Go/PostgreSQL API, four-preset provenance, and real failed/uncertain archival proofs. |
| `evals/phase8/verify.ps1` | Disposable full/focused/race verification and explicit serving. |
| `web/src/Evidence.tsx` | Parse authoritative source, display distinct labels, and remove invented Mastra/model validation claims. |
| `web/src/ApprovalInbox.tsx` | Source-aware inbox summary; unknown when matching evidence is unavailable. |
| `web/src/Evidence.test.tsx` | Regression coverage for source classes, missing/unknown sources, and existing evidence/approval boundaries. |
| `web/src/design.test.tsx` | Require unavailable provenance when a proposal has no source evidence. |
| `docs/pub-01.md` | Lifecycle, trust boundaries, trade-offs, verification and file list. |

GraphQL schema, tool signatures, dependencies, frozen safety rules, robot commands and simulator algorithms are unchanged. No branches, commits, pushes, merges, hosting resources, firewall changes or paid inference were created. PUB-02/PUB-03 remain unimplemented.
