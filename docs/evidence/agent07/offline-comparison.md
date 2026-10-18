# AGENT-07 offline comparison

No live inference was performed. All v2 choices below use scripted local Mastra adapters executing the actual read tools. Go admission, persisted evidence and simulator outcomes use the authenticated production GraphQL/service path against isolated PostgreSQL. These are infrastructure and conditional policy-choice measurements.

Base revision (user supplied): `a61faa1f512f39cd5d15c0c0833a05b6d709dfbc`. Evaluated source digest: `7c81b43e96b513e4bb4830ebb50aa748f2b4c70f64bc6f07f6100b459dc0ada8`. Dataset: `1c1824a719664f967edea577424914f876fdea8e48fb5fda9abfc06df8b9b57f`. Version: agent07-v1 / agent07-fixtures-v1.

Two complete runs produced identical scientific results across 44 independent branches per run. Seed 42; 200 post-decision ticks; 12 investigations per run. The eight comparable cases each have no-intervention, A, B, C and v2 branches; four negative/runtime cases have only v2.

A = first eligible; B = minimum pickup; C = maximum admission headroom. Ties retain first input order. All use the same admitted set.

| Policy | Decision trials | Initially admitted selections | Go pending admissions / attempts | Applicable objective met | Required abstentions | Dominated selected choices | Host summary latency ms |
|---|---:|---:|---:|---:|---:|---:|---|
| first-eligible | 8 | 6/6 | 6/6 | 4/6 | 2/2 | 1/6 | 41–58 (8 reports) |
| minimum-pickup | 8 | 6/6 | 6/6 | 5/6 | 2/2 | 0/6 | 42–96 (8 reports) |
| maximum-headroom | 8 | 6/6 | 6/6 | 4/6 | 2/2 | 1/6 | 36–94 (8 reports) |
| v2-investigation | 12 | 7/7 | 6/7 | 6/6 | 3/3 | 1/7 | 171–592 (12 reports) |

V2 grounding accepted 10/12 attempted trials, including the stale control's initially valid choice. The intentionally false-evidence response was rejected; the unsupported-tool probe failed before execution. They remain in the 12-trial denominator. Actual Go admission is separately 6/7 submission attempts: the stale control is denied. Successful local tool executions: 56/56; complete evidence 11/12; request/tool budget compliance 12/12. Direct scripted adapter failures: 0 across 12 investigations. HTTP model metrics are unavailable because no provider request occurred. Failed-or-unknown trials: 1/12 (the invalid-tool probe); unknown completion reports: 0/12.

Usage: 11/12 complete mocked reports, 1/12 unavailable, subtotal 80 mocked token units. These are neither real tokens nor billing evidence. Estimated and billed cost are unavailable. Reported provider model is unavailable. Latency measures this local harness, including read/completion round trips; original host and Go server durations are retained separately in persisted run records. It does not measure OpenAI latency.

| Scenario / predeclared objective | A recipient / J1 done tick | B | C | Scripted v2 | Go / observed result |
|---|---|---|---|---|---|
| blocked-multiple / compare_tradeoffs | R3 / 45 | R3 / 45 | R3 / 45 | R5 / 48 | pending_admitted; accepted |
| non-first-pickup / minimize_pickup_steps | R3 / 50 | R4 / 45 | R3 / 50 | R4 / 45 | pending_admitted; accepted |
| tradeoff-pickup / minimize_pickup_steps | R3 / 45 | R3 / 45 | R4 / 50 | R3 / 45 | pending_admitted; accepted |
| tradeoff-headroom / preserve_battery_headroom | R3 / 45 | R3 / 45 | R4 / 50 | R4 / 50 | pending_admitted; accepted |
| tradeoff-compare / compare_tradeoffs | R3 / 45 | R3 / 45 | R4 / 50 | R5 / 48 | pending_admitted; accepted |
| tied-pickup / minimize_pickup_steps | R3 / 46 | R3 / 46 | R3 / 46 | R3 / 46 | pending_admitted; accepted |
| no-recipients / compare_tradeoffs | no_candidates / censored or N/A | no_candidates / censored or N/A | no_candidates / censored or N/A | no_candidates / censored or N/A | not_requested; accepted |
| healthy / compare_tradeoffs | no_supported_incident / censored or N/A | no_supported_incident / censored or N/A | no_supported_incident / censored or N/A | no_supported_incident / censored or N/A | not_requested; accepted |
| poisoned / compare_tradeoffs | — | — | — | runtime_poisoned / censored or N/A | not_requested; accepted |
| stale / minimize_pickup_steps | — | — | — | R3 / censored or N/A | denied; accepted |
| false-evidence / minimize_pickup_steps | — | — | — | none / censored or N/A | not_requested; rejected; grounding_rejected |
| invalid-tool / compare_tradeoffs | — | — | — | none / censored or N/A | not_requested; failed; invalid_tool |

All six eligible treatments delivered J1 and their 18 independent baseline branches also delivered. This is 24 successful simulated reassignments across separate worlds; it is not 24 AI recoveries. Intervention failures: 0/24 attempted approved executions. No-intervention branches remain censored at tick 242. No percentage speedup or numeric completion-time saving is assigned against censored controls. No-recipient and healthy cases have no eligible intervention; negative/runtime cases have no paired execution comparison.

The non-first scenario chooses R4, completes at tick 45 versus A's tick 50, and ties minimum-pickup B. This demonstrates measurable conditional choice effects, not measured real-model benefit. The scripted blocked-multiple choice R5 is dominated by R3 and takes three more ticks than A/B/C; it remains admitted under the comparison policy. Headroom choices may take longer routes. Compare-tradeoffs has no unique optimal label; pickup and headroom regret are evaluated only for their declared objectives. Exact signed delivery/battery/blocking/unresolved-job deltas and candidate facts appear in offline-go-report.json.

The harness freezes simulator decision time during host/model/operator latency. Battery values are stored percentage-points; blocked samples are post-tick robot samples. Reassignment does not repair R2's obstruction. One seed and deliberately small fixture variants do not establish statistical superiority. Test approvals are labeled **test-harness approvals; NOT actual human approvals**. Production independent human approval remains required outside these experiments.

The default free synthetic tool report has 39 policy trials (12 investigations and 27 baselines). It has no authentic Go admission or outcome measurements; its synthetic observation IDs cannot authorize a real proposal. The historical Phase-5 assisted 11/12 and existing 17-case synthetic benchmark remain separate and are not pooled here.
