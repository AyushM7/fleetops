# P5-00 evaluation foundation

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
node ../agent/node_modules/vitest/vitest.mjs run
node ../agent/node_modules/typescript/bin/tsc --noEmit
```

No install or dependency change is required for these fallback commands. The
benchmark uses Node's built-in TypeScript stripping; `scripts/run.mjs` maps
relative NodeNext `.js` source imports to their existing `.ts` sources. It does
not compile or execute a model. Agent contracts/fixtures require the existing
agent installation. Type checking remains a separate required check.

The CLI exits 0 only for a nonempty benchmark with every case passing, otherwise
1. Runtime startup errors also exit nonzero. JSON has no timing fields and keeps
input order, making repeated runs comparable byte for byte.

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
conservatively rather than being removed from the denominator. Valid malformed
envelopes may still pass the isolated command-name check; the case still fails.
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

P5-01 can add a broader reviewed scenario dataset with fixed input snapshots,
provenance, explicit incident/action labels, and any acceptable alternatives
specified before running the agent. Scenarios making behavioral claims must use
actual deterministic Go execution and independently observed submission,
execution, and job-state evidence; add that verification only where needed.
Live model comparison requires explicit authorization and is not implemented
or run here. No P5-01 dataset work is included in P5-00.
