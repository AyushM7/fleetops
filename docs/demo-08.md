# DEMO-08 — grounded evidence and controlled model evaluation

Implemented locally on 2026-10-17–18, on `phase6-demo08-ai-evidence`, starting from
`8e2c830028eab837ecf92f6812408f612f17df78`. No commit, branch switch, PR, push,
merge or deployment was performed.

**LLM proposes. Go validates. Human approves. Go executes.**

## Architecture and provenance

The dashboard adds an expandable **Evidence & reasoning** section inside the
existing approval context. It uses deterministic, host-authored presentation;
it does not display a new live or recorded LLM response or hidden chain of thought.
The local Mastra adapter remains explicitly labelled deterministic. Healthy and
no-candidate presets do not invoke that adapter at startup.

| Evidence | Actual source and boundary |
| --- | --- |
| Original observation | Existing Go GraphQL resolver `FleetState`, captured by `demoFactory.createScenario` after verified, non-mutating submission. Exact incident classifications, robot telemetry, jobs, stations and admitted candidates come from `fleet.Service.ReadState`. |
| Run ownership | Local-only `/demo/status` includes an immutable evidence envelope with scenario ID, generation, service runtime ID, original observation and exact proposal ID (empty for no-action presets). The existing lifecycle gate serializes observation and replacement. |
| Recommendation source / host checks | The fixed trusted factory invokes `agent/scripts/phase4-demo-reasoning.mjs` for actionable presets. That script performs Mastra local-double generation, `validateReasoning`, exact host-statement checking, fresh-facts verification and proposal submission. The factory independently verifies committed pending proposal/create audit and no simulator mutation. The UI summarizes this pipeline; it does not claim to have run the validator itself. |
| Current submission/decision/execution | Existing authenticated pending-proposal reads, mutation acknowledgements and stored-status reads in `ApprovalInbox`/`proposals.ts`. An original candidate is not future approval-time authorization. |
| Later effects | Current Go status snapshot, with its own tick. Job `done`, charging status and battery readings are observations. Execution success and subsequent simulation effects are separate. No causal effect, predicted delivery, route, confidence or monetary value is inferred. |
| History | Existing launcher captures and original `reasoning.json` under ignored `evals/dist/p405/history/`. Retired evidence does not authorize the replacement run. Browser receipts remain tab-local; a reload after a decision may require stored-status reconciliation rather than reconstructing that receipt. |

This is an additive read-only field in the test-only local status DTO, not a
production schema or agent tool change. No new trustworthy production source
for arbitrary model provenance, operator history, diagnosis or causal impact was
invented. Normal bearer-mode monitoring does not claim demo provenance.

The browser checks envelope generation/scenario/runtime format, original snapshot
shape and candidate telemetry relationships. Proposal evidence must match the
original exact proposal and action targets. Missing or mismatched evidence,
poison, failed status transport and old generations show an unavailable/stale
message. Selecting/restarting clears the current fleet, explanation and receipts
while replacement is pending. Same-run older evidence remains explicitly labelled
historical when the current tick advances. No re-analysis or auto-submission occurs.

The disclosure distinguishes seven stages: Go observation/classification;
recommendation source; host grounding/policy; proposal/current Go validation;
human decision; recorded execution; observed simulator outcome. Existing controls,
expiry/pending checks, uncertain receipts and Go revalidation remain responsible
for actions. Expanding evidence is read-only. Approval buttons remain before
the disclosure, so expanding it does not push the decision controls behind it.

## Four actual scenarios

| Scenario | Original Go evidence | Explanation and verification |
| --- | --- | --- |
| `blocked-reassignment` | Tick 42, R1 blocked with assigned/uncollected J1, R3 idle at (4,3), 98% battery; Go candidate exists. | Exact J1 R1→R3 intervention; the obstruction remains. The candidate's battery requirement is shown alongside the separate 30% floor. Approval transfers ownership; later ticks confirm job progression separately. |
| `routine-charging` | Tick 1000, idle R1 at S1 (0,0), 50%, station free. | SetCharging charges in place; no navigation or offline repair. Browser test approval left battery at 50% at tick 1000; one ordinary Step observed charging at 52% at 1001. |
| `healthy-operations` | Tick 0, six robots, eight jobs, no incidents or proposal. | Brief no-recovery explanation. One Step observed six moving robots at tick 1, with no approval controls. Movement is simulator activity. |
| `blocked-no-candidate` | Tick 42, blocked R1, offline R2, idle R3/R4/R5 each at 15%; zero admitted transfers. | Operator attention, no fabricated approval/repair. Step 43 observed autonomous R3 movement/14% and station reservation, while the original no-candidate evidence remained scoped to tick 42. Eligibility can change later. |

Browser approvals in acceptance are automated test-operator actions against an
isolated local simulation. They are not evidence of a real human's acceptance or
of model authority to approve. PostgreSQL tests separately exercise persisted
approval, execution/audit, rejection, expiry and poisoned-runtime behavior.

## Model experiment and independent scoring

`agent/src/evaluation-model.ts` accepts an evaluation-only prompt condition.
Assisted uses the production prompt unchanged. Reduced-hint removes **only** the
direct `supportedAction` field. Production `reasoningPrompt`, instructions,
`validateReasoning`, bounded statements and tools are unchanged. Both conditions
still contain the complete validated fleet, incident references, action
definitions, Go-admitted candidates and host statement catalog. These can reveal
the action; reduced-hint is not independent planning. The shared production
instructions still refer to supplied candidates, another limitation of this
minimal variant.

`backend/fleet/demo08_evidence_test.go` exports the four actual DEMO-09 factories
through the existing Go resolver without proposals, commands, ticks after
observation or database access. Each export records a service runtime ID, tick,
source and SHA256 of Go's compact snapshot JSON. `evals/src/demo08.ts` verifies
those hashes and maps independently reviewed action labels to the new inputs.
Incident truth comes from Go's classifier. Source strings/hashes are reviewable
provenance, not cryptographic proof that an arbitrary supplied file is authentic.
The schema's `existing-tested-fixture` category here denotes the tested Go preset;
these four snapshots are not the P5 synthetic reasoning-only variants.

CLI additions reuse P5: `--demo08-observations PATH`, `--prompt-variant assisted`,
`reduced-hint` or `both`. Both conditions count separately toward request/cost
ceilings. Labels/scoring metadata never enter generation. The existing scorer
retains schema, incident-reference, exact evidence/statement, action identity,
operator-attention and appropriate-abstention checks plus independent host
acceptance. Reports add condition/hint, source/runtime/hash, tick, expected action
and condition totals. Completed incorrect outputs remain distinct from generation
failures. Live responses can record measured elapsed milliseconds and only numeric
usage exposed by Mastra response metadata; unavailable tokens are null. Provider
errors, headers and credentials are not serialized. Rendering/evaluation cannot
submit, approve, tick, switch scenarios or execute commands.

Actual offline result: **8/8** observations passed, **4/4 assisted** and **4/4
reduced-hint**, zero generation failures. This is a deterministic Mastra model
double, not LLM accuracy. See [the saved comparison](evidence/demo-08/offline-comparison.json)
and [actual Go observations](evidence/demo-08/observations.json). Negative tests
retain stale-reference, unsupported rationale and poisoned-runtime rejection;
they are offline probes, not measured live prompt-injection resistance.

The historical [P5 live report](evidence/p5-04-live-full.json) is untouched.
Its 11/12 outcome remains assisted-contract compliance on the old selected
fixtures; one poisoned-runtime wrong action was host-rejected. The existing
replay still checks every saved score/host decision and the archive digest.
Neither that history nor the new double runs establish general reliability,
Go safety admission, actual approval, execution or recovery effectiveness.
The separate P5-03 Go recovery benchmarks retain that operational scope.

## Real OpenAI availability and budget outcome

`OPENAI_API_KEY` was present in the server environment. A single read-only
`GET /v1/models/gpt-6-luna` returned **HTTP 403**. Account availability could not
be verified, and no fallback was attempted. **Paid generation requests made: 0.**
No new assisted/reduced-hint real-model outcomes, latency or usage were measured.
The sanitized [availability record](evidence/demo-08/availability.json) retains
no credential, raw provider error or response header.

Official [GPT-6 Luna documentation](https://developers.openai.com/api/docs/models/gpt-6-luna)
lists the model, a 1,050,000-token context and 128,000 maximum output tokens.
The [pricing page](https://developers.openai.com/api/docs/pricing) lists
Standard short-context input/output at $0.10/$0.50 per million, long-context
input/output at $0.20/$0.75, and long-context cache writes at $0.25.
The [reasoning guide](https://developers.openai.com/api/docs/guides/reasoning)
states that the output ceiling includes reasoning, visible output and formatting
tokens, and incomplete generations can still incur charges.

The new OpenAI evaluation configuration explicitly uses the installed Mastra
Responses adapter, fixed `https://api.openai.com/v1`, default service tier and
`store:false`. A mocked transport test checks its actual `max_output_tokens`
request and exactly one HTTP request even on a retryable 503. Both agent/model
retry limits are zero, and evaluation explicitly disables Mastra's default
stability error processors, whose separate retry policy otherwise retried twice.
The regression test first observed three mock requests and failed; setting the
generation retry limit alone still failed. Disabling the independent processors
fixed the test. No real generation or cost occurred during this discovery.
This fix is evaluation-only; no production prompt, validator or tool changed.
No new SDK/dependency was added.

The [review](evidence/demo-08/cost-review.json) conservatively bounds all input by
the documented full context, uses the highest applicable Standard long-context
input/cache-write rate ($0.25/M), output $0.75/M, and a chosen request output cap
of 6,000 tokens. This cap is a run setting, not an invented model maximum.

| Actual plan command | Outcome |
| --- | --- |
| Two selected cases × both conditions, four requests, 6,000 output cap, $1 budget, reviewed SHA256, `--plan` | Exit 1: `Estimated maximum exceeds spending ceiling or budget missing.` Calculated conservative maximum **$1.068**, so no generation. |
| One selected case × both conditions, two requests, same caps/review/budget, `--plan` | Exit 0; reviewed maximum **$0.534**. [Saved dry plan](evidence/demo-08/two-request-plan.json). Not executed because availability was refused. This reduced sample would cover only blocked reassignment. |

These are reviewed estimates, not actual spending or provider-enforced budgets.
No generation charge was incurred by this work's zero generation requests;
provider billing/account charges were not independently inspected. Usage and
verified billed cost are unavailable. No additional paid request was sent to
improve a score. A $1 ceiling, explicit live/provider/model flags, current review,
matching SHA256, request cap, output cap, one step, zero application retries and
30-second per-generation timeout remain mandatory. Live mode is blocked in CI.
Anonymous dashboard visitors have no model-call endpoint.

To plan safely after the owner resolves model access, recheck official pricing
and token semantics, renew the time-limited review if necessary, then run from the repository root:

```powershell
Push-Location evals
try {
  $review = '../docs/evidence/demo-08/cost-review.json'
  $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $review).Hash.ToLowerInvariant()
  npm run benchmark --silent -- --model-eval --live --provider openai --model gpt-6-luna --demo08-observations ../docs/evidence/demo-08/observations.json --scenario-limit 1 --prompt-variant both --max-requests 2 --max-output-tokens 6000 --cost-review $review --approve-cost-sha256 $hash --budget-usd 1 --plan
} finally { Pop-Location }
```

Do not execute a refused or over-budget plan. An owner-approved execution uses
the identical reviewed options without `--plan`, only after model access is
verified. Keep the key in the process environment supplied by an existing secret
manager; never put it in source, flags, reviews or browser configuration. Raw new
responses must be sanitized and explicitly reviewed before any future commit.

## Local launch

With Docker, Node 24.21.0 and existing locked dependencies, from the repository root:

```powershell
$env:Path = 'C:\Users\Ayush\go\pkg\mod\golang.org\toolchain@v0.0.1-go1.27.1.windows-amd64\bin;' + $env:Path
$env:GOTOOLCHAIN = 'local'
$env:GOPROXY = 'off'
$env:GOSUMDB = 'off'
pwsh -NoProfile -File evals/phase4/start-demo.ps1 -LocalDemo -Submit
```

Open the READY URL. No provider key or paid inference is used by this launcher.
Do not rebuild production assets during the session: the gateway retains startup
HTML. Stop gracefully with `New-Item evals/dist/p405/stop -ItemType File -Force`.
No Docker/terminal-free public hosting or multi-visitor isolation is added here.

## Verification and screenshot evidence

All images are genuine, unedited browser captures. The initial
before captures had viewport-resize defects; replacements use untouched frontend
source archived from the starting HEAD, served through the real Go/PostgreSQL
demo in an ignored temporary checkout. This is a reconstructed before baseline,
not a screenshot of a mock or a modified DOM.

Final relevant results (actual output, not inferred acceptance):

| Check | Result |
| --- | --- |
| Agent serial unit suite, excluding the separate backend integration entry | **9 files, 103 tests passed**. Final focused evaluation transport/prompt tests also **3/3 passed** after the provider settings change. |
| Agent typecheck/build | Exit 0. |
| Evaluation suite, final source-directory run | **9 files, 247 tests passed**; typecheck exit 0. Includes new saved comparison replay, both prompt conditions, actual preset hashes, poisoned/stale/prose negatives, current budget proof, missing-key/hash/pricing/request gates and historical P5 digest/scoring replay. |
| Frontend suite | **11 files, 73 tests passed**; typecheck and production build exit 0; **26 modules transformed**. |
| Tagged Go exporter | PASS; fleet **3.092s**, four actual preset observations exported. |
| Native tagged safety/proposal/fleet | PASS; safety **2.922s**, proposal **3.094s**, fleet **3.694s**. Database-dependent coverage is the separate row below. |
| Real PostgreSQL, gateway, controller, scenario, restart and dashboard HTTP/React integration | Final serial suite PASS; fleet **39.760s**. New assertions verify immutable original evidence after four ticks and fresh generation/runtime/proposal identities after every restart. |
| Cached Linux Go, network-disabled controller/gateway race checks, database tests excluded | PASS; fleet **1.408s**, no race diagnostics. PostgreSQL tests run separately on Windows. |
| Native `go test ./... -count=1 -timeout 180s` | Exit 1: **only TestGoldenSnapshot and TestWarehouseGoldenSnapshot** fail with the previously documented Windows CRLF/LF byte mismatch. All other packages pass. No golden was edited or normalized. |
| Original-asset baseline gateway / final-asset browser session | PASS; fleet **100.094s** / **137.053s**. Earlier interactive launcher closes cleanly with PASS, fleet **1115.594s**; isolated container and temporary checkout cleaned by launcher. Session file and run lock absent. |
| `git diff --check` | Exit 0; Git prints only line-ending conversion notices for the changed Go harness files. |

Exact JavaScript checks, each from its package directory:

```powershell
# agent/
node node_modules/vitest/vitest.mjs run --pool=threads --no-file-parallelism --exclude src/backend.integration.test.ts --testTimeout=60000
npm run typecheck
npm run build
# final evaluation-specific agent check
node node_modules/vitest/vitest.mjs run src/evaluation-model.test.ts --pool=threads --no-file-parallelism --testTimeout=60000

# evals/ (use a writable local TEMP/TMP, as in prior P5 verification)
$env:TEMP = (Resolve-Path dist/demo08-temp).Path
$env:TMP = $env:TEMP
node node_modules/vitest/vitest.mjs run --dir src --pool=threads --no-file-parallelism --testTimeout=60000
npm run typecheck
npm run benchmark --silent -- --model-eval --demo08-observations ../docs/evidence/demo-08/observations.json --prompt-variant both --max-requests 8

# web/
node node_modules/vitest/vitest.mjs run --pool=threads --no-file-parallelism --testTimeout=60000
npm run typecheck
npm run build
```

The Go exporter was run from `backend/` with the cached Go 1.27.1 executable and
`FLEETOPS_DEMO08_EXPORT` set to the absolute observations artifact path:

```text
go test -tags fleetops_test ./fleet -run '^TestDemo08ExportObservations$' -count=1 -v
go test -tags fleetops_test ./safety ./proposal ./fleet -count=1 -timeout 180s
go test ./... -count=1 -timeout 180s
```

The PostgreSQL command ran in the launcher's temporary backend copy, using its
isolated test database, `FLEETOPS_P405_ROOT` set to this repository and
`FLEETOPS_P404_WEB_ROOT` set to this repository's `web/`. Its frontend contract
bundle was built first with `node node_modules/vite/bin/vite.js build --ssr
scripts/contract-entry.ts --outDir dist/contract` from `web/`:

```text
go test -p 1 -parallel 1 -tags fleetops_test ./fleet -run '^(TestDemo.*|TestDashboardHTTPIntegration)$' -count=1 -v -timeout 180s
```

The race command used the cached `golang:1.27.1` image with network disabled,
read-only temporary backend and module-cache mounts and a writable ignored Go
build cache. No downloads or database credentials entered that container:

```text
go test -p 1 -parallel 1 -race -tags fleetops_test ./fleet -run '^(TestDemoController.*|TestDemoControls.*|TestDemoGatewaySecurity)$' -skip 'Postgres|Integration' -count=1 -timeout 180s
```

Failed attempts are not hidden: the first sandbox Docker launch was denied pipe
access and the approved retry succeeded. Initial evaluation execution encountered
the existing Windows temporary SSR ENOENT problem and wrong working-directory
CLI launches; package-directory execution with writable TEMP/TMP and the serial
thread pool passed. New tests caught the runtime ID's actual `P` plus 32-hex
format and missing recipient telemetry; both were corrected. Additional dashboard
HTTP checks initially lacked their script-root environment and SSR bundle. One
intermediate integration run also stopped a blocked replacement; its underlying
cause was not established. The final serial suite passed all replacements and
dashboard checks. An initial network-disabled race run mistakenly included
PostgreSQL tests and failed for missing database configuration; the corrected
unit scope above passed. A late broad evaluation search discovered copied HEAD
frontend tests in the ignored screenshot reconstruction under the wrong Vite
configuration; the temporary source copy was removed, histories retained, and
the final source-directory suite passed. No failed call or malformed output was
counted as a passing model trial.

The [screenshot manifest](evidence/demo-08/captures.json) records all 15 JPEGs,
their actual dimensions, bytes and SHA256. Requested CSS viewports were
1280×720 and 390×844; JPEG captures exclude browser/scrollbar regions. Read-only
DOM measurements found desktop map/context widths **843.2/421.6px**, preserving
67/33. Client/scroll widths were equal at **1265px desktop / 375px mobile**.
There were zero console warning/error entries before deliberate transport loss.
[Browser observations](evidence/demo-08/browser-checks.json) preserve actual
rendered context, counts, receipts, layout readings and the final disabled approval.

| State | Desktop | Mobile |
| --- | --- | --- |
| Before blocked (untouched HEAD build) | [capture](evidence/demo-08/before-blocked-desktop.jpg) | [capture](evidence/demo-08/before-blocked-mobile.jpg) |
| Before healthy (untouched HEAD build) | [capture](evidence/demo-08/before-healthy-desktop.jpg) | [capture](evidence/demo-08/before-healthy-mobile.jpg) |
| After blocked | [capture](evidence/demo-08/after-blocked-desktop.jpg) | [capture](evidence/demo-08/after-blocked-mobile.jpg) |
| After charging | [capture](evidence/demo-08/after-charging-desktop.jpg) | [capture](evidence/demo-08/after-charging-mobile.jpg) |
| After healthy | [capture](evidence/demo-08/after-healthy-desktop.jpg) | [capture](evidence/demo-08/after-healthy-mobile.jpg) |
| After no eligible recovery | [capture](evidence/demo-08/after-no-candidate-desktop.jpg) | [capture](evidence/demo-08/after-no-candidate-mobile.jpg) |

Additional real states: [battery 52% at tick 1001](evidence/demo-08/charging-tick-1001-desktop.jpg),
[J1 done at tick 45](evidence/demo-08/blocked-tick-45-desktop.jpg),
[transport loss](evidence/demo-08/transport-loss-desktop.jpg). Transport loss
removed the expanded reasoning, marked retained telemetry stale and disabled
Approve, Reject, restart and simulation controls. Final browser acceptance also
verified selection while old evidence was expanded, fresh tick 42/no old receipt
after restart, and rejection with committed audit and no execution.

## Final replacement-failure review (2026-10-18)

**Merge recommendation: safe to merge the local DEMO-08 scope, with the historical
failure's exact infrastructure cause still unproven.** No runtime fix was justified
by this review. No paid API request, commit, branch change, push or merge was made.

The intermediate failure occurred selecting `blocked-reassignment` in
`TestDemoScenariosPostgres`: the response was the controller's generic 503
`restart failed; old runtime stopped` message. A later `scenario unavailable`
message followed the same failed replacement; it is not evidence of a second
independent defect. The failing subtest took 8.73 seconds. This does **not** prove
that the 45-second proposer or 60-second replacement deadline expired.

| Candidate cause | Evidence and conclusion |
| --- | --- |
| Transient test infrastructure | Most likely, with limited confidence in the specific mechanism. The same scenario passed before and after the failure, and all review repetitions passed. Heavy native/Linux compilation overlapped the original run. Replacement depends on PostgreSQL queries (default 5-second bounds), a Node subprocess, loopback HTTP and synchronized evidence-file writes. No retained error identifies which dependency failed; a timeout or resource-contention explanation remains an inference. |
| Reproducible scenario lifecycle defect | Not reproduced. Ten complete PostgreSQL runs passed all four selections, selected restarts, fresh identities, approval gates, progress and history assertions. A final run with unmodified harness sources also passed. Failure-path tests confirm that capture/factory/identity/scenario/record failures stop access to the old runtime. |
| Race or stale generation | No supporting evidence. The observed message is from `replaceScenario`, after the stale-generation check; stale requests receive 409 when recording succeeds. This particular integration test has no parallel subtests or background ticker: it invokes `pulse` synchronously. The shared lifecycle mutex drains active operations. One hundred controller-suite repetitions under the Go race detector passed, including concurrent drain and restart cases. This is bounded testing, not proof that every race is impossible. |
| Test setup or teardown | The missing dashboard script root/bundle caused separate, explicit dashboard-contract failures in the surrounding commands. The scenario proposer uses the agent build rather than that dashboard bundle; those failures do not explain its 503. Pool/schema and evidence-directory cleanup occurs after the parent test, and controller close precedes that cleanup. No premature cleanup was found. Cleanup did remove the failed test's temporary evidence, limiting retrospective diagnosis. |

The historical intermediate output was visible in the original tool result but
was not saved as a standalone log. The controller deliberately returns a generic
failure, its integration-test lifecycle recorder is a no-op, and the proposer
discards subprocess stderr on error. The schema is dropped and `t.TempDir()` is
removed at test completion. Consequently, the exact failed replacement stage and
underlying OS/database/subprocess error cannot now be recovered. The surrounding
saved logs `evals/dist/demo08-go-integration.log` and `demo08-go-final.log` both
show the scenario suite passing; the former still correctly reports its separate
dashboard setup failure. No original failure was relabelled as a pass.

Review verification used existing dependencies and a fresh disposable loopback
PostgreSQL container. Provider keys were cleared in the test shell. The existing
deterministic proposer restricts fetch to its exact loopback GraphQL endpoint.

| Check | Actual result |
| --- | --- |
| `TestDemoScenariosPostgres`, `-count=10` | PASS; 10 complete suites, 40 preset subtests, zero failures; `ok .../backend/fleet 90.847s`. Temporary ignored copies added error-only stage/exit classification, without changing checks, expectations, success behavior or timeouts. |
| `TestDemoController.*`, Linux `-race -count=100` | PASS; `ok .../backend/fleet 18.384s`; container network disabled. |
| Unmodified `TestDemoScenariosPostgres`, `TestDemoRestartIntegration`, `TestDemoController.*` | PASS; nine top-level tests, zero failures; `ok .../backend/fleet 20.290s`. Diagnostic copies were replaced with exact repository sources before this run. |
| Full frontend Vitest suite | PASS; 11 files, 77 tests. Includes four added preset-specific deterministic-label and generation-retirement checks, using the existing real Go observation fixtures. |
| Frontend `npm run typecheck` | PASS; `tsc --noEmit`, exit 0. |

Review logs remain ignored at `evals/dist/demo08-review/repeat-scenarios.jsonl`
and `final-unmodified.log`. No safety check, expected outcome or timeout was
relaxed. Review changes are limited to the four frontend regression cases in
`web/src/Evidence.test.tsx` and this documentation.

For all four presets, the evidence panel labels itself `Deterministic
presentation · No live LLM`. Actionable presets identify the deterministic local
Mastra adapter and host-authored explanation; no-action presets state that no
adapter or model was invoked. Parsing and rendering reject a previous generation
even when scenario/proposal IDs are reused. Real PostgreSQL scenario tests also
check the current generation/scenario/original observation on each selection and
fresh generation/runtime/proposal identities on restart. App replacement clears
the displayed evidence and receipts, and stopped status polling cannot emit an
old response. Original observations remain explicitly historical after later
ticks. These paths do not present deterministic analysis as live OpenAI inference.

## Safety compatibility and limitations

All seven frozen rules remain intact. `schema.graphql`, `docs/safety-rules.md`,
agent tool signatures, Go routing/scheduling/battery code and golden fixtures are
unchanged. No new dependency, credential persistence, public permission,
runtime model tool, automatic approval or deployment was added. DEMO-07,
DEMO-10 and multi-agent orchestration are outside this ticket.

Evidence is local/read-only, with original and current observations distinguished;
it cannot guarantee approval preflight, repairs, delivery, future eligibility or
causal improvement. There is no unrestricted natural-language explanation layer.
Only bounded existing model statements pass production host validation.
No real-model comparison was possible because access verification failed. The
offline results are eight contract observations, not general reliability evidence.
Browser acceptance is sampled Chromium desktop/mobile viewport testing, not
physical-device, cross-browser, screen-reader or full accessibility certification.

## Files changed; unfinished or guessed

Source and tests (19 files):

- `agent/src/evaluation-model.ts`, `agent/src/evaluation-model.test.ts`
- `backend/fleet/demo08_evidence_test.go`
- `evals/phase4/controller_test.go`, `evals/phase4/controls_test.go`, `evals/phase4/scenario_test.go`, `evals/phase4/scenarios_integration_test.go`
- `evals/src/model-cli.ts`, `evals/src/model-controls.ts`, `evals/src/model-runner.ts`, `evals/src/demo08.ts`, `evals/src/demo08.test.ts`
- `web/src/App.tsx`, `web/src/ApprovalInbox.tsx`, `web/src/Evidence.tsx`, `web/src/Evidence.test.tsx`, `web/src/fleet.ts`, `web/src/restart.tsx`, `web/src/styles.css`

Documentation/artifacts: this file; the 15 JPEGs enumerated in `captures.json`;
`docs/evidence/demo-08/observations.json`, `offline-comparison.json`,
`availability.json`, `cost-review.json`, `two-request-plan.json`,
`browser-checks.json` and `captures.json` (all within that same evidence directory).
The saved JSON/JPEG artifacts were reviewed for this ticket; no commit was made.
Existing locked evaluation dependencies were restored with `npm ci`; manifests
and lockfiles did not change. Full local harness logs/history remain ignored under
`evals/dist/`.

Unfinished: paid model evaluation remains blocked by HTTP 403 and the original
four-request conservative plan's over-budget estimate. No new real-model score,
token consumption or verified provider charge is available. The two native
golden checks remain the documented line-ending failures. The intermediate
replacement failure most likely involved transient test infrastructure, but its
exact failed stage and mechanism remain unproven; see the final review above.
Repeated regressions and earlier browser replacements passed. No operational
requirement was guessed; missing current evidence fails closed.
