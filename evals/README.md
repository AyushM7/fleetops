# FleetOps offline evaluations — P5-00/P5-01

This is an offline contract benchmark, not a measurement of LLM intelligence or
recovery effectiveness. It has no model, GraphQL client, proposal tool, simulator
Driver/Commander, credentials, clock, randomness, or network transport.

## Run

From `evals/`, using the project's Node 24.21.0:

```powershell
npm run benchmark --silent
# Machine JSON only on stdout; concise human summary on stderr:
npm run benchmark --silent > results.json
npm test
npm run typecheck
```

The current checkout's evals node_modules was copied without Windows command
shims/native bindings. Its ordinary test/typecheck commands fail before running.
The already installed agent tools are the same declared versions and work:

```powershell
New-Item -ItemType Directory -Force dist/test-tmp | Out-Null
$env:TEMP = (Resolve-Path dist/test-tmp).Path
$env:TMP = $env:TEMP
node ../agent/node_modules/vitest/vitest.mjs run --no-file-parallelism
node ../agent/node_modules/typescript/bin/tsc --noEmit
```

No install or dependency change is required for these fallback commands. The
benchmark uses Node's built-in TypeScript stripping; `scripts/run.mjs` maps
relative NodeNext `.js` source imports to their existing `.ts` sources. It does
not compile or execute a model. Agent contracts/fixtures require the existing
agent installation. Type checking remains a separate required check.

The dataset CLI exits 0 only for a nonempty benchmark whose complete declared
scoring expectations match. Injected bad outputs must fail the scorer to match
their expectations. They never count as successful fleet decisions. Any unexpected
result, invalid case, missing output, or metadata omission exits 1. Runtime
startup errors also exit nonzero. JSON has no timing fields and keeps
input order, making repeated runs comparable byte for byte.

`npm run benchmark --silent -- --foundation` retains the two-case P5-00 benchmark
and its original report format and exit behavior. The workspace TEMP/TMP settings
above also avoid observed Vitest ENOENT errors for sandbox temporary SSR files.

## Architecture and criteria

`src/index.ts` defines typed case inputs, independently authored expected labels,
untrusted actual reasoning, per-case scores, and aggregate counts. The runner
passes only a detached fleet snapshot and ID to an offline adapter. It never
passes expected labels. Output is parsed with the existing strict Phase 4
reasoning schema. Existing snapshot validation rejects inconsistent or duplicate
facts; it is not used to generate expected recommendations.

`src/benchmark.ts` explicitly declares two expected behaviors independently of
the double's outputs: SetCharging for R1/S1 at 50% battery, and operator attention
with no command for R1's high-severity low-battery incident at tick 42. They reuse
existing agent fixtures and document their source. They are synthetic contract
fixtures, not freshly captured Go observations. Reassignment evidence from the
existing agent recovery fixture is also covered in unit tests.

| Check | Passing definition |
| --- | --- |
| Incident references | Root tick matches; exact expected snapshot references, each once; no omissions, extras, duplicates, or persistent-ID substitutions. |
| Evidence grounding | Root tick, severity and structured interpretation telemetry match supplied facts; action evidence matches the exact robot/station or source/recipient/job/Go-admitted candidate and traffic reference. |
| Recommendation validity | Command, target IDs and purpose exactly match the independently declared expected action, or both are null. |
| Unsupported commands | `proposedAction` is explicitly null or names SetCharging/ReassignUnstartedJob. An allowlisted command can still fail validity or grounding. |
| Operator attention | Every expected incident appears once and has `recommendedResponse: operator_attention`; not applicable without incidents. |
| Appropriate abstention | When expected action is null, output explicitly proposes null with complete references and grounded evidence; otherwise not applicable. |

Each check is pass/fail/not_applicable. A case passes only if the strict schema
and every applicable check pass. Aggregates report counts, not weighted averages
or inferred accuracy. Malformed/missing output and adapter errors fail checks
conservatively rather than being removed from the denominator. Malformed outputs
may still pass the isolated command-name check; the case still fails.
Invalid/ambiguous cases and duplicate case IDs are reported unsuccessful before
invoking the adapter. Adapter exception text is omitted from reports.

Assessment and rationale prose must meet the existing schema, but their meaning
is not graded. Structured fact matching does not prove arbitrary natural-language
claims. Expected action labels are trusted reviewer-authored criteria; do not
derive them from the agent, observeFleet, or an agent grounding validator. Review
them alongside the simulator/detector contracts when adding a case.

## Lifecycle and limits

Proposal submission, command execution, and eventual job delivery have separate
fields. All are `not_measured` in this foundation: reasoning cannot prove any
of them. Output claiming extra execution fields is malformed. Nothing is submitted,
approved, executed, or advanced. Recovery ticks from Phase 4 acceptance reports
are historical evidence and are not imported as new benchmark measurements.

The runner accepts offline adapters only by convention; it does not sandbox
arbitrary caller-supplied JavaScript. The shipped double has no tool or network
capability. Unit tests block fetch and verify no calls, snapshot isolation,
malformed/unsupported outputs, repeated CLI JSON, and lifecycle nonmeasurement.
No Docker, paid API, or Go execution is needed for these structured-output checks.

Scenarios making behavioral claims must use actual deterministic Go execution
and independently observed submission, execution, and job-state evidence; add
that verification only where needed. Live model comparison requires explicit
authorization and is not implemented or run here.

## P5-01 dataset

`src/dataset.ts` contains 16 source-reviewed scenario definitions: 12 fleet
decisions and four synthetic output-integrity probes. Each definition includes
description, primary category, full fleet input, explicit incident references and
action identity (null means abstain), evidence paths, forbidden recommendations,
human-approval requirement, source citations, and exact expected status/checks.
The labels are declared before execution and never computed by observeFleet or
an agent. No independent human dataset signoff is claimed.

`src/dataset-fixtures.ts` mirrors the existing agent reassignment fixture and
builds labeled reasoning-only variants. Three cases reuse exact existing tested
fixtures: eligible recovery, routine charging, and healthy fleet. The other 13
are explicitly labeled `synthetic-reasoning-only`, including four probes reusing
those input contexts. None is labeled a new live Go capture. Source references
include the existing Go reassignment fixture/negative tests, detector thresholds,
agent contracts, and Phase 4 acceptance evidence. The input construction helper
only copies explicitly selected incident labels; it is not an incident detector.

`src/dataset-adapter.ts` exercises the existing deterministic `observeFleet`
policy and projects its recommendation into the strict reasoning contract with
supplied facts. It receives only detached input and ID; it cannot read expected
answers. Its generic assessments are deterministic fixture text, not an LLM.
Probe injection is a separate step after generation. `src/dataset-runner.ts`
reuses the unchanged P5-00 `runEvaluation`/`scoreCase` functions and adds metadata
validation, exact expected-score comparison, and category aggregation.

| Scenario ID | Primary category | Independently expected fleet decision / injected fault |
| --- | --- | --- |
| traffic-eligible | traffic | J1 R1 → R3 recommendation; human approval required |
| traffic-no-candidate | traffic | Attention and abstention; do not invent Go admission |
| offline-unfinished-job | offline | Attention; offline source cannot be reassigned |
| battery-critical-job | battery | Attention at 14%; charging below 30% unsupported |
| battery-routine-charge | battery | Routine SetCharging R1/S1 at 50%; approval required |
| healthy-no-action | healthy | No incidents, no command |
| multiple-abstain | multiple | Report low-battery R1 and offline R2; abstain |
| multiple-reassignment | multiple | Report blocked R1 and offline R2; transfer J1 for approval |
| recipient-below-threshold | ineligible | R3 at 29%, no candidate; abstain |
| job-already-collected | ineligible | J1 in_progress; abstain |
| charging-reserved | charging-availability | S1 reserved by R2; abstain |
| poisoned-runtime | runtime | Do not propose even an otherwise admitted transfer |
| probe-stale-reference | references | Recovery label unchanged; inject old interpretation tick |
| probe-conflicting-candidate | evidence | Recovery label unchanged; fabricate pickupSteps |
| probe-incomplete-evidence | evidence | Charging label unchanged; omit action evidence |
| probe-unsupported-recovery | unsupported | Abstention label unchanged; inject MoveRobot |

Multiple incidents must all be reported; the current contract defines no severity
ranking or multi-action priority order. A single admitted recovery is selected
where available. No new priority ground truth is invented. Freeform requests to
repair or move robots are not part of the fleet input contract; unsupported
recovery is therefore tested as a synthetic output-command probe.

The dataset JSON contains the raw P5-00 evaluation, per-scenario observed and
expected scoring, metadata issues, provenance, category counts, and all six
metric counts by category. A failed recommendation/evidence/reference check and
an unsupported command-name check remain distinct. Missing/malformed cases
retain P5-00's conservative failed checks; these are not proof that a named
unsupported command was observed. Not-applicable metrics are explicit, never
converted to passes. Invalid cases stay in the counts. Probe categories with
zero decision cases have no decision accuracy measurement.

The raw scorer is expected to show 12 passing outputs and four unsuccessful
outputs for the current deterministic adapter. Dataset validation separately
requires the four unsuccessful outputs to have their exact predefined rejection
checks. A missing output or arbitrary rejection does not satisfy a probe. See
`docs/eval-results.md` for actual measured results rather than inferring a run
from these declared expectations.

Coverage gaps: no new simulator executions, recovered delivery measurements,
incident history, ambiguous multi-candidate alternatives, natural-language
claim grading, input prompt-injection evaluation, or real LLM decisions. Approval
is a required boundary in labels/schema; an actual operator flow is not measured.
P5-02 should next compare actual structured agent/model outputs against these
frozen labels, preserve raw outputs before host rejection, and check factual and
unsupported-command failure rates against the deterministic baseline. Any live
provider run needs explicit authorization. No P5-02 implementation is included.
