import { isDeepStrictEqual } from "node:util";
import { fleetSchema, reasoningSchema, type AIReasoning, type AgentResult, type FleetState, type SnapshotReference } from "./contracts.js";
import { observeFleet } from "./observe.js";

export class GroundingError extends Error {
  constructor() { super("AI grounding validation rejected the response or snapshot."); }
}
export function sameFacts(left: unknown, right: unknown): boolean {
  return isDeepStrictEqual(left, right);
}
export function snapshotReference(state: FleetState, incident: FleetState["activeIncidents"][number]): SnapshotReference {
  return { scope: "snapshot", tick: state.tick, robotId: incident.robotId, type: incident.type };
}
export function validateSnapshot(input: unknown): FleetState {
  const parsed = fleetSchema.safeParse(input);
  if (!parsed.success) throw new GroundingError();
  const state = parsed.data;
  if (new Set(state.robots.map(r => r.id)).size !== state.robots.length ||
      new Set(state.stations.map(s => s.id)).size !== state.stations.length) throw new GroundingError();
  const references = new Set<string>();
  for (const incident of state.activeIncidents) {
    const key = JSON.stringify(snapshotReference(state, incident));
    const robot = state.robots.find(r => r.id === incident.robotId);
    if (references.has(key) || !robot || !sameFacts(robot, incident.telemetry)) throw new GroundingError();
    references.add(key);
  }
  return state;
}
export function observedFacts(state: FleetState): AgentResult["observedFacts"] {
  return { referenceScope: "snapshot",
    referenceNotice: "Snapshot-scoped references, not stable incident IDs. Not valid across separate simulator runs.",
    fleet: state, incidentReferences: state.activeIncidents.map(i => snapshotReference(state, i)) };
}
function validateInterpretations(reasoning: AIReasoning, state: FleetState): void {
  if (reasoning.tick !== state.tick || reasoning.interpretations.length !== state.activeIncidents.length) throw new GroundingError();
  const seen = new Set<string>();
  for (const interpretation of reasoning.interpretations) {
    const { reference, telemetry, severity } = interpretation;
    const matches = state.activeIncidents.filter(i => sameFacts(snapshotReference(state, i), reference));
    const key = JSON.stringify(reference);
    if (seen.has(key) || matches.length !== 1 || severity !== matches[0].severity ||
        !sameFacts(telemetry, matches[0].telemetry)) throw new GroundingError();
    seen.add(key);
  }
}
export function validateReasoning(input: unknown, state: FleetState, demo?: { robotId: string; stationId: string }): AIReasoning {
  const snapshot = validateSnapshot(state);
  const parsed = reasoningSchema.safeParse(input);
  if (!parsed.success) throw new GroundingError();
  const reasoning = parsed.data;
  validateInterpretations(reasoning, snapshot);
  const action = reasoning.proposedAction;
  if (!action) return reasoning;
  const supported = observeFleet(snapshot, demo).recommendedAction;
  if (!supported || action.robotId !== supported.robotId || action.stationId !== supported.stationId ||
      action.purpose !== (demo ? "demonstration" : "routine_charging") || action.evidence.tick !== snapshot.tick ||
      !sameFacts(action.evidence.robot, snapshot.robots.find(r => r.id === action.robotId)) ||
      !sameFacts(action.evidence.station, snapshot.stations.find(s => s.id === action.stationId))) throw new GroundingError();
  return reasoning;
}
