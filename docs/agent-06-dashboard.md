# AGENT-06 implementation report

AGENT-06 is implemented. The existing warehouse map, deterministic evidence, simulator controls and approval inbox remain in place. A compact agent activity panel follows the inbox in the operator context. Run selection reveals the original evidence, comparisons, sanitized investigation timeline and separate proposal/approval/execution/outcome stages.

## Files changed

| File | Purpose |
| --- | --- |
| `web/src/AgentActivity.tsx` | Activity history, selected-run evidence, reported source labels, measurements, limitations and recovery stages; polling lifecycle and selection reset. |
| `web/src/agentRecords.ts` | Bounded, version-aware parser for the exported Go `agent-run-v1` DTO and associated proposals. |
| `web/src/agentRuns.ts` | Authenticated, read-only GraphQL retrieval, bounded response parsing, distinct access/outage/empty states, cancellation and polling. |
| `web/src/App.tsx` | Inserts the panel after the existing approval inbox and binds it to connection/demo generation availability. |
| `web/src/styles.css` | Scoped panel, history, evidence table and timeline styles using the existing design. |
| `web/src/agentFixtures.ts` | Test-only typed Go-shaped records, including the R3/R4/R5 alternatives. No production fixture fallback. |
| `web/src/AgentActivity.test.tsx` | Rendered evidence, all source labels and run states, measurements, limitations, proposals, outcome separation and escaped content. |
| `web/src/agentRuns.test.ts` | Retrieval/authentication, malformed data, association checks, bounds, cancellation, runtime isolation and demo behavior. |
| `backend/graphql/agent_runs.go` | Narrow read-error classification fix: `FORBIDDEN` for unauthorized provenance reads, `UNAVAILABLE` for service/storage failures. Mutation behavior is unchanged. |
| `backend/graphql/agent_run_reads_test.go` | Tests the error distinction and sanitized messages. |
| `web/scripts/contract-entry.ts` | Exposes the new parser/read/render entry points to the existing real-HTTP integration harness. |
| `web/scripts/go-dashboard-integration_test.go` | Adds the persisted scripted-agent profile to the existing isolated Go/PostgreSQL dashboard harness. |
| `web/scripts/check-agent-backend.mjs` | Runs AGENT-05's actual mocked Mastra investigation and checks authenticated records through rendered UI, independent approval and Go execution; optionally exports credential-free HTML. |
| `web/scripts/check-backend.mjs` | Supplies the current expected observation identity in the existing charging/reassignment integration submissions, as required by merged AGENT-02. Existing assertions remain. |
| `docs/agent-06-dashboard.md` | This review report. |

## Real data flow and authorization

Opening the connected dashboard issues only authenticated reads to the existing same-origin `/graphql` endpoint. The panel requests at most ten current-runtime `agentRuns`, plus a sampled fleet observation and job state. It parses the versioned JSON before rendering. There is no browser inference endpoint, provider credential or model invocation. The test integration invokes a local scripted model explicitly; production rendering never imports its fixtures.

The existing Go service enforces operator access to current-runtime records, proposer access to their own records, and viewer denial. The panel preserves those boundaries. Forbidden, unavailable, malformed, legacy ambiguous denial and successful empty responses have distinct presentations. Raw backend/provider error messages are not displayed. The backend previously used the same error code for denial and storage failure, which blocked the required distinction; only its provenance read error mapping was changed. No schema, migration, safety-rule or agent decision changes were needed.

Parsing checks outer/inner run, runtime and observation identities, supported contract versions, bounded typed facts and activity, exact numerical comparison values, admitted selected tuples and matching current-runtime proposal associations. Unexpected properties and oversized responses fail closed. React renders only the typed projection and escapes text. Cost remains unavailable; absent measurements remain unavailable rather than zero.

Selection keys include runtime identity. Disconnects, unavailable runtimes, restarts and demo generation changes unmount or reset the panel; stopped/superseded requests cannot publish late results. Failed reads clear selectable records. Each provenance read carries its own sampled job state, so outcome text does not borrow an unrelated dashboard snapshot. The Go API remains responsible for current-runtime scoping; the client cannot derive runtime identity from an opaque observation hash.

The UI distinguishes deterministic, scripted/mock, recorded and agent-reported live-model sources. Server decision verification is separate from reported model/source/usage metadata. The local deterministic demo keeps “No live LLM” and displays an honest provenance-unavailable explanation without substituting its historical evidence for agent records.

## Verification

Final checks on 2026-10-18:

| Check | Actual result |
| --- | --- |
| Frontend Vitest, all 13 files | **133 passed, 0 failed, 0 skipped**; 56 new focused cases and 77 existing cases. |
| Frontend TypeScript | Passed, exit 0. |
| Frontend Vite production build | Passed, exit 0; final build output under ignored `web/dist/agent06-production`. |
| SSR integration contract build | Passed, exit 0. |
| Agent unit tests, all 13 unit files | **277 passed, 0 failed, 0 skipped**; backend integration file deliberately excluded by the existing unit command. |
| Agent TypeScript and build | Both passed, exit 0. |
| Offline evaluation, all 10 files | Final run **254 passed, 0 failed, 0 skipped**; evaluation TypeScript passed. |
| Real dashboard HTTP/PostgreSQL integration | **3 profile subtests passed, 0 failed, 0 skipped**, plus their passing parent test. Charging, reassignment and agent profiles. |
| Go regression suite, `-tags fleetops_test ./...` | **356 top-level tests passed, 2 failed, 15 skipped**. Including nested subtests: **1,697 passed, 2 failed, 15 skipped**. |
| Two Go golden tests in disposable LF-normalized copy | **2 passed, 0 failed, 0 skipped**. See environment explanation below. |
| `git diff --check` | Passed. |

The real HTTP integration used an ephemeral localhost PostgreSQL 18.6 database and production GraphQL/service/persistence. AGENT-05's scripted Mastra model made four local requests and six read-tool executions, selected non-first R5, and produced a verified durable record and pending proposal. The frontend retrieved and rendered it. Viewer reads were forbidden; proposer-owned retrieval succeeded. Proposer and self-approval were denied without mutation. An independent operator then approved, Go executed the transfer, and the rendered receipt distinguished transfer from delivery. Existing charging/reassignment integration assertions also passed, including permission, stale, duplicate and expiry boundaries.

No paid inference or provider network call was performed. The new browser path has only read queries; the scripted fixture restricts its network traffic to the localhost test GraphQL endpoint. The production bundle excludes the test fixture module.

### Environment results and skips

The initial evaluation run had **251 passes and 3 failures**, all existing CLI tests exceeding their default five-second deadline during concurrent validation. Rerunning unchanged assertions with `--testTimeout=60000` produced 254 passes. No test source or assertion was weakened.

The native Go failures were `TestGoldenSnapshot` and `TestWarehouseGoldenSnapshot`. Both tracked working-copy fixtures contain CRLF, while simulator JSON emits LF. Normalizing each working-copy fixture in memory exactly matched its committed Git blob. Only the ignored, disposable backend copy's two fixtures were normalized to LF; both unchanged golden tests then passed. Tracked simulator code and fixtures were not edited or regenerated. These are checkout-format limitations, with no demonstrated behavior regression.

The 15 Go skips were the two opt-in legacy Node/agent contract hooks, the demo observation exporter, and twelve existing optional/legacy simulator diagnostic tests. The new real dashboard integration separately exercised the actual persisted Mastra fixture. The standalone six-case agent backend integration file was not run in this ticket. No race-detector run was required for the read-error mapping; no concurrency primitives changed.

Sandboxed Go/database runs stalled without results and were stopped. The isolated database tests completed successfully after the sandbox boundary was lifted. The first sandboxed HTML preview was inaccessible to the browser; the credential-free localhost preview worked outside the sandbox. The temporary database and preview server were stopped after verification.

## Screenshot and limitations

`web/dist/agent06-dashboard.jpg` is a browser screenshot of a server-rendered dashboard assembled from the actual authenticated PostgreSQL-backed scripted run. `web/dist/agent06-dashboard.html` is its credential-free static snapshot. This screenshot verifies rendering, not interactive React hydration; interactive lifecycle behavior is covered by the existing frontend regression suite and new polling/selection tests.

Polling shows sampled state, not an atomic causal proof of delivery. The original admitted alternatives can become historical; Go retains fresh admission/execution checks. A command receipt is not delivery confirmation. Source, actual model and token usage remain reported metadata, and pricing is unavailable. The panel cannot access provenance through the deterministic demo gateway and intentionally does not synthesize it. Runtime scoping relies on the authenticated production Go API.

All AGENT-06 implementation acceptance criteria are covered. The native golden-file and initial timeout results above remain explicitly reported rather than hidden. No requirements were guessed, no dependencies added, no Git mutations performed, and no later ticket implemented. Stop here for owner review.
