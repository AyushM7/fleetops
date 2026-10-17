# Evaluation results

## P5-01 — scenario dataset (2026-10-17)

Implemented 16 source-reviewed cases: 12 fleet-decision contexts and four
synthetic bad-output probes. The adapter uses the existing deterministic
observer policy, not a model. Expected incident/action labels and exact scoring
outcomes are declared separately in `evals/src/dataset.ts`; the adapter receives
only a detached snapshot and ID. Source citations and the case inventory are in
[the evaluation guide](../evals/README.md#p5-01-dataset).

Three cases mirror exact existing tested agent fixtures. The remaining cases
are explicitly synthetic reasoning-only adaptations/probes grounded in the
existing detector, command eligibility tests and reasoning contracts. No new Go
capture, behavioral experiment, independent human dataset signoff, or real LLM
measurement is claimed. Phase 4 delivery evidence is historical and was not
counted as a new result.

### Measured category results

| Category | Fleet decisions passed / measured | Synthetic probes rejected / injected | Unexpected outcomes |
| --- | --- | --- | --- |
| traffic | 2 / 2 | — | 0 |
| offline | 1 / 1 | — | 0 |
| battery | 2 / 2 | — | 0 |
| healthy | 1 / 1 | — | 0 |
| multiple | 2 / 2 | — | 0 |
| ineligible | 2 / 2 | — | 0 |
| charging-availability | 1 / 1 | — | 0 |
| runtime | 1 / 1 | — | 0 |
| references | — | 1 / 1 | 0 |
| evidence | — | 2 / 2 | 0 |
| unsupported | — | 1 / 1 | 0 |
| Total | 12 / 12 | 4 / 4 | 0 |

The raw P5-00 scorer reports **12 passed and 4 unsuccessful**. The four bad
outputs retain their observed statuses: stale reference `failed`, fabricated
candidate `failed`, missing evidence `malformed`, unsupported MoveRobot
`malformed`. Matching those exact expected rejections validates the probe;
it does not convert them into successful recommendations or an agent accuracy
percentage. There were no skipped cases or invalid dataset definitions in the
measured run. Submission, execution and delivery remain `not_measured` in all 16.

| Raw scoring check (includes probes) | Pass | Fail | Not applicable |
| --- | --- | --- | --- |
| Incident references | 13 | 3 | 0 |
| Structured evidence grounding | 12 | 4 | 0 |
| Recommendation identity validity | 14 | 2 | 0 |
| Supported command name | 15 | 1 | 0 |
| Operator attention | 10 | 3 | 3 |
| Appropriate abstention | 9 | 2 | 5 |

Command names and recommendation validity do not measure evidence validity:
the fabricated candidate still has the expected action identity but fails
evidence grounding. Missing/malformed outputs conservatively fail unmeasured
checks under the existing scorer; those failures are not additional observed
commands. All raw metric counts by primary category are in the JSON report.

### Commands and actual verification

Run from `evals/` with existing Node 24.21.0:

```powershell
New-Item -ItemType Directory -Force dist/test-tmp | Out-Null
$env:TEMP = (Resolve-Path dist/test-tmp).Path
$env:TMP = $env:TEMP
node ../agent/node_modules/vitest/vitest.mjs run --no-file-parallelism
node ../agent/node_modules/typescript/bin/tsc --noEmit
npm run benchmark --silent > dist/p5-01-results.json
```

| Command/check | Actual output/result | Exit |
| --- | --- | --- |
| Full relevant suite using the fallback command above | `Test Files 2 passed (2)`; `Tests 136 passed (136)` | 0 |
| Typecheck using existing agent compiler | No diagnostics | 0 |
| `npm run benchmark --silent > dist/p5-01-results.json` | 12/12 fleet decisions passed, 4/4 probes rejected exactly, zero expectation mismatches | 0 |
| Repeatability in tests | Entire dataset reports and two full CLI stdout streams byte-identical; both CLI exits 0 | 0 |
| Existing P5-00 coverage | All 32 original tests pass; two-case CLI test explicitly uses `--foundation` | 0 |
| `git diff --check` | No whitespace errors | 0 |
| Ordinary `npm test` | `'vitest' is not recognized`; existing local Windows shims missing | 1 |
| Ordinary `npm run typecheck` | `'tsc' is not recognized`; existing local Windows shims missing | 1 |

An initial fallback run failed both suites during imports with sandbox TEMP SSR
file `ENOENT`; a serial retry passed the 100 dataset tests but failed loading
the old suite. Setting TEMP/TMP to `evals/dist/test-tmp` fixed this without
installing anything. Four additional expectation-validation tests were added;
the final full run passed all 136 tests. The JSON artifact
is generated under ignored `evals/dist`; the one-command benchmark also works
without writing a file. Summary is on stderr and JSON alone is on stdout.

Tests exercise every case's correct, incorrect, fabricated, unsupported and
incomplete outputs, all abstention cases, unique IDs, complete metadata,
snapshot/label validity, exact probe checks, invalid-case retention, no fetch,
unchanged inputs, deterministic output, and lifecycle nonmeasurement.

Remaining gaps: no live-model intelligence, prose semantic verification, actual
operator approvals, simulator recovery/delivery measurements, incident history,
multi-candidate alternative ranking, or prompt-injection resistance measurement.
The current schema has no incident-priority output or freeform recovery-request
input, so neither receives invented labels. P5-02 should evaluate actual raw
structured agent outputs against these fixed labels and compare to this offline
baseline, with explicit authorization before any live provider call. No P5-02
work was started, and no simulator, safety, authorization, frontend, dependency,
branch, commit, push, or merge change was made.

## P5-00 — evaluation foundation (2026-10-17)

The offline framework and deterministic double passed both foundation contract
cases: grounded charging in place and low-battery operator attention/abstention.
This is framework validation, not real LLM accuracy or recovery effectiveness.
See [evaluation usage and exact scoring criteria](../evals/README.md).

Actual verification from `evals/`:

| Command | Actual output/result | Exit |
| --- | --- | --- |
| `npm test` | `'vitest' is not recognized`; local Windows shims missing | 1 |
| `npm run typecheck` | `'tsc' is not recognized`; local Windows shims missing | 1 |
| `node node_modules/vitest/vitest.mjs run` | Startup error: missing native rolldown binding | 1 |
| `node node_modules/typescript/bin/tsc --noEmit` | Missing `@typescript/typescript-win32-x64` | 1 |
| `node ../agent/node_modules/vitest/vitest.mjs run` | `Test Files 1 passed (1)`; `Tests 32 passed (32)` | 0 |
| `node ../agent/node_modules/typescript/bin/tsc --noEmit` | No diagnostics | 0 |
| `npm run benchmark --silent` | Two cases passed; zero unsuccessful; JSON stdout and summary stderr | 0 |

The final suite also launches the CLI twice, checks both exit codes are zero,
parses the JSON totals and verifies byte-identical stdout. During implementation,
an initial fallback typecheck exposed third-party Mastra declaration errors and
missing Node ambient types; the eval config now matches the agent's existing
skipLibCheck policy and reuses its installed Node types. No dependencies changed.

Both cases pass incident-reference, structured-evidence, recommendation-validity
and command-allowlist checks. Operator attention and abstention each pass in one
case and are not applicable in the charging case. Submission, execution and job
delivery are all explicitly `not_measured`. No live model/API, Docker, simulator
execution, proposal submission, or simulator mutation occurred.

Remaining limitations: two synthetic cases, fixed deterministic outputs, no prose
semantic grading, no behavioral/recovery measurements, no live-model comparison.
P5-01 will supply broader independently reviewed scenarios; it was not started.
