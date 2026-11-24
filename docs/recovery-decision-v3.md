# Recovery decision contract audit

The original `recovery-decision-v2` contract remains the control for the historical 24-case authenticity audit. Its schema, prompts, scoring and recorded outcomes stay versioned together.

| Legacy field | Owner in the upgraded contract |
| --- | --- |
| Wire version, observation ID, tick | The host binds the original trusted Go observation and checks the provenance receipt. |
| Objective | The caller sets a fixed objective or permits an explicit set. The model chooses only within that set. |
| Incident identity | The model cites relevant robot/type pairs. The host resolves them against actual incident reads and attaches exact observation-bound references. |
| Incident severity and telemetry | The host copies the complete, pinned Go incident results. |
| Considered candidate tuples, candidate membership | Go supplies eligible tuples. The host attaches the full set. The model cites candidates when selecting or comparing. |
| Source and recipient telemetry, job identity, status, ownership and coordinates | The host joins the candidate tuples to the actual robot and job tool results. |
| Pickup, delivery, charging-exit steps and required battery | Go supplies these route facts through the candidate tool. |
| Total route, mission headroom, admission headroom | The host derives these from candidate results and recipient telemetry. Admission headroom is battery minus the larger of 30 and the required battery. |
| Comparison pairs and metric | The model selects the evidence that supports its judgment. |
| Comparison values and numerical ordering | The host evaluates each referenced metric using Go facts. |
| Select/abstain, basis, abstention reason | The model chooses. The host checks consistency with the objective, evidence and poisoned-runtime state. |
| Operator-attention references and invariant limitations | The host attaches every active incident and the six legacy limitations once, including independent approval. |
| Authorization, freshness, energy, collision safety, ownership, reservations and execution | Go validates against current state and remains authoritative for proposals, independent approvals, execution and receipts. |

The legacy live audit attempted all 24 cases and records 19 accepted cases, four grounding rejections and one pre-finalization pipeline block. The original route-objective run selected R3, and the retained structured response lacks the exact rejected field. Equal-cost alternatives and candidate order `021` both failed `considered[0].admissionHeadroom`. No-eligible-candidate returned the expected abstention and failed the `limitations` set check. The evidence and original dispositions are in the [Phase 4 acceptance report](v2-phase4-acceptance.md#current-legacy-status).

The proposed V3 preserves the distinction between model choice and admissibility by accepting references and judgment, validating the references against completed tool reads, and recording the host-derived evidence separately. Model-selected uncertainty leaves the decision in shadow and never creates a command. V2 remains independently testable against its original cases.

## V3 implementation and evaluation

V3 finalizes a strict document with `version`, `objective`, `relevantIncidents`, `comparisons`, and `decision`. Candidate references contain a job, its current owner and a proposed recipient. Incident references contain the observed robot and incident type. Comparison references name two candidates and a metric. The model can abstain on conflicting evidence under the tradeoff objective. Missing required observations or tool results end the investigation safely.

V3 reads motor, sensor and critical-battery incidents as Go-classified evidence and preserves their original identities in the agent record. Physical repair remains the responsibility of Go maintenance and the separate Operations worker. Only an eligible traffic-blocked source can ground a V3 reassignment. The V2 generation entry retains the original incident vocabulary.

The host verifies complete, observation-bound tool returns, resolves references, and reconstructs the same verified evidence projection independently understood by Go. That projection retains the historical V2 evidence shape for the shared validation and rendering code. The new wire response and durable Go metadata are explicitly versioned V3. The evaluated trial retains the schema-valid model intent even when grounding rejects it, with noncanonical identifiers redacted. Malformed output is represented by a safe diagnostic. The trial records the tool results, host projection and Go receipt alongside separate HTTP evidence. PostgreSQL holds Go-verified facts and the bounded completion record. The evidence directory holds the detailed traces.

The manual Operations recovery entry point accepts an explicit review event carrying a Go observation ID. It rejects stale events before model generation and routes valid events to shadow review with proposal submission disabled. The opt-in V3 Operations Manager also handles eligible incidents through the normal visitor controller. It records runs under a proposer identity and sends only grounded, fresh results through Go admission. Admitted proposals await a separate operator. The single-request worker remains available through the existing policy.

V3 evaluation uses an explicit opt-in on the existing AGENT-07 paired harness. Paired simulator branches report J1 progress, completion ticks, deliveries, unresolved jobs, blocked samples and remaining battery over the same fixed horizon. The provider traces and trial records supply tool evidence, estimated costs, decision quality and latency.

## Live verification and Operations boundary

The earlier paired Go harness completed five genuine `gpt-6-luna` cases in 13 HTTP 200 responses. The shadow tradeoff abstained with `operator_tradeoff`. For the same seed-42 initial state, minimum pickup selected R3 with J1 delivered at tick 45, while maximum battery headroom selected R4 with J1 delivered at tick 50. Both executions used authenticated Go proposals and test-harness operator approvals. Healthy and no-recipient cases abstained. The detailed provider and replay evidence stays in local verification storage.

The manual entry is `--live --operations-recovery-shadow` with a matching `FLEETOPS_RECOVERY_REVIEW_OBSERVATION_ID`. The normal V3 worker is enabled with the Operations opt-in and `FLEETOPS_OPERATIONS_PROTOCOL=v3`. Go reserves and settles each provider request through PostgreSQL. Routine scheduling remains deterministic. See the [Operations configuration and limits](operations-manager.md).

### Legacy compatibility

The model-facing V2 contract remains supported through `--live --decision-v2`, with optional `--submit` for an authenticated Go proposal, and remains the default for AGENT-07 unless the evaluation explicitly selects V3. The frozen historical live worker still imports V2. The V3 operator CLI is shadow-only, so it cannot replace the V2 proposal workflow. V3 also calls the shared V2 validation of the host-derived evidence projection. Retiring the V2 model-facing entry point requires a reviewed workflow migration or retirement and closure or explicit disposition of the historical audit. Removing the shared projection validator would additionally require a separate compatibility change. Existing Go readers and durable V2 audit records must remain supported.

The investigation trace records the budget guard name and the byte count for a prompt-size rejection. The general `budget_exhausted` category still governs cancellation under the existing request and token bounds. The historical healthy V2 guard remains unknown.
