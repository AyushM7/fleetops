import { isDeepStrictEqual } from "node:util";
import { fleetSchema, reasoningSchema, type AIReasoning, type AgentResult, type FleetState, type SnapshotReference } from "./contracts.js";
import { observeFleet } from "./observe.js";
import { reasoningTextGrounded } from "./statements.js";

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
      new Set(state.stations.map(s => s.id)).size !== state.stations.length ||
      new Set(state.jobs.map(j => j.id)).size !== state.jobs.length) throw new GroundingError();
  const references = new Set<string>();
  for (const incident of state.activeIncidents) {
    const key = JSON.stringify(snapshotReference(state, incident));
    const robot = state.robots.find(r => r.id === incident.robotId);
    if (references.has(key) || !robot || !sameFacts(robot, incident.telemetry)) throw new GroundingError();
    references.add(key);
  }
  validateCandidates(state);
  return state;
}
function validateCandidates(state: FleetState): void {
  const seen = new Set<string>();
  for (const candidate of state.reassignmentCandidates) {
    const key = JSON.stringify([candidate.jobId, candidate.fromRobotId, candidate.toRobotId]);
    const source = state.robots.find(r => r.id === candidate.fromRobotId);
    const recipient = state.robots.find(r => r.id === candidate.toRobotId);
    const job = state.jobs.find(j => j.id === candidate.jobId);
    if (seen.has(key) || !source || !recipient || source.id === recipient.id || !job ||
        source.status !== "moving" || source.battery < 30 || !source.blocked || source.jobId !== job.id ||
        !state.activeIncidents.some(i => i.robotId === source.id && i.type === "traffic_blocked") ||
        job.status !== "assigned" || job.assignedRobot !== source.id || (source.x === job.fromX && source.y === job.fromY) ||
        recipient.status !== "idle" || recipient.jobId !== "" || recipient.battery < Math.max(30, candidate.requiredBattery) ||
        state.robots.some(r => r.id !== source.id && r.jobId === job.id)) throw new GroundingError();
    seen.add(key);
  }
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
  if (!reasoningTextGrounded(reasoning, snapshot)) throw new GroundingError();
  const action = reasoning.proposedAction;
  if (!action) return reasoning;
  const supported = observeFleet(snapshot, demo).recommendedAction;
  if (!supported || action.action !== supported.action || action.evidence.tick !== snapshot.tick) throw new GroundingError();
  if (action.action === "ReassignUnstartedJob") {
    if (supported.action !== action.action || action.jobId !== supported.jobId || action.fromRobotId !== supported.fromRobotId || action.toRobotId !== supported.toRobotId ||
        !sameFacts(action.evidence.source, snapshot.robots.find(r => r.id === action.fromRobotId)) ||
        !sameFacts(action.evidence.recipient, snapshot.robots.find(r => r.id === action.toRobotId)) ||
        !sameFacts(action.evidence.job, snapshot.jobs.find(j => j.id === action.jobId)) ||
        !sameFacts(action.evidence.candidate, snapshot.reassignmentCandidates.find(c => c.jobId === action.jobId && c.fromRobotId === action.fromRobotId && c.toRobotId === action.toRobotId)) ||
        !snapshot.activeIncidents.some(i => i.type === "traffic_blocked" && i.robotId === action.fromRobotId && sameFacts(snapshotReference(snapshot, i), action.evidence.reference))) throw new GroundingError();
    return reasoning;
  }
  if (supported.action !== "SetCharging" || action.robotId !== supported.robotId || action.stationId !== supported.stationId ||
      action.purpose !== (demo ? "demonstration" : "routine_charging") || action.evidence.tick !== snapshot.tick ||
      !sameFacts(action.evidence.robot, snapshot.robots.find(r => r.id === action.robotId)) ||
      !sameFacts(action.evidence.station, snapshot.stations.find(s => s.id === action.stationId))) throw new GroundingError();
  return reasoning;
}
