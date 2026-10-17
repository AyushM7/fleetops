import type { AIReasoning, FleetState, MutationResult } from "./contracts.js";
import type { MastraModelConfig } from "@mastra/core/llm";

export function fleetFixture(battery = 100): FleetState {
  return { tick: 0, poisoned: false, activeIncidents: [],
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
