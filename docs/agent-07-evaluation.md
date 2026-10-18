# AGENT-07 implementation and verification

The offline evaluation infrastructure is complete. Real-model investigation quality, provider latency, actual token usage, actual billed cost and actual human review remain unmeasured. No model was selected and no paid API request was made. The live integration is implemented and tested with local transports; a real pilot requires separate model, scenario and cost approval.

The architecture remains **LLM proposes. Go validates. Human approves. Go executes.** Existing production agent behavior, API/tool signatures, schema, migrations, simulator algorithms and seven safety rules are unchanged. New Go fixtures and harness code are compiled only with `fleetops_test`. The only added production-build module is an evaluation-specific model resolver; existing agent entry points do not call it.

## Exact file manifest

All paths below are repository-relative. Every entry is new except the two marked modified. Generated verification scratch files under ignored `evals/dist/` are excluded.

| File | Change and purpose |
|---|---|
| `agent/src/evaluation-investigation-model.ts` | Resolve only the explicitly reviewed OpenAI Responses model, clamp output including reasoning, disable provider retries and set fixed retention/service-tier options. Used by the separate evaluation CLI. |
| `agent/src/evaluation-investigation-model.test.ts` | Two tests verify caps, bound model methods and denial before generation. |
| `backend/sim/agent07_fixture.go` | Test-tagged reproducible initial conditions before service runtime creation; no production simulator change. |
| `backend/sim/agent07_fixture_test.go` | Repeat snapshots/ticks, non-first candidate order, ties, absent recipients and route/battery conflict. |
| `backend/fleet/agent07_export_test.go` | Test-tagged helper reuses isolated PostgreSQL integration setup and actual fleet service. |
| `backend/fleet/agent07_integration_test.go` | Authenticated production GraphQL, separate proposer/operator, persisted v2 evidence, atomic stale control and independent outcome branches. |
| `evals/fixtures/agent07-v1.json` | Predeclared twelve-case dataset, objectives, expected behavior, quality metric, seed and horizon. |
| `evals/fixtures/agent07-price-review.template.json` | Intentionally invalid blank review template; no selected model or invented price. |
| `evals/src/agent07-dataset.ts` | Version/hash checks, independent baselines, tie handling, regret and Pareto scoring. |
| `evals/src/agent07-scripted.ts` | Free local Mastra adapter drives real tools; no expected labels or full observations enter its model closure. Includes negative/failure probes. |
| `evals/src/agent07-cost.ts` | Current hash-reviewed pricing, full-context six-request reservations, finite budget and live/CI guards. |
| `evals/src/agent07-network.ts` | Fixed OpenAI endpoint and exact localhost Go endpoint, request-body verification, no hidden retries, retained uncertain reservations. |
| `evals/src/agent07-runner.ts` | Actual `runRecoveryDecision` investigations, sanitized measurements, unknown handling, denominators and replay projection. |
| `evals/src/agent07-outcomes.ts` | Independent Go admissions and matched simulator effects, censored completion and no-intervention handling. |
| `evals/src/agent07-cli.ts` | Strict offline-default/local CLI, plan before generation, reviewed exact model, source digest, isolated database and explicit test approvals. |
| `evals/src/agent07.test.ts` | Twenty-seven tests cover methods, real Mastra tools, failure/no-retry controls, costs, transport and live guardrails. |
| `evals/scripts/agent07.mjs` | Node 24 TypeScript launcher using installed dependencies and built-in transforms. |
| `evals/package.json` — modified | Add `npm run agent07`; no dependency or lockfile change. |
| `evals/README.md` — modified | Commands and interpretation for the distinct AGENT-07 modes. |
| `docs/agent-07-evaluation.md` | This implementation, acceptance, verification and limitations report. |
| `docs/evidence/agent07/offline-comparison.md` | Compact per-scenario and aggregate comparison with measured limitations. |
| `docs/evidence/agent07/offline-tool-report.json` | All 39 free synthetic policy trials and sanitized investigation measurements. |
| `docs/evidence/agent07/offline-go-report.json` | All 44 authenticated Go experiment branches, original identities/states, persisted evidence, approvals and outcomes. |
| `docs/evidence/agent07/replay-check.json` | Two-run scientific equality proof, including what was excluded. |
| `docs/evidence/agent07/draft-pilot-plan.json` | Two-scenario planning envelope; model, pricing and dollar ceiling remain unselected. This is not live authorization. |
| `docs/evidence/agent07/verification-results.json` | Exact pass/fail/skip counts, skip identities and environmental notes. |

## Dataset and provenance

Benchmark `agent07-v1`, fixtures `agent07-fixtures-v1`, seed 42, 200 post-decision ticks. Dataset SHA-256: `1c1824a719664f967edea577424914f876fdea8e48fb5fda9abfc06df8b9b57f`.

Base revision `a61faa1f512f39cd5d15c0c0833a05b6d709dfbc` was supplied by the user; it was not queried or independently verified with Git. Evaluated source digest: `7c81b43e96b513e4bb4830ebb50aa748f2b4c70f64bc6f07f6100b459dc0ada8`. `sourceDigest()` in the CLI documents the exact normalized source/lock/module file list. This supplements the supplied base SHA for the implementation being evaluated; it is not a whole-repository digest. One initial read-only Git status occurred before the attachment's no-Git restriction was read; no subsequent Git operations occurred. No commit, push, merge or branch operation was performed.

The manifest declares blocked recovery, a non-first minimum-pickup recipient, three route/battery objectives, tied pickup minima, no recipients, healthy state, poisoned runtime, atomic stale submission, false evidence and invalid tool. Expected labels stay in host scoring code. The production investigator receives its normal objective and incident references, then discovers facts through `readIncident`, `listRecoveryCandidates`, `readRobots` and `readJobs`. No deterministic `supportedAction` answer hint is supplied.

Default offline mode executes real Mastra calls and tools against explicitly synthetic observations. Its IDs cannot authorize Go submissions; Go admission and simulator outcomes are labeled unmeasured. The `--offline --go` mode instead uses actual Go-issued observations from a fresh authenticated GraphQL service and isolated PostgreSQL per branch. Each v2 run is persisted through AGENT-05, including server observation facts, agent-reported completion, independently verified decision and proposal association. Structured comparisons and sanitized tool-name events are retained; private raw model/tool text is not published.

Historical Phase-5 OpenAI **11/12** was assisted by `supportedAction`. It remains a historical assisted result. The existing **17-case** synthetic contract benchmark remains separate. Neither is pooled with AGENT-07 or presented as independent real-model accuracy.

## Baselines, scoring and experiments

A selects the first admitted candidate. B minimizes `pickupSteps`. C maximizes `battery - max(30, requiredBattery)`. Equal extrema preserve initial order, while scoring treats all equal extrema as valid. None of these policies changes the admitted set. Treatment invokes the existing genuine v2 Mastra/TypeScript/provenance path using the free adapter in these examples.

Validity is separate from quality: an initially admitted selection can still fail current Go admission. Pickup/headroom regret is calculated only for the corresponding objective. `compare_tradeoffs` does not acquire a unique best candidate; pairwise Pareto dominance is reported separately, and the declared tradeoff case permits a grounded `operator_tradeoff` abstention. The scripted dominated choice is retained, not tuned away.

Each of eight comparable scenarios restores the same seed, initial simulator state, scheduled incidents and job opportunity for five independent branches: no intervention, A, B, C and v2. Four runtime/negative controls have only a v2 branch, for 44 branches total. Setup variants modify only test-tagged initial positions/batteries before runtime identity issuance. There are no measurement-window faults or additional job arrivals. Ordinary production tick behavior measures the following 200 ticks. The stale case alone deterministically schedules one tick after the final client read and before the finish operation reaches Go admission; it uses no timing sleeps.

No model is given operator credentials or approval/execution tools. Submission is disabled by default. Explicit `--test-harness-approvals` lets this isolated experiment submit; an independent test operator approves only known pending, verified receipts using the existing Go decision and execution preflights. Simulator state is checked unchanged before approval (apart from the stale case's declared tick). The operator identity is held only by the Go parent, not the Node investigator. Every approval is labeled **test-harness approval; NOT actual human approval**. Unknown or failed acknowledgements are not retried or approved; failures stay in attempted denominators, unattempted trials are counted, and the experiment stops when appropriate.

Delivery count, reassignment success, J1 completion tick/progress, unresolved jobs, blocked robot samples and final aggregate stored battery come from actual Go states. Samples count robots after each tick, rather than continuous duration. Battery is percentage-points, not measured energy. Incomplete completion stays null at the observation horizon; there is no percentage speedup against a censored control. Empty/incomparable interventions have null effects. Reassignment does not repair an obstruction or guarantee delivery.

## Measured offline results

See [the full comparison](evidence/agent07/offline-comparison.md) and [raw Go report](evidence/agent07/offline-go-report.json).

- Two full runs: **44/44 scientific branch results identical**. Random IDs/timestamps and measured latency are excluded from equality, but retained in the original report.
- Twelve attempted investigations, none omitted. Initially admitted selections **7/7**; current Go pending admissions **6/7**, with the stale attempt correctly denied. All three deterministic policies admit **6/6** each.
- Grounding acceptance **10/12**: one deliberately fabricated-evidence response rejected, one deliberately invalid-tool response failed. Appropriate required abstention **3/3**. These are scripted infrastructure results, not model accuracy.
- Tools **56/56** successful counted executions; evidence completeness **11/12** (invalid-tool probe intentionally incomplete); six-request/eight-tool compliance **12/12**. There were **0 direct local adapter failures / 12 investigations**. Provider HTTP failure rate is unavailable without provider requests.
- Twenty-four independently approved simulated reassignments succeeded (18 baseline, six treatment), with **0/24 execution failures**. The one stale admission denial is separate. These independent worlds are not 24 AI recoveries.
- Non-first R4 completed J1 at tick **45**, versus first-eligible R3 at **50**, tying minimum-pickup. Conversely, scripted R5 in blocked-multiple finished at **48**, three ticks slower than A/B/C, and was dominated. Headroom preference can trade throughput for reserve. No real-model superiority is claimed.
- Mock usage is available in **11/12**, unavailable in **1/12**; subtotal **80 mock token units** is not real usage or billing. Host summary latency **171–592 ms** across 12 scripted investigations is local, not OpenAI latency. The persisted report retains the original agent duration and separately measured Go server duration. Validated estimated cost and actual billed cost remain null.

## Verification results

Counts include subtests where stated. Overlapping suites are not added into a misleading grand total. Exact skipped test names are in [verification-results.json](evidence/agent07/verification-results.json).

| Check | Passed | Failed | Skipped | Other checks |
|---|---:|---:|---:|---|
| Evaluation Vitest, 11 files | 281 | 0 | 0 | Typecheck passed |
| Agent unit Vitest, 14 files | 279 | 0 | 0 | Typecheck and build passed |
| Frontend Vitest, 13 files | 133 | 0 | 0 | Typecheck and production build passed |
| Existing agent production-backend PostgreSQL integrations | 6 | 0 | 0 | Actual server application and isolated loopback database |
| Full native Go tagged suite, including subtests | 1709 | 2 | 14 | Production build passed; top level: 361 pass, 2 fail, 14 skip |
| Go race: fleet, GraphQL, safety, proposal, including subtests | 235 | 0 | 1 | Top level: 84 pass, 0 fail, 1 skip; actual isolated DB and stale/negative controls |
| Go race: new simulator fixtures, including subtests | 9 | 0 | 0 | Two top-level tests |
| Existing golden tests in disposable LF-normalized copy | 2 | 0 | 0 | Tracked CRLF files untouched |
| Complete Go experiment replay | 44 equal branches | 0 | 0 | Two successful full runs, 12 investigations each |

The two native failures are the existing Windows CRLF `TestGoldenSnapshot` and `TestWarehouseGoldenSnapshot`; the unchanged assertions both pass when only the disposable copy's golden files use LF. The 14 skips are the opt-in AGENT-07 experiment (run separately), demo export, and 12 existing opt-in diagnostic tests. The race suite's sole skip is demo export. No assertion or production fixture was weakened.

Initial legacy Node hook checks emitted an experimental TypeScript-transform warning into `CombinedOutput`, interfering with their JSON parsing. Rerunning unchanged assertions with `NODE_NO_WARNINGS=1` resolved that diagnostic issue. An initial Docker fleet/GraphQL race build could not find the cached module directory with downloads disabled; setting `GOMODCACHE=/go/pkg/mod` and `GOPATH=/go` resolved it. These were environment failures, not hidden final passes. Native race support was replaced with the cached Linux GCC/Go/Node test image. Docker/PostgreSQL needed sandbox escalation; no unresolved sandbox restriction remains. No new dependencies or downloads were needed.

Verification used Node 24.21.0, cached Go 1.27.1, installed npm dependencies and isolated PostgreSQL 18.6. Go builds set `GOFLAGS=-buildvcs=false`; `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off` prevented downloads. Full native tests used `go test -tags fleetops_test ./... -count=1 -json`, database-required mode and the existing test-only Node contracts through the Node TypeScript hook. The Linux image ran the four packages with `-race`, plus `./sim -run '^TestAgent07' -race`. AGENT-07 runs are serial and opt-in through the CLI. Existing six TypeScript backend integration tests were run against the production `application()` inside a disposable server test wrapper. No tracked compatibility code changed.

## Offline commands and draft pilot

From `evals/`, Node 24 and installed agent dependencies are required:

```powershell
npm run agent07 -- --offline --source-revision a61faa1f512f39cd5d15c0c0833a05b6d709dfbc --output dist/agent07-tool.json
npm run agent07 -- --plan --scenario non-first-pickup,tradeoff-headroom --source-revision a61faa1f512f39cd5d15c0c0833a05b6d709dfbc
```

For real Go observations and outcomes, set `FLEETOPS_EVAL_GO` to the installed/cached compatible Go executable and `FLEETOPS_TEST_DATABASE_URL` to a deliberately disposable localhost database named `fleetops_agent07...` or `fleetops_test...`. There is no production database fallback. The integration helper creates and drops an isolated schema for each branch.

```powershell
npm run agent07 -- --offline --go --test-harness-approvals --source-revision a61faa1f512f39cd5d15c0c0833a05b6d709dfbc --output dist/agent07-go.json
```

The [draft envelope](evidence/agent07/draft-pilot-plan.json) selects `non-first-pickup,tradeoff-headroom`, one trial each: **2 investigations, at most 12 model requests, at most 6,000 output tokens per request / 72,000 total**, eight tool executions and 30 seconds per investigation, plus a ten-minute whole-run ceiling. This draft uses offline planning, selects no provider/model and authorizes nothing. Its provider request field is zero because it is an offline draft. A live plan for the same selected cases would reserve all 12 potential requests. Input and estimated dollar bounds are unavailable until exact model/pricing review; inventing a dollar ceiling would be unsafe.

The next review must supply an exact supported OpenAI model identifier, its official model documentation (context window, maximum output and Responses output-cap support), current applicable input/output rates, billed reasoning semantics, highest long-context tier, all ancillary fees, and a finite approved dollar ceiling. The intentionally invalid price template must be copied and reviewed; all acknowledgements must become true. Review cannot be future-dated, older than seven days, or valid for more than seven days. Any unsupported safe bound blocks live mode.

For one request, the reservation is rounded upward to the micro-dollar:

`(full model context × highest applicable input rate + capped output including reasoning × output rate) / 1,000,000 + all per-request fees`

That amount is reserved for **every** possible request, including repeated schemas/tool context. The plan reserves six requests per investigation and compares the total with the ceiling; there is no assumed discount for cache hits or short observed prompts. Uncertain sends retain the reservation. The transport also checks the serialized request against the reviewed context and exact model/output/tool/tier settings. A hash-reviewed acknowledgement is not a provider hard spending cap. Actual billed cost requires authoritative billing evidence and stays null here. See the official [reasoning-token guide](https://developers.openai.com/api/docs/guides/reasoning) and [pricing documentation](https://developers.openai.com/api/docs/pricing); these must be reviewed again for the chosen model at pilot time.

After the model and price record are supplied, generate a live **plan only**, without credentials or inference. `$ReviewedModel`, `$ReviewPath` and `$ApprovedDollarCeiling` are deliberately unspecified review inputs:

```powershell
$reviewHash = (Get-FileHash -LiteralPath $ReviewPath -Algorithm SHA256).Hash.ToLowerInvariant()
npm run agent07 -- --live --go --plan --scenario non-first-pickup,tradeoff-headroom --trials 1 --provider openai --model $ReviewedModel --cost-review $ReviewPath --approve-cost-sha256 $reviewHash --budget-usd $ApprovedDollarCeiling --source-revision a61faa1f512f39cd5d15c0c0833a05b6d709dfbc
```

Review that printed selected-scenario/model/source/dataset/pricing hash and maximum-call/token/dollar plan before any live authorization. Actual generation additionally requires the exact `--approve-plan-sha256`, server-side `OPENAI_API_KEY`, an explicit live invocation and the isolated database; CI/Vitest/test mode refuses it. **Do not run it as part of AGENT-07 implementation.** Default live submissions remain disabled. Any future decision to enable controlled simulator approvals must explicitly add `--test-harness-approvals` to the plan, changing its reviewed hash; it still does not become actual human approval. Provider credentials and the random proposer token are process-local server environment values, never browser inputs. Only the fixed OpenAI Responses endpoint is allowed; no public gateway is added. Requests run serially; automatic model/provider/submission retries are disallowed.

## Acceptance and remaining limits

All requested offline infrastructure, failure tests, paired Go measurements, reporting and guarded live-plan integration are implemented and verified. The acceptance boundary is offline infrastructure completion, not real-model quality measured. The optional paid pilot is deliberately unfinished pending explicit review. No requirement or model/price choice was guessed.

The dataset is small and uses one seed, one decision opportunity and test-tagged initial variants. Simulator time is frozen while the investigator/test operator runs, so latency is not converted to operational delay. Real model instruction following, provider protocol behavior, usage and billing are still unmeasured; local transport tests do not establish those empirical results. No statistical superiority, hardware transfer, continuous controller benefit, real human review or obstruction repair is demonstrated. Cost correctness depends on accurate and still-current reviewed provider facts; plan hashes are acknowledgements rather than payment controls. Node's built-in TypeScript transformation is experimental and can emit a warning. The existing CRLF native golden failures remain visible. No PUB or later work was started. Stop here for review.
