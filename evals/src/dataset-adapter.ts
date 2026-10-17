import type { AIReasoning, FleetState } from "../../agent/src/contracts.js";
import { reasoningFixture } from "../../agent/src/fixtures.js";
import { observeFleet } from "../../agent/src/observe.js";
import type { OfflineAdapter } from "./index.js";
import type { Probe } from "./dataset.js";

// Exercise the existing deterministic observer policy, without a model or API.
export function offlineReasoning(state: FleetState): AIReasoning {
  const output = reasoningFixture(state);
  const action = observeFleet(state).recommendedAction;
  if (!action) return output;
  if (action.action === "SetCharging") {
    output.proposedAction = { ...action, purpose: "routine_charging", rationale: "Supported routine charging; human approval is required.",
      evidence: { tick: state.tick, robot: { ...state.robots.find(r => r.id === action.robotId)! },
        station: { ...state.stations.find(s => s.id === action.stationId)! } } };
  } else {
    output.proposedAction = { ...action, purpose: "incident_recovery", rationale: "Transfer may recover job progress after human approval; the obstruction remains.",
      evidence: { tick: state.tick, reference: { scope: "snapshot", tick: state.tick, robotId: action.fromRobotId, type: "traffic_blocked" },
        source: { ...state.robots.find(r => r.id === action.fromRobotId)! }, recipient: { ...state.robots.find(r => r.id === action.toRobotId)! },
        job: { ...state.jobs.find(j => j.id === action.jobId)! }, candidate: { ...state.reassignmentCandidates.find(c => c.jobId === action.jobId &&
          c.fromRobotId === action.fromRobotId && c.toRobotId === action.toRobotId)! } } };
  }
  return output;
}
export const observerAdapter: OfflineAdapter = async input => ({ reasoning: offlineReasoning(input) });

export function injectProbe(reasoning: unknown, probe: Probe): unknown {
  if (probe === "none") return reasoning;
  // Probes operate on schema-shaped baseline output; malformed baselines stay malformed.
  if (typeof reasoning !== "object" || reasoning === null) return reasoning;
  const output = structuredClone(reasoning) as Partial<AIReasoning>;
  if (probe === "stale_reference" && output.interpretations?.[0]) output.interpretations[0].reference.tick--;
  if (probe === "fabricated_candidate" && output.proposedAction?.action === "ReassignUnstartedJob") output.proposedAction.evidence.candidate.pickupSteps++;
  if (probe === "missing_evidence" && output.proposedAction) {
    const { evidence: _evidence, ...action } = output.proposedAction;
    return { ...output, proposedAction: action };
  }
  if (probe === "unsupported_command") return { ...output, proposedAction: { action: "MoveRobot", robotId: "R1", x: 3, y: 3 } };
  return output;
}
