import type { FleetState, SnapshotReference } from "./contracts.js";
import { sameFacts, snapshotReference, validateSnapshot } from "./reasoning.js";
import { requireGrounded, schemaFailure } from "./recovery-grounding.js";
import { decisionLimitations, decisionObjectiveSchema, recoveryDecisionSchema, type CandidateComparison, type CandidateEvidence, type CandidateKey, type DecisionMetric, type DecisionObjective, type RecoveryDecision } from "./decision-contracts.js";

export function candidateKey(candidate: CandidateKey): CandidateKey {
  return { jobId: candidate.jobId, fromRobotId: candidate.fromRobotId, toRobotId: candidate.toRobotId };
}
function key(candidate: CandidateKey): string { return JSON.stringify(candidateKey(candidate)); }
function equalSet<T>(actual: T[], expected: T[]): boolean {
  return actual.length === expected.length && actual.every(item => expected.filter(other => sameFacts(item, other)).length === 1) &&
    expected.every(item => actual.filter(other => sameFacts(item, other)).length === 1);
}
function incidentReferences(state: FleetState): SnapshotReference[] {
  return state.activeIncidents.map(i => snapshotReference(state, i));
}
function validateIncidents(output: RecoveryDecision, state: FleetState): void {
  const expected = state.activeIncidents.map(i => ({ reference: snapshotReference(state, i), severity: i.severity, telemetry: i.telemetry }));
  requireGrounded(equalSet(output.incidents, expected), "incident", "incidents", "fact_mismatch",
    "contains every trusted incident and its exact severity and telemetry once", output.incidents, "readIncident");
  requireGrounded(equalSet(output.operatorAttention, incidentReferences(state)), "incident", "operatorAttention", "fact_mismatch",
    "contains every trusted incident reference once", output.operatorAttention, "readIncident");
  requireGrounded(equalSet(output.limitations, [...decisionLimitations]), "decision", "limitations", "constraint_violation",
    "contains each required limitation once", output.limitations);
}
function requireFacts(actual: unknown, expected: unknown, path: string, constraint: string,
  tool: "listRecoveryCandidates" | "readRobots" | "readJobs" | "readIncident"): void {
  if (sameFacts(actual, expected)) return;
  if (actual !== null && expected !== null && typeof actual === "object" && typeof expected === "object") {
    const a = actual as Record<string, unknown>, e = expected as Record<string, unknown>;
    const mismatch = Object.keys(e).find(field => !sameFacts(a[field], e[field]));
    if (mismatch !== undefined) {
      requireFacts(a[mismatch], e[mismatch], path + "." + mismatch, constraint, tool);
      return;
    }
  }
  requireGrounded(false, "candidate", path, "fact_mismatch", constraint, actual, tool);
}

function validateEvidence(evidence: CandidateEvidence, state: FleetState, index: number): void {
  const path = "considered[" + index + "].";
  const c = evidence.candidate;
  const admitted = state.reassignmentCandidates.find(other => key(other) === key(c));
  requireFacts(c, admitted, path + "candidate", "matches the trusted candidate key and route metrics", "listRecoveryCandidates");
  requireFacts(evidence.source, state.robots.find(r => r.id === c.fromRobotId), path + "source", "matches the source robot snapshot", "readRobots");
  requireFacts(evidence.recipient, state.robots.find(r => r.id === c.toRobotId), path + "recipient", "matches the recipient robot snapshot", "readRobots");
  requireFacts(evidence.job, state.jobs.find(j => j.id === c.jobId), path + "job", "matches the job snapshot and assignment", "readJobs");
  const incident = state.activeIncidents.find(i => i.robotId === c.fromRobotId && i.type === "traffic_blocked" && sameFacts(snapshotReference(state, i), evidence.incidentReference));
  requireGrounded(!!incident, "candidate", path + "incidentReference", "fact_mismatch",
    "matches the source robot's traffic incident", evidence.incidentReference, "readIncident");
  const total = c.pickupSteps + c.deliverySteps + c.chargingExitSteps;
  requireGrounded(Number.isSafeInteger(total) && evidence.totalRouteSteps === total, "candidate", path + "totalRouteSteps", "fact_mismatch",
    "equals pickup plus delivery plus charging-exit steps", evidence.totalRouteSteps, "listRecoveryCandidates");
  requireGrounded(evidence.missionHeadroom === evidence.recipient.battery - c.requiredBattery, "candidate", path + "missionHeadroom", "fact_mismatch",
    "equals recipient battery minus the required battery", evidence.missionHeadroom, "readRobots");
  requireGrounded(evidence.admissionHeadroom === evidence.recipient.battery - Math.max(30, c.requiredBattery), "candidate", path + "admissionHeadroom", "fact_mismatch",
    "equals recipient battery minus the larger of 30 and the required battery", evidence.admissionHeadroom, "readRobots");
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
  for (const [index, c] of output.comparisons.entries()) {
    const path = "comparisons[" + index + "]";
    const left = evidence.get(key(c.left)), right = evidence.get(key(c.right));
    requireGrounded(!!left, "comparison", path + ".left", "evidence_missing", "references a listed candidate", c.left, "listRecoveryCandidates");
    requireGrounded(!!right && key(c.left) !== key(c.right), "comparison", path + ".right", "constraint_violation",
      "references a listed candidate distinct from left", c.right, "listRecoveryCandidates");
    const identity = JSON.stringify([key(c.left), key(c.right), c.metric]);
    requireGrounded(!seen.has(identity), "comparison", path, "duplicate_evidence", "candidate pair and metric are unique", c);
    seen.add(identity);
    const l = metricValue(left, c.metric), r = metricValue(right, c.metric);
    const tool = ["battery", "missionHeadroom", "admissionHeadroom"].includes(c.metric) ? "readRobots" : "listRecoveryCandidates";
    requireGrounded(c.leftValue === l, "comparison", path + ".leftValue", "fact_mismatch",
      "equals the trusted metric for the left candidate", c.leftValue, tool);
    requireGrounded(c.rightValue === r, "comparison", path + ".rightValue", "fact_mismatch",
      "equals the trusted metric for the right candidate", c.rightValue, tool);
    requireGrounded(c.relation === (l < r ? "less" : l > r ? "greater" : "equal"), "comparison", path + ".relation", "fact_mismatch",
      "expresses the ordering of verified values", c.relation, tool);
  }
}
function tradeoffExists(evidence: CandidateEvidence[]): boolean {
  if (evidence.length < 2) return false;
  const pickup = Math.min(...evidence.map(e => e.candidate.pickupSteps));
  const headroom = Math.max(...evidence.map(e => e.admissionHeadroom));
  return !evidence.some(e => e.candidate.pickupSteps === pickup && e.admissionHeadroom === headroom);
}
function validateAbstention(output: RecoveryDecision, state: FleetState): void {
  requireGrounded(output.decision.kind === "abstain", "decision", "decision.kind", "constraint_violation", "uses the abstention variant", output.decision.kind);
  const reason = output.decision.reason;
  const checkReason = (valid: boolean, expected: string) =>
    requireGrounded(valid, "decision", "decision.reason", "constraint_violation", expected, reason, "readIncident");
  switch (output.decision.reason) {
    case "runtime_poisoned": checkReason(state.poisoned, "the runtime is poisoned"); return;
    case "no_candidates": checkReason(!state.poisoned && !output.considered.length && state.activeIncidents.some(i => i.type === "traffic_blocked"), "a traffic incident has no eligible candidates"); return;
    case "no_supported_incident": checkReason(!state.poisoned && !output.considered.length && !state.activeIncidents.some(i => i.type === "traffic_blocked"), "no supported traffic incident or candidates are present"); return;
    case "operator_tradeoff": checkReason(!state.poisoned && output.objective === "compare_tradeoffs" && tradeoffExists(output.considered), "the objective and evidence support a tradeoff");
  }
}
function relativeBenefit(c: CandidateComparison, selected: CandidateKey): number {
  const difference = key(c.left) === key(selected) ? c.leftValue - c.rightValue : c.rightValue - c.leftValue;
  return ["battery", "missionHeadroom", "admissionHeadroom"].includes(c.metric) ? difference : -difference;
}
function validateSelection(output: RecoveryDecision, state: FleetState): void {
  requireGrounded(output.decision.kind === "select", "decision", "decision.kind", "constraint_violation", "uses the selection variant", output.decision.kind);
  const { selected, basis } = output.decision;
  const chosen = output.considered.find(e => key(e.candidate) === key(selected));
  requireGrounded(!state.poisoned && !!chosen, "decision", "decision.selected", "constraint_violation",
    "selects an eligible candidate in an unpoisoned runtime", selected, "listRecoveryCandidates");
  const comparisons = output.comparisons.filter(c => key(c.left) === key(selected) || key(c.right) === key(selected));
  const alternatives = output.considered.filter(e => key(e.candidate) !== key(selected));
  const checkComparisons = (valid: boolean, expected: string) =>
    requireGrounded(valid, "decision", "comparisons", "evidence_missing", expected, comparisons, "listRecoveryCandidates");
  checkComparisons(alternatives.every(e => comparisons.some(c => key(c.left) === key(e.candidate) || key(c.right) === key(e.candidate))),
    "compares the selected candidate with every alternative");
  const leastPickup = alternatives.every(e => chosen.candidate.pickupSteps <= e.candidate.pickupSteps);
  const mostHeadroom = alternatives.every(e => chosen.admissionHeadroom >= e.admissionHeadroom);
  const checkBasis = (valid: boolean, expected: string) =>
    requireGrounded(valid, "decision", "decision.basis", "constraint_violation", expected, basis, "listRecoveryCandidates");
  if (output.objective === "minimize_pickup_steps") checkBasis(leastPickup && basis === "pickup_cost", "selects a minimum pickup with pickup_cost basis");
  if (output.objective === "preserve_battery_headroom") checkBasis(mostHeadroom && basis === "battery_headroom", "selects maximum admission headroom with battery_headroom basis");
  if (basis === "pickup_cost") checkComparisons(leastPickup && alternatives.every(e => comparisons.some(c => c.metric === "pickupSteps" && (key(c.left) === key(e.candidate) || key(c.right) === key(e.candidate)))), "compares pickup steps against every alternative and establishes the minimum");
  if (basis === "battery_headroom") checkComparisons(mostHeadroom && alternatives.every(e => comparisons.some(c => c.metric === "admissionHeadroom" && (key(c.left) === key(e.candidate) || key(c.right) === key(e.candidate)))), "compares admission headroom against every alternative and establishes the maximum");
  if (basis === "tradeoff") checkBasis(comparisons.some(c => relativeBenefit(c, selected) > 0) && comparisons.some(c => relativeBenefit(c, selected) < 0), "establishes both an advantage and a disadvantage");
}
export function validateRecoveryDecision(input: unknown, state: FleetState, objective: DecisionObjective): RecoveryDecision {
  const snapshot = validateSnapshot(state);
  const parsed = recoveryDecisionSchema.safeParse(input);
  if (!parsed.success) schemaFailure(parsed.error.issues[0], input);
  const output = parsed.data;
  requireGrounded(!!snapshot.observationId && output.observationId === snapshot.observationId, "observation", "observationId",
    "identity_mismatch", "equals the trusted observation ID", output.observationId);
  requireGrounded(output.tick === snapshot.tick, "observation", "tick", "identity_mismatch", "equals the trusted tick", output.tick);
  requireGrounded(decisionObjectiveSchema.safeParse(objective).success && output.objective === objective, "observation", "objective",
    "identity_mismatch", "equals the declared objective", output.objective);
  validateIncidents(output, snapshot);
  requireGrounded(equalSet(output.considered.map(e => candidateKey(e.candidate)), snapshot.reassignmentCandidates.map(candidateKey)),
    "candidate", "considered", "evidence_missing", "includes every trusted candidate once", output.considered, "listRecoveryCandidates");
  output.considered.forEach((e, index) => validateEvidence(e, snapshot, index));
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
