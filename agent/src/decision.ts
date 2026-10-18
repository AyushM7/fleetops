import type { FleetState, SnapshotReference } from "./contracts.js";
import { GroundingError, sameFacts, snapshotReference, validateSnapshot } from "./reasoning.js";
import { decisionLimitations, decisionObjectiveSchema, recoveryDecisionSchema, type CandidateComparison, type CandidateEvidence, type CandidateKey, type DecisionMetric, type DecisionObjective, type RecoveryDecision } from "./decision-contracts.js";

export function candidateKey(candidate: CandidateKey): CandidateKey {
  return { jobId: candidate.jobId, fromRobotId: candidate.fromRobotId, toRobotId: candidate.toRobotId };
}
function key(candidate: CandidateKey): string { return JSON.stringify(candidateKey(candidate)); }
function requireGrounded(condition: boolean): void { if (!condition) throw new GroundingError(); }
function equalSet<T>(actual: T[], expected: T[]): boolean {
  return actual.length === expected.length && actual.every(item => expected.filter(other => sameFacts(item, other)).length === 1) &&
    expected.every(item => actual.filter(other => sameFacts(item, other)).length === 1);
}
function incidentReferences(state: FleetState): SnapshotReference[] {
  return state.activeIncidents.map(i => snapshotReference(state, i));
}
function validateIncidents(output: RecoveryDecision, state: FleetState): void {
  const expected = state.activeIncidents.map(i => ({ reference: snapshotReference(state, i), severity: i.severity, telemetry: i.telemetry }));
  requireGrounded(equalSet(output.incidents, expected) && equalSet(output.operatorAttention, incidentReferences(state)));
  requireGrounded(equalSet(output.limitations, [...decisionLimitations]));
}
function validateEvidence(evidence: CandidateEvidence, state: FleetState): void {
  const c = evidence.candidate;
  const admitted = state.reassignmentCandidates.find(other => key(other) === key(c));
  requireGrounded(!!admitted && sameFacts(c, admitted));
  requireGrounded(sameFacts(evidence.source, state.robots.find(r => r.id === c.fromRobotId)) &&
    sameFacts(evidence.recipient, state.robots.find(r => r.id === c.toRobotId)) && sameFacts(evidence.job, state.jobs.find(j => j.id === c.jobId)));
  requireGrounded(state.activeIncidents.some(i => i.robotId === c.fromRobotId && i.type === "traffic_blocked" && sameFacts(snapshotReference(state, i), evidence.incidentReference)));
  const total = c.pickupSteps + c.deliverySteps + c.chargingExitSteps;
  requireGrounded(Number.isSafeInteger(total) && evidence.totalRouteSteps === total &&
    evidence.missionHeadroom === evidence.recipient.battery - c.requiredBattery &&
    evidence.admissionHeadroom === evidence.recipient.battery - Math.max(30, c.requiredBattery));
}
export function metricValue(e: CandidateEvidence, metric: DecisionMetric): number {
  switch (metric) {
    case "battery": return e.recipient.battery;
    case "totalRouteSteps": case "missionHeadroom": case "admissionHeadroom": return e[metric];
    default: return e.candidate[metric];
  }
}
function validateComparisons(output: RecoveryDecision): void {
  const evidence = new Map(output.considered.map(e => [key(e.candidate), e]));
  const seen = new Set<string>();
  for (const c of output.comparisons) {
    const left = evidence.get(key(c.left)), right = evidence.get(key(c.right));
    requireGrounded(!!left && !!right && key(c.left) !== key(c.right));
    if (!left || !right) throw new GroundingError();
    const identity = JSON.stringify([key(c.left), key(c.right), c.metric]);
    requireGrounded(!seen.has(identity)); seen.add(identity);
    const l = metricValue(left, c.metric), r = metricValue(right, c.metric);
    requireGrounded(c.leftValue === l && c.rightValue === r && c.relation === (l < r ? "less" : l > r ? "greater" : "equal"));
  }
}
function tradeoffExists(evidence: CandidateEvidence[]): boolean {
  if (evidence.length < 2) return false;
  const pickup = Math.min(...evidence.map(e => e.candidate.pickupSteps));
  const headroom = Math.max(...evidence.map(e => e.admissionHeadroom));
  return !evidence.some(e => e.candidate.pickupSteps === pickup && e.admissionHeadroom === headroom);
}
function validateAbstention(output: RecoveryDecision, state: FleetState): void {
  if (output.decision.kind !== "abstain") throw new GroundingError();
  switch (output.decision.reason) {
    case "runtime_poisoned": requireGrounded(state.poisoned); return;
    case "no_candidates": requireGrounded(!state.poisoned && !output.considered.length && state.activeIncidents.some(i => i.type === "traffic_blocked")); return;
    case "no_supported_incident": requireGrounded(!state.poisoned && !output.considered.length && !state.activeIncidents.some(i => i.type === "traffic_blocked")); return;
    case "operator_tradeoff": requireGrounded(!state.poisoned && output.objective === "compare_tradeoffs" && tradeoffExists(output.considered));
  }
}
function relativeBenefit(c: CandidateComparison, selected: CandidateKey): number {
  const difference = key(c.left) === key(selected) ? c.leftValue - c.rightValue : c.rightValue - c.leftValue;
  return ["battery", "missionHeadroom", "admissionHeadroom"].includes(c.metric) ? difference : -difference;
}
function validateSelection(output: RecoveryDecision, state: FleetState): void {
  if (output.decision.kind !== "select") throw new GroundingError();
  const { selected, basis } = output.decision;
  const chosen = output.considered.find(e => key(e.candidate) === key(selected));
  requireGrounded(!state.poisoned && !!chosen);
  if (!chosen) throw new GroundingError();
  const comparisons = output.comparisons.filter(c => key(c.left) === key(selected) || key(c.right) === key(selected));
  const alternatives = output.considered.filter(e => key(e.candidate) !== key(selected));
  requireGrounded(alternatives.every(e => comparisons.some(c => key(c.left) === key(e.candidate) || key(c.right) === key(e.candidate))));
  const leastPickup = alternatives.every(e => chosen.candidate.pickupSteps <= e.candidate.pickupSteps);
  const mostHeadroom = alternatives.every(e => chosen.admissionHeadroom >= e.admissionHeadroom);
  if (output.objective === "minimize_pickup_steps") requireGrounded(leastPickup && basis === "pickup_cost");
  if (output.objective === "preserve_battery_headroom") requireGrounded(mostHeadroom && basis === "battery_headroom");
  if (basis === "pickup_cost") requireGrounded(leastPickup && alternatives.every(e => comparisons.some(c => c.metric === "pickupSteps" && (key(c.left) === key(e.candidate) || key(c.right) === key(e.candidate)))));
  if (basis === "battery_headroom") requireGrounded(mostHeadroom && alternatives.every(e => comparisons.some(c => c.metric === "admissionHeadroom" && (key(c.left) === key(e.candidate) || key(c.right) === key(e.candidate)))));
  if (basis === "tradeoff") requireGrounded(comparisons.some(c => relativeBenefit(c, selected) > 0) && comparisons.some(c => relativeBenefit(c, selected) < 0));
}
export function validateRecoveryDecision(input: unknown, state: FleetState, objective: DecisionObjective): RecoveryDecision {
  const snapshot = validateSnapshot(state);
  const parsed = recoveryDecisionSchema.safeParse(input);
  if (!parsed.success) throw new GroundingError();
  const output = parsed.data;
  requireGrounded(!!snapshot.observationId && output.observationId === snapshot.observationId && output.tick === snapshot.tick &&
    decisionObjectiveSchema.safeParse(objective).success && output.objective === objective);
  validateIncidents(output, snapshot);
  requireGrounded(equalSet(output.considered.map(e => candidateKey(e.candidate)), snapshot.reassignmentCandidates.map(candidateKey)));
  output.considered.forEach(e => validateEvidence(e, snapshot));
  validateComparisons(output);
  if (output.decision.kind === "abstain") validateAbstention(output, snapshot);
  else validateSelection(output, snapshot);
  return output;
}

export function explainRecoveryDecision(input: unknown, state: FleetState, objective: DecisionObjective): string {
  const output = validateRecoveryDecision(input, state, objective);
  const decision = output.decision;
  const choice = decision.kind === "abstain" ? `Abstained: ${decision.reason}.` :
    `Selected ${JSON.stringify(decision.selected)} using ${decision.basis}.`;
  const comparisons = output.comparisons.map(c => `${JSON.stringify(c.left)} ${c.metric} ${c.leftValue} ${c.relation} ${JSON.stringify(c.right)} ${c.rightValue}.`);
  return [choice, ...comparisons, "Snapshot evidence only; routes are not reserved. Reassignment does not remove an obstruction or guarantee delivery. Independent operator approval is required; optimality is not established."].join(" ");
}
