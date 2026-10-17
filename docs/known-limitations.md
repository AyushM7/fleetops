# Known limitations

Current boundaries verified for DEMO-03 against the [DEMO-02 inventory](demo-inventory.md), [Phase 5 report](phase5-evaluation-report.md) and source references below. Recorded evidence retains its original scope; this documentation task did not rerun a live model or interactive demo.

## Reasoning and evaluation

- **Restricted independent action discovery.** The prompt supplies a deterministic supported-action hint, Go-admitted candidates and exact allowed statements. The agent can propose only charging in place or pre-pickup reassignment. It cannot approve, execute, tick, inject faults or repair an offline robot. This is assisted contract compliance, not autonomous fleet management. See [agent tools](../agent/src/tools.ts) and [reasoning validation](../agent/src/reasoning.ts).
- **Small real-model sample.** The saved run contains 12 selected cases, one trial each: three existing tested fixtures and nine synthetic reasoning variants. Eleven passed; the poisoned-runtime wrong action was rejected by the TypeScript host before submission. There is no held-out generalization estimate, cross-model comparison or live adversarial coverage.
- **Bounded explanations.** Exact statements prevent the tested false approval/delivery assertions but can reject truthful paraphrases. This is not general natural-language fact checking. Four deterministic input defenses do not establish live prompt-injection resistance.
- **Limited recovery experiments.** Five paired Go experiments use one seed and fixed 200-tick windows, with test-controlled approval. Two commands succeeded; three controls abstained. These do not measure realistic operator latency, production throughput or an end-to-end live-model intervention. Charging had no competing jobs. Battery units are stored percentage-points, not physical energy.

The [evaluation report](phase5-evaluation-report.md) records methodology and all denominators, including the original adversarial gaps and the retained model failure.

## Local demo and dashboard

The ordinary [demo launcher](../evals/phase4/start-demo.ps1) uses deterministic Mastra reasoning and trusted scenario setup, with scripted tick progression. It is not a live LLM demonstration or spontaneous discovery of a traffic incident. Human approval in the interactive flow remains separate from automated harness decisions.

The DEMO-01 gateway is a loopback-only test harness with a shared demo operator identity, a 20-minute cookie session and temporary PostgreSQL infrastructure. It is not a public or multi-user authentication design. Restarting means stopping and relaunching; no runtime pause/resume, step, reset, speed or scenario-selection API exists. Evidence files are overwritten on later runs; cleanup removes private schemas/container. A crash can leave launcher locks or infrastructure requiring investigation. See the [demo guide](phase4-demo.md).

The existing dashboard polls snapshots and displays current conditions. It has no actual-route view, movement interpolation, recorded playback, persistent incident timeline or baseline/treatment comparison panel. Agent rationale is not linked into persisted proposals or delivered as a dynamic inbox explanation. These are future contracts, not implemented Phase 6 features. The approved dashboard design direction is unchanged; no new visual acceptance is claimed.

## Runtime, persistence and scalability

The simulator is an in-memory discrete grid model, not robot hardware or continuous physics. Public snapshots omit private RNG, navigation and recovery state and cannot restore a complete checkpoint. PostgreSQL persists proposal/audit workflow, not the running simulation or incident history. See [simulator](../backend/sim/sim.go), [service](../backend/fleet/service.go) and [storage](../backend/fleet/storage.go).

One service mutex serializes ticks and decisions. There is no demonstrated distributed simulator ownership, multi-instance coordination, horizontal scaling result or production load benchmark. The normal server is distinct from the loopback demo gateway; the gateway's local protections cannot be assumed for a deployment. Phase 7 public hosting, production authentication/operations and deployment acceptance are not complete.

Simulator commands and database commits cannot be atomically rolled back together. Persistence uncertainty poisons the runtime; reconciliation is required. Restarting does not prove a previous command failed. Transport rejection precedes domain auditing, and database failure can prevent durable audit evidence. Automatic mutation retries would misrepresent unknown outcomes.

## Verification and provenance

- Historical Phase 2 closeout retains S3/seed99's 108-tick healthy-traffic wait above WaitCap40. The rejected lookahead change was reverted. That historical limitation is not erased by merged phases or polished documentation; independently authenticated P2-07 full-run compatibility evidence also remains unavailable. See [architecture closeout](architecture.md#phase-2-closeout-evidence) and [decisions](decisions.md).
- DEMO-02's native Windows backend run failed the two byte-for-byte golden comparisons. Both outputs matched after in-memory CRLF normalization; fixtures were unchanged. That run reported no soak failure, which does not resolve the separate historical limitation above. Do not call it a passing full suite.
- DEMO-02 records passing retries of 37 web, 100 agent and 242 eval tests with explicit Windows runner adjustments. Those are prior results, not fresh DEMO-03 application verification. Database/browser acceptance, remote CI status and branch protection were not rechecked there or here.
- Saved model evidence lacks an exact historical run timestamp/source revision, immutable model version and provider usage receipt. Exact API cost is unconfirmed; configured estimates are not charges. Saved reports must be labelled recorded evidence rather than live results.

See the [inventory verification record](demo-inventory.md#10-tests-ci-and-inspection-verification) and [Phase 5 provenance](phase5-evaluation-report.md#cost-and-evidence-provenance). No paid calls are required to inspect or replay saved evidence.
