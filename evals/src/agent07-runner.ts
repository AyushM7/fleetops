import { runRecoveryDecision, type RecoveryDecisionResult } from "../../agent/src/decision-agent.js";
import { completionRecord, type AgentRunCompletion } from "../../agent/src/agent-run.js";
import type { FleetAPI } from "../../agent/src/graphql.js";
import type { FleetState } from "../../agent/src/contracts.js";
import type { CandidateKey } from "../../agent/src/decision-contracts.js";
import { baseline, dataset, datasetSHA256, policies, quality, syntheticState, type Policy, type Scenario } from "./agent07-dataset.js";
import { scriptedModel, type Probe } from "./agent07-scripted.js";
import { sha256 } from "./agent07-cost.js";
import { snapshotReference } from "../../agent/src/reasoning.js";

export interface Trial {
  scenarioId: string; policy: Policy; objective: Scenario["objective"]; source: "mocked" | "deterministic" | "live-model";
  requestedModel: string | null;
  observationProvenance: "synthetic-tool-contract-only" | "authenticated-go-runtime";
  initialObservationId: string | null; initialStateHash: string | null; candidateFacts: FleetState["reassignmentCandidates"];
  incidentFacts: FleetState["activeIncidents"];
  status: string; failure: string | null; selected: CandidateKey | null; abstention: string | null;
  quality: ReturnType<typeof quality>; completion: AgentRunCompletion | null;
  latencyMs: number | null; proposal: RecoveryDecisionResult["proposal"] | null; provenance: RecoveryDecisionResult["provenance"] | null;
  groundingAccepted: boolean; expectationMet: boolean; submissionAttempts: number | null;
  cost: { estimatedMaximumUSD: number; actualBilledUSD: null; method: string; providerHTTPRequests: number } | null;
  modelCalls: { localAdapterFailures: number | null; providerHTTPAttempts: number | null; failedHTTPResponses: number | null; uncertainHTTPResults: number | null };
  tools: { successfulExecutions: number; countedExecutions: number; evidenceComplete: boolean } | null;
}
export function syntheticAPI(state: FleetState, behavior?: "unknown" | "stale"): FleetAPI {
  let reads = 0, finishes = 0;
  return { readFleet: async () => { const next = structuredClone(state); if (reads++ && behavior === "stale") { next.tick++; next.observationId = "e".repeat(64); } return next; },
    startAgentRun: async observationId => ({ id: "synthetic-run", runtimeId: "synthetic-runtime", observationId }),
    finishAgentRun: async (id, input) => { if (++finishes > 1 || behavior === "unknown" || input.submit) throw new Error("Synthetic persistence unavailable; no proposal authority."); return { runId: id, recorded: true, denial: "", proposalResult: null }; },
    submitCharging: async () => { throw new Error("No command authority"); }, submitReassignment: async () => { throw new Error("No command authority"); } };
}
export async function trial(scenario: Scenario, policy: Policy, api: FleetAPI, options: {
  go?: boolean; live?: boolean; allowSubmit?: boolean; model?: Parameters<typeof runRecoveryDecision>[1]["model"]; requestedModel?: string; probe?: Probe; signal?: AbortSignal;
} = {}): Promise<Trial> {
  const started = performance.now(); let state: FleetState | null = null;
  const result: Trial = { scenarioId: scenario.id, policy, objective: scenario.objective, source: policy !== "v2-investigation" ? "deterministic" : options.live ? "live-model" : "mocked",
    requestedModel: policy !== "v2-investigation" ? null : options.live ? options.requestedModel ?? null : "offline-agent07/scripted",
    observationProvenance: options.go ? "authenticated-go-runtime" : "synthetic-tool-contract-only", initialObservationId: null, initialStateHash: null,
    candidateFacts: [], incidentFacts: [], status: "generation-failed", failure: "pipeline_unavailable", selected: null, abstention: null, completion: null,
    latencyMs: 0, proposal: null, provenance: null, groundingAccepted: false, expectationMet: false,
    quality: { admitted: false, regret: null, dominated: null, objectiveMet: false }, submissionAttempts: 0, tools: null, cost: null,
    modelCalls: { localAdapterFailures:null, providerHTTPAttempts:null, failedHTTPResponses:null, uncertainHTTPResults:null } };
  const wrapped: FleetAPI = { ...api, finishAgentRun: async (id, input) => {
    if (input.submit) result.submissionAttempts = (result.submissionAttempts ?? 0)+1;
    return api.finishAgentRun!(id, input);
  } };
  try {
    if (options.signal?.aborted) throw new Error("Paused/cancelled evaluation cannot generate.");
    if (options.model && (typeof options.model === "string" || !("doGenerate" in options.model))) throw new Error("Provider configurations require the reviewed CLI resolver.");
    if (options.model && !options.live && (!("provider" in options.model) || !String(options.model.provider).startsWith("offline-"))) throw new Error("Offline evaluation requires an explicitly local adapter.");
    if (options.live && (!options.go || !options.model || process.env.CI || process.env.VITEST || process.env.NODE_ENV==="test" || !("provider" in options.model) || !String(options.model.provider).startsWith("openai"))) throw new Error("Unreviewed live trial blocked.");
    state = await api.readFleet(); result.initialObservationId = state.observationId ?? null;
    result.incidentFacts = structuredClone(state.activeIncidents);
    result.initialStateHash = sha256(JSON.stringify({ ...state, observationId: undefined })); result.candidateFacts = structuredClone(state.reassignmentCandidates);
    if (policy === "v2-investigation") {
      const scripted = options.model ? null : scriptedModel(options.probe ?? scenario.probe);
      const model = options.model ?? scripted!.model;
      let pinned = true;
      const run = await runRecoveryDecision({ ...wrapped, readFleet: async () => { if (pinned) { pinned = false; return state!; } return api.readFleet(); } }, { enabled: true, model, source: result.source,
        objective: scenario.objective, allowSubmit: options.allowSubmit === true, signal: options.signal });
      // The first pinned read may be reused; subsequent freshness checks must still read Go.
      result.completion = completionRecord(run.aiAnalysis.status, run.aiAnalysis.decision, run.investigation, performance.now() - started);
      result.completion.submissionCheck = run.submissionCheck;
      if (scripted) result.modelCalls.localAdapterFailures=scripted.failures;
      const events = run.investigation.events.filter(e => e.kind === "tool");
      const references = (name: string) => new Set(events.filter(e => e.name === name).map(e => JSON.stringify((e.input as { reference: unknown }).reference)));
      const ids = (name: string) => new Set(events.filter(e => e.name === name).flatMap(e => (e.input as { ids: string[] }).ids));
      result.tools = { successfulExecutions: events.length, countedExecutions: run.investigation.toolCalls,
        evidenceComplete: state.activeIncidents.every(i => ["readIncident", "listRecoveryCandidates"].every(n => references(n).has(JSON.stringify(snapshotReference(state!, i))))) &&
          state.reassignmentCandidates.every(c => ids("readRobots").has(c.fromRobotId) && ids("readRobots").has(c.toRobotId) && ids("readJobs").has(c.jobId)) };
      result.status = run.aiAnalysis.status; result.failure = result.completion.failure === "none" ? null : result.completion.failure;
      const d = run.aiAnalysis.decision?.decision;
      result.selected = d?.kind === "select" ? d.selected : null; result.abstention = d?.kind === "abstain" ? d.reason : null;
      result.proposal = run.proposal; result.provenance = run.provenance; result.groundingAccepted = run.aiAnalysis.status === "accepted";
      if (run.provenance.status === "unknown") result.failure = "completion_unknown";
      if (run.submissionCheck === "snapshot_changed") result.failure = "snapshot_changed";
    } else {
      result.selected = baseline(state, policy); result.status = "deterministic"; result.failure = null;
      result.abstention = result.selected ? null : state.poisoned ? "runtime_poisoned" : state.activeIncidents.some(i => i.type === "traffic_blocked") ? "no_candidates" : "no_supported_incident";
      if (options.allowSubmit && result.selected) {
        result.submissionAttempts = (result.submissionAttempts ?? 0)+1;
        const receipt = await api.submitReassignment({ ...result.selected, expectedObservationId: state.observationId!, expiresAt: new Date(Date.now() + 60_000).toISOString() });
        result.proposal = { status: !receipt.error && receipt.state === "pending" && receipt.auditCommitted ? "pending_approval" : "rejected", proposalId: receipt.proposalId || undefined };
      }
    }
    result.quality = quality(scenario, state, result.selected);
    const justifiedTradeoff = result.abstention === "operator_tradeoff" && scenario.expected === "admitted-or-tradeoff" && result.groundingAccepted;
    if (justifiedTradeoff) result.quality.objectiveMet = true;
    result.expectationMet = scenario.expected === "grounding-rejection" ? result.status === "rejected" : scenario.expected === "invalid-tool-rejection" ? result.failure === "invalid_tool" :
      scenario.expected === "stale-denial" ? result.failure === "snapshot_changed" || result.proposal?.status === "rejected" :
      result.abstention ? result.abstention === scenario.expected || justifiedTradeoff : result.quality.objectiveMet && (policy !== "v2-investigation" || result.groundingAccepted);
  } catch {
    // Attempts stay in the denominator; an uncertain acknowledgement is never retried.
    result.status="generation-failed";
    result.failure=result.submissionAttempts ? "submission_unknown" : options.signal?.aborted ? "cancelled" : "pipeline_unavailable";
    if (result.submissionAttempts) result.proposal={status:"unknown"};
    if(state) result.quality=quality(scenario,state,result.selected);
  }
  result.latencyMs = Math.round(performance.now() - started);
  return result;
}
export function aggregate(rows: Trial[]) {
  return policies.map(policy => {
    const all = rows.filter(r => r.policy === policy), selected = all.filter(r => r.selected), toolRuns = all.filter(r => r.completion);
    const expectedAbstentions = all.filter(r => r.abstention!==null || ["no-recipients", "healthy", "poisoned"].includes(r.scenarioId));
    const qualityRows = all.filter(r => dataset.scenarios.find(s => s.id === r.scenarioId)?.quality !== "not-applicable");
    const latencies = all.flatMap(r => r.latencyMs === null ? [] : [r.latencyMs]);
    return { policy, attemptedTrials: all.length, admittedSelections: { numerator: selected.filter(r => r.quality.admitted).length, denominator: selected.length },
      groundingAccepted: { numerator: all.filter(r => r.groundingAccepted).length, denominator: policy === "v2-investigation" ? all.length : 0 },
      appropriateAbstention: { numerator: expectedAbstentions.filter(r => r.expectationMet).length, denominator: expectedAbstentions.length },
      groundingFailures: all.filter(r => r.failure === "grounding_rejected").length,
      unsupportedToolAttempts: all.filter(r => r.failure === "invalid_tool").length,
      budgetExhaustions: all.filter(r=>r.failure==="budget_exhausted").length,
      modelCallFailures: { localAdapterFailures: all.some(r=>r.modelCalls.localAdapterFailures!==null) ? all.reduce((n,r)=>n+(r.modelCalls.localAdapterFailures??0),0) : null,
        failedHTTPResponses: all.some(r=>r.modelCalls.failedHTTPResponses!==null) ? all.reduce((n,r)=>n+(r.modelCalls.failedHTTPResponses??0),0) : null,
        uncertainHTTPResults: all.some(r=>r.modelCalls.uncertainHTTPResults!==null) ? all.reduce((n,r)=>n+(r.modelCalls.uncertainHTTPResults??0),0) : null,
        attemptedInvestigationTrials:policy==="v2-investigation"?all.length:0, interpretation:"Direct adapter/HTTP observations only; framework, grounding and stream-finalization failures remain separate" },
      failedOrUnknown: all.filter(r => r.status === "failed" || r.status === "generation-failed" || r.failure === "completion_unknown").length,
      toolBudgetCompliant: { numerator: toolRuns.filter(r => r.completion!.modelRequests <= 6 && r.completion!.toolExecutions <= 8).length, denominator: policy === "v2-investigation" ? all.length : 0 },
      unknownCompletionReports: policy === "v2-investigation" ? all.length-toolRuns.length : 0,
      toolExecutionsSuccessful: { numerator: all.reduce((n,r) => n+(r.tools?.successfulExecutions ?? 0),0), denominator: all.reduce((n,r) => n+(r.tools?.countedExecutions ?? 0),0) },
      toolEvidenceComplete: { numerator: all.filter(r => r.tools?.evidenceComplete).length, denominator: policy === "v2-investigation" ? all.length : 0 },
      objectiveMet: { numerator: qualityRows.filter(r => r.quality.objectiveMet && r.failure === null).length, denominator: qualityRows.length },
      dominated: { numerator: selected.filter(r => r.quality.dominated === true).length, denominator: selected.length },
      reportedUsage: { complete: toolRuns.filter(r => r.completion!.reportedUsage.status === "reported").length, partial: toolRuns.filter(r => r.completion!.reportedUsage.status === "partial").length, unavailable: policy === "v2-investigation" ? all.filter(r => !r.completion || r.completion.reportedUsage.status === "unavailable").length : 0,
        reportedTokens: toolRuns.some(r => r.completion!.reportedUsage.totalTokens !== null) ? toolRuns.reduce((n, r) => n + (r.completion!.reportedUsage.totalTokens ?? 0), 0) : null,
        label: "reported subtotal only; missing reports are unknown; mock values are not billing evidence" },
      latencyMs: latencies.length ? { minimum: Math.min(...latencies), maximum: Math.max(...latencies), reports: latencies.length, attemptedTrials: all.length } : null };
  });
}
export async function offlineEvaluation(scenarios = dataset.scenarios, trials = 1, milliseconds = 600000) {
  const rows: Trial[] = [];
  const plannedTrials = scenarios.reduce((n,s) => n+trials*(s.probe || s.expected === "stale-denial" ? 1 : policies.length),0), expires = Date.now()+milliseconds;
  const signal = AbortSignal.timeout(Math.max(1,milliseconds));
  execution: for (const s of scenarios) for (let n = 0; n < trials; n++) for (const policy of s.probe || s.expected === "stale-denial" ? ["v2-investigation" as const] : policies) {
    if (Date.now() >= expires) break execution;
    rows.push(await trial(s, policy, syntheticAPI(syntheticState(s), s.expected === "stale-denial" ? "stale" : undefined), { allowSubmit: s.expected === "stale-denial", signal }));
  }
  return { version: dataset.version, fixtureVersion: dataset.fixtureVersion, datasetSHA256, mode: "offline-synthetic-tool-contract", actualLLM: "not_measured", goAdmission: "not_measured", simulatorOutcomes: "not_measured",
    historicalResults: "Phase-5 assisted 11/12 and recovery-decision-eval-v2 17-case contract benchmark are separate, not pooled", plannedTrials, notAttemptedTrials: plannedTrials-rows.length,
    stoppedReason: rows.length < plannedTrials ? "elapsed ceiling; no retry" : null, rows, aggregate: aggregate(rows), estimatedCostUSD: null, actualBilledCostUSD: null };
}
// Replay compares scientific results, not clock measurements or random runtime/proposal IDs.
export function replayProjection(rows: Trial[]) {
  return rows.map(({ latencyMs: _latency, initialObservationId: _obs, provenance, proposal, completion, ...r }) => ({ ...r,
    provenance: provenance ? { status: provenance.status } : null, proposal: proposal ? { status: proposal.status } : null, completion: completion ? { ...completion, latencyMs: null } : null }));
}
