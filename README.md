# FleetOps

AI-assisted warehouse fleet operations, built around a deterministic Go simulator and a human approval boundary.

When a robot blocks an aisle or holds an unfinished job, an explanation alone does not restore progress. FleetOps connects observed conditions to bounded recovery proposals, operator review, validated execution and measurable simulator outcomes.

**LLM proposes. Go validates. Human approves. Go executes.**

- **Observe:** a React dashboard shows warehouse state, telemetry, active incidents and job progress from Go snapshots.
- **Recommend:** a TypeScript/Mastra agent produces grounded explanations and can submit a pending proposal for charging in place or transferring an uncollected job.
- **Review:** an independent operator approves or rejects; Go revalidates before execution and records domain attempts in PostgreSQL.
- **Measure:** separate evaluations test recommendation quality, adversarial rejection and paired simulator recovery.

**Recorded evidence:** 11/12 real-model decision trials passed; host validation rejected the remaining poisoned-runtime recommendation. In a separate Go experiment, reassignment delivered a blocked job in three ticks while the baseline remained unfinished after 200 ticks. These are historical, scoped results, not live dashboard metrics or a general reliability claim.

## Architecture and stack

```text
Go simulator -> telemetry + Go-classified conditions -> Mastra reasoning
                                                         | pending proposal
React operator review -> authenticated GraphQL -> Go fleet service -> simulator command
                                                        |
                                           PostgreSQL proposals + audit attempts
```

The fleet service owns the simulator and serializes ticks, submissions and decisions with one mutex. The agent has observation/proposal tools, with no approval, execution, ticking or fault-injection tools. PostgreSQL persists workflow records; running simulator state stays in memory.

| Layer | Technology and responsibility |
| --- | --- |
| Simulation | Go 1.27.1; seeded ticks, A* routing, dispatch, battery/charging and bounded recovery |
| Service/API | Go, gqlgen GraphQL, trusted bearer identities, admission and execution checks |
| Persistence | PostgreSQL 18.6, pgx, migrations and transactional proposal/audit records |
| Reasoning | TypeScript, Mastra, Zod; snapshot grounding and bounded statement validation |
| Operator UI | React 19, TypeScript, Vite, SVG map and approval inbox |
| Verification | Go tests/race checks, Vitest, strict TypeScript checks and paired recovery harnesses |

See [architecture](docs/architecture.md) and [engineering decisions](docs/decisions.md).

## Safety and human approval

Submission creates a pending proposal without executing it. The service checks trusted identity, canonical parameters, runtime ownership and expiry; submitter and approving operator must be independent. Approval triggers another Go preflight immediately before the command. Checks include robot health, the 30% battery floor, job ownership and station/route eligibility. Rejection or expiry cannot execute a command.

Only `SetCharging` (already at a station) and `ReassignUnstartedJob` are supported operator commands. Successful reassignment means ownership transferred; delivery requires subsequent simulator ticks. It does not repair the obstructing robot.

Simulator memory and SQL commits cannot roll back together. Persistence uncertainty poisons the runtime and stops further ticks/mutations pending reconciliation. Clients retain unknown outcomes rather than automatically retrying. Domain audit records are not a complete HTTP access log, and failed database writes cannot be advertised as durable evidence. The [frozen safety rules](docs/safety-rules.md) remain authoritative.

## Evaluation evidence

These results come from the [Phase 5 report](docs/phase5-evaluation-report.md) and [saved real-model artifact](docs/evidence/p5-04-live-full.json). They were not regenerated for this README.

| Evaluation | Recorded result | Scope |
| --- | --- | --- |
| OpenAI `gpt-6-luna` | **11/12** recommendation/host passes; 12/12 completed calls | One trial per selected fixture/synthetic scenario |
| Poisoned runtime | Model proposed reassignment despite required abstention; TypeScript host rejected it | No submission or Go execution was reached |
| Adversarial contracts | **16/16** bad outputs rejected after remediation; **4/4** deterministic input defenses passed | Input attacks used a local adapter; no live adversarial trials |
| Mastra local adapter | **12/12** decisions passed | Deterministic integration, without a live LLM |
| Paired Go recovery | **5/5 experiment criteria** | Two command successes, three no-intervention controls; one delivery improvement |

The model receives a deterministic **supported-action hint** and allowed statements. This measures assisted contract compliance, not independent incident diagnosis or action discovery. Twelve non-random cases with one trial each are a small sample, with no generalization or variability estimate. Phase 5 model runs did not exercise human approval, GraphQL execution or PostgreSQL audits; [Phase 4 acceptance](docs/phase4-acceptance.md) records a separate end-to-end demonstration.

### Measured simulator outcomes

Each Phase 5 pair uses seed 42, equal initial state and a 200-tick observation window, with a trusted test operator rather than a human reviewer.

- **Blocked job:** baseline J1 remained unfinished at tick 242; reassignment at tick 42 delivered J1 at tick 45. Treatment added one completed job and removed 200 blocked-robot post-tick samples, using 13 more aggregate stored battery percentage-points.
- **Routine charging:** R1 ended at 91% versus 40% baseline battery, a 51-point gain; no competing jobs or throughput improvement were measured.
- **Three controls:** no eligible recipient, offline source and healthy fleet produced no command and identical paired final states.

Ticks are simulation steps; battery percentage-points are not kWh. An unfinished baseline cannot support a percentage speedup. Five passing criteria are not five successful recoveries.

## Engineering iteration

| Decision | Reason and lesson |
| --- | --- |
| Deterministic simulation | Seeded RNG, ordered processing and replay checks permit reproducible regressions and equal baseline/treatment comparisons, trading physical realism for inspectable experiments. |
| Safety in Go | Grounded text can still recommend the wrong action, as the poisoned-runtime failure shows. The reasoning host checks its contract; Go owns operational admission and execution. |
| Independent human approval | A recommendation does not authorize a command. Canonical pending proposals, independent identities and repeated preflight preserve review as state changes. |
| Constrained explanations | Initial adversarial checks rejected only 14/16 outputs: false approval and delivery claims passed grounding. Exact host-supplied statements closed those probes, at the cost of rejecting some truthful paraphrases. |
| Separate evidence layers | Correct recommendations, accepted commands, delivery and committed audits require different evidence; the reports keep those measurements separate. |

Phases 0–2 established repository and deterministic simulation/navigation foundations. Phase 3 added the safety/proposal boundary; Phase 4 connected persistence, GraphQL, Mastra and operator review. Phase 5 added decision, adversarial and paired recovery evaluation. Phase 6 began with the local demo gateway and repository inventory; the dashboard redesign and richer controls remain unfinished. Historical simulator acceptance limitations remain in [known limitations](docs/known-limitations.md).

## Local demo quickstart

Use PowerShell 7, Docker, Node **24.21.0** and Go **1.27.1**. From the repository root:

```powershell
npm ci --prefix agent
npm ci --prefix web
pwsh -NoProfile -File evals/phase4/start-demo.ps1 -LocalDemo -Submit
```

The launcher builds both apps and creates its own temporary backend test copy and ephemeral PostgreSQL container. Open the loopback URL printed after `READY`. No API key is needed: **the ordinary demo uses deterministic Mastra reasoning, not a live LLM**. Its gateway gives browser sessions a shared demo operator identity and must stay local.

### Demo walkthrough

1. At tick 42, inspect blocked R1 and uncollected J1 in the warehouse and approval inbox.
2. Review the pending R1 → R3 reassignment. As the human operator, click **Approve** once, or reject without executing.
3. Distinguish the transfer receipt from delivery. After approval, scripted ticks show delivery at tick 45, then compare with the untouched baseline at tick 242.
4. Inspect generated evidence and committed audits using the [full demo guide](docs/phase4-demo.md). Recorded reports are historical evidence; only a newly observed session establishes that session's outcome.

Stop gracefully from another terminal:

```powershell
New-Item evals/dist/p405/stop -ItemType File -Force | Out-Null
```

The launcher removes its temporary infrastructure. Preserve sanitized evidence before another run overwrites it. See the guide for rejection, expiry, outages and cleanup. Real screenshots and a recorded walkthrough will follow dashboard completion and verification; these steps describe the existing interface.

## Current status and further reading

Phases 0–5 and DEMO-01 are merged; DEMO-02 inventory is available locally. DEMO-03 is documentation polish. The approved Phase 6 dashboard design direction remains planned; no redesigned dashboard, playback, scenario controls or Phase 7 public deployment is claimed here. Remote CI acceptance and branch protection were not checked for this task.

- [Known limitations](docs/known-limitations.md): model, simulator, lifecycle and deployment boundaries.
- [Architecture](docs/architecture.md), [decisions](docs/decisions.md), [safety rules](docs/safety-rules.md).
- [Phase 5 report](docs/phase5-evaluation-report.md), [evaluation runner/offline commands](evals/README.md), [results log](docs/eval-results.md).
- [Recovery demo](docs/phase4-demo.md), [Phase 4 acceptance](docs/phase4-acceptance.md), [normal GraphQL setup](docs/local-graphql-demo.md), [operator inbox](docs/operator-inbox.md).

Repository layout: `backend/` owns simulation and service boundaries; `agent/` owns reasoning/submission; `web/` owns operator presentation; `evals/` owns evaluation harnesses; `docs/` records decisions and evidence.

For development, install pinned dependencies in `agent`, `web` and `evals`, then run `make test` (Go tests/vet and all three npm test/typecheck suites). Windows Make workflows require WSL with corresponding tools and Docker integration. [Verification limitations](docs/known-limitations.md#verification-and-provenance) explain why historical failures must not be presented as a clean current full-suite result.

For the separate Compose database setup, copy `.env.example` to ignored `.env`, replace database placeholders privately, then use `make up` and `make down`. PostgreSQL binds to loopback; `POSTGRES_HOST_PORT` overrides its host port. The named volume preserves data; do not use `down -v`. CI defines test, integration and Gitleaks jobs; configuration alone does not establish passing remote runs or required branch-protection checks.
