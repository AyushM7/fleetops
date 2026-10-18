import type { AIReasoning, FleetState, MutationResult } from "./contracts.js";
import type { MastraModelConfig } from "@mastra/core/llm";
import { readFileSync } from "node:fs";
import { validateSnapshot } from "./reasoning.js";
import type { CandidateComparison, CandidateEvidence, DecisionObjective, RecoveryDecision } from "./decision-contracts.js";
import { decisionLimitations } from "./decision-contracts.js";

export function fleetFixture(battery = 100): FleetState {
  return { observationId: "a".repeat(64), tick: 0, poisoned: false, activeIncidents: [], jobs: [], reassignmentCandidates: [],
    robots: [{ id: "R1", x: 0, y: 0, battery, status: "idle", jobId: "", waitTicks: 0, blocked: false }],
    stations: [{ id: "S1", x: 0, y: 0, occupiedBy: "", reservedBy: "" }],
  };
}
export const pendingFixture: MutationResult = {
  proposalId: "p1", state: "pending", execution: "none", auditCommitted: true, executionAttempted: false, error: null,
};

export function reasoningFixture(state: FleetState): AIReasoning {
  return { tick: state.tick, interpretations: state.activeIncidents.map(i => ({
    reference: { scope: "snapshot", tick: state.tick, robotId: i.robotId, type: i.type },
    severity: i.severity, telemetry: { ...i.telemetry },
    assessment: "This condition can interrupt warehouse operations; an operator should investigate the supplied telemetry.",
    recommendedResponse: "operator_attention",
  })), proposedAction: null };
}
export function chargingReasoningFixture(state: FleetState): AIReasoning {
  return { ...reasoningFixture(state), proposedAction: {
    action: "SetCharging", robotId: state.robots[0].id, stationId: state.stations[0].id,
    purpose: "routine_charging", rationale: "Charging in place is a supported option for this snapshot.",
    evidence: { tick: state.tick, robot: { ...state.robots[0] }, station: { ...state.stations[0] } },
  } };
}

// Synthetic reasoning-only variants of the saved Go export; changed costs are not new Go route measurements.
export function recoveryDecisionState(variant: "non_first_minimum" | "tradeoff" | "tie" | "none" = "non_first_minimum"): FleetState {
  const rows = JSON.parse(readFileSync(new URL("../../docs/evidence/demo-08/observations.json", import.meta.url), "utf8")) as { id: string; snapshot: unknown }[];
  const state = validateSnapshot(rows.find(row => row.id === "blocked-reassignment")?.snapshot);
  state.observationId = "c".repeat(64);
  const pickups = variant === "tradeoff" ? [1, 6, 4] : variant === "tie" ? [3, 3, 6] : [6, 1, 4];
  for (const [index, c] of state.reassignmentCandidates.entries()) {
    c.pickupSteps = pickups[index]; c.requiredBattery = pickups[index] + 12;
    const recipient = state.robots.find(r => r.id === c.toRobotId)!;
    recipient.battery = variant === "tradeoff" ? [48, 96, 65][index] : 80;
  }
  if (variant === "none") state.reassignmentCandidates = [];
  return state;
}
function decisionEvidence(state: FleetState): CandidateEvidence[] {
  return state.reassignmentCandidates.map(c => ({
    candidate: { ...c }, source: { ...state.robots.find(r => r.id === c.fromRobotId)! },
    recipient: { ...state.robots.find(r => r.id === c.toRobotId)! }, job: { ...state.jobs.find(j => j.id === c.jobId)! },
    incidentReference: { scope: "snapshot", tick: state.tick, robotId: c.fromRobotId, type: "traffic_blocked" },
    totalRouteSteps: c.pickupSteps + c.deliverySteps + c.chargingExitSteps,
    missionHeadroom: state.robots.find(r => r.id === c.toRobotId)!.battery - c.requiredBattery,
    admissionHeadroom: state.robots.find(r => r.id === c.toRobotId)!.battery - Math.max(30, c.requiredBattery),
  }));
}
function decisionComparisons(evidence: CandidateEvidence[], chosen: CandidateEvidence): CandidateComparison[] {
  return evidence.filter(e => e !== chosen).flatMap(e => (["pickupSteps", "admissionHeadroom"] as const).map(metric => {
    const leftValue = metric === "pickupSteps" ? chosen.candidate.pickupSteps : chosen.admissionHeadroom;
    const rightValue = metric === "pickupSteps" ? e.candidate.pickupSteps : e.admissionHeadroom;
    const identity = (row: CandidateEvidence) => ({ jobId: row.candidate.jobId, fromRobotId: row.candidate.fromRobotId, toRobotId: row.candidate.toRobotId });
    return { left: identity(chosen), right: identity(e), metric, leftValue, rightValue,
      relation: leftValue < rightValue ? "less" : leftValue > rightValue ? "greater" : "equal" };
  }));
}
export function recoveryDecisionFixture(state: FleetState, objective: DecisionObjective, recipient: string | null,
  basis?: Extract<RecoveryDecision["decision"], { kind: "select" }>["basis"]): RecoveryDecision {
  const considered = decisionEvidence(state), chosen = considered.find(e => e.candidate.toRobotId === recipient);
  if (recipient !== null && !chosen) throw new Error("Missing decision fixture candidate.");
  return { version: "recovery-decision-v2", observationId: state.observationId!, tick: state.tick, objective,
    incidents: state.activeIncidents.map(i => ({ reference: { scope: "snapshot", tick: state.tick, robotId: i.robotId, type: i.type }, severity: i.severity, telemetry: { ...i.telemetry } })),
    considered, comparisons: chosen ? decisionComparisons(considered, chosen) : [],
    decision: chosen ? { kind: "select", selected: { jobId: chosen.candidate.jobId, fromRobotId: chosen.candidate.fromRobotId, toRobotId: chosen.candidate.toRobotId },
      basis: basis ?? (objective === "minimize_pickup_steps" ? "pickup_cost" : objective === "preserve_battery_headroom" ? "battery_headroom" : "admitted_option") }
      : { kind: "abstain", reason: state.poisoned ? "runtime_poisoned" : considered.length ? "operator_tradeoff" : state.activeIncidents.some(i => i.type === "traffic_blocked") ? "no_candidates" : "no_supported_incident" },
    operatorAttention: state.activeIncidents.map(i => ({ scope: "snapshot", tick: state.tick, robotId: i.robotId, type: i.type })),
    limitations: [...decisionLimitations] };
}

// Local provider adapter for exercising Mastra's actual generation loop without HTTP.
export function offlineModelFixture(output: unknown, onCall: () => void = () => {}) {
  const text = JSON.stringify(output);
  const usage = { inputTokens: 1, outputTokens: 1, totalTokens: 2 };
  return {
    specificationVersion: "v2" as const, provider: "offline-fixture", modelId: "deterministic", supportedUrls: {},
    async doGenerate() {
      onCall();
      return { content: [{ type: "text" as const, text }], finishReason: "stop" as const, usage, warnings: [] };
    },
    async doStream() {
      onCall();
      return { stream: new ReadableStream({ start(controller) {
        controller.enqueue({ type: "stream-start", warnings: [] });
        controller.enqueue({ type: "text-start", id: "1" });
        controller.enqueue({ type: "text-delta", id: "1", delta: text });
        controller.enqueue({ type: "text-end", id: "1" });
        controller.enqueue({ type: "finish", finishReason: "stop", usage });
        controller.close();
      } }) };
    },
  } satisfies MastraModelConfig;
}
