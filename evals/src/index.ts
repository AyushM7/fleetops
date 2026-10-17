import { isDeepStrictEqual as equal } from "node:util";
import { reasoningSchema, type AIReasoning, type FleetState, type SnapshotReference } from "../../agent/src/contracts.js";
import { validateSnapshot } from "../../agent/src/reasoning.js";
import { reasoningTextGrounded } from "../../agent/src/statements.js";

type Identity<T> = T extends unknown ? Omit<T, "evidence" | "rationale"> : never;
export type Action = Identity<NonNullable<AIReasoning["proposedAction"]>>;
export interface EvaluationCase {
  id: string;
  provenance: string;
  input: FleetState;
  expected: { incidents: SnapshotReference[]; action: Action | null };
}
export type Check = "pass" | "fail" | "not_applicable";
export type Metric = "incidentReferences" | "evidenceGrounding" | "recommendationValidity" |
  "unsupportedCommands" | "operatorAttention" | "appropriateAbstention";
export interface ActualOutput { reasoning: unknown }
export interface ScoringResult {
  id: string;
  status: "passed" | "failed" | "missing" | "malformed" | "invalid_case" | "adapter_error";
  checks: Record<Metric, Check>;
  issues: string[];
  outcomes: { proposalSubmission: "not_measured"; execution: "not_measured"; jobDelivery: "not_measured" };
}
export interface EvaluationReport {
  version: 1;
  mode: "offline-deterministic";
  results: ScoringResult[];
  totals: { cases: number; passed: number; unsuccessful: number };
  metrics: Record<Metric, { passed: number; failed: number; notApplicable: number }>;
}
export type OfflineAdapter = (input: FleetState, caseId: string) => Promise<ActualOutput>;
const metrics: Metric[] = ["incidentReferences", "evidenceGrounding", "recommendationValidity",
  "unsupportedCommands", "operatorAttention", "appropriateAbstention"];
const check = (value: boolean): Check => value ? "pass" : "fail";
const ref = (state: FleetState, i: FleetState["activeIncidents"][number]): SnapshotReference =>
  ({ scope: "snapshot", tick: state.tick, robotId: i.robotId, type: i.type });
function sameReferences(a: SnapshotReference[], b: SnapshotReference[]): boolean {
  return a.length === b.length && a.every(r => b.filter(other => equal(r, other)).length === 1) &&
    b.every(r => a.filter(other => equal(r, other)).length === 1);
}
function baseResult(id: string): ScoringResult {
  return { id, status: "failed", checks: Object.fromEntries(metrics.map(m => [m, "fail"])) as Record<Metric, Check>,
    issues: [], outcomes: { proposalSubmission: "not_measured", execution: "not_measured", jobDelivery: "not_measured" } };
}
function validCase(test: EvaluationCase): boolean {
  try {
    validateSnapshot(test.input);
    return test.id.trim().length > 0 && test.provenance.trim().length > 0 &&
      sameReferences(test.expected.incidents, test.input.activeIncidents.map(i => ref(test.input, i))) &&
      (test.expected.action === null || ["SetCharging", "ReassignUnstartedJob"].includes(test.expected.action.action));
  } catch { return false; }
}
function actionIdentity(action: NonNullable<AIReasoning["proposedAction"]>): Action {
  if (action.action === "SetCharging") return { action: action.action, robotId: action.robotId,
    stationId: action.stationId, purpose: action.purpose };
  return { action: action.action, jobId: action.jobId, fromRobotId: action.fromRobotId,
    toRobotId: action.toRobotId, purpose: action.purpose };
}
function groundedAction(action: AIReasoning["proposedAction"], state: FleetState): boolean {
  if (!action) return true;
  const e = action.evidence;
  if (e.tick !== state.tick) return false;
  if (action.action === "SetCharging" && "robot" in e) return (
    equal(e.robot, state.robots.find(r => r.id === action.robotId)) &&
    equal(e.station, state.stations.find(s => s.id === action.stationId)));
  if (action.action === "ReassignUnstartedJob" && "source" in e) return (
    equal(e.source, state.robots.find(r => r.id === action.fromRobotId)) &&
    equal(e.recipient, state.robots.find(r => r.id === action.toRobotId)) &&
    equal(e.job, state.jobs.find(j => j.id === action.jobId)) &&
    equal(e.candidate, state.reassignmentCandidates.find(c => c.jobId === action.jobId &&
      c.fromRobotId === action.fromRobotId && c.toRobotId === action.toRobotId)) &&
    state.activeIncidents.some(i => i.type === "traffic_blocked" && i.robotId === action.fromRobotId && equal(ref(state, i), e.reference)));
  return false;
}
function supportedCommand(raw: unknown): boolean {
  if (typeof raw !== "object" || raw === null || !("proposedAction" in raw)) return false;
  const action = raw.proposedAction;
  return action === null || (typeof action === "object" && "action" in action &&
    ["SetCharging", "ReassignUnstartedJob"].includes(String(action.action)));
}
export function scoreCase(test: EvaluationCase, actual: ActualOutput): ScoringResult {
  const result = baseResult(test.id);
  if (!validCase(test)) return { ...result, status: "invalid_case", issues: ["Invalid or ambiguous input/ground truth."] };
  const raw = actual?.reasoning;
  if (raw === undefined || raw === null) return { ...result, status: "missing", issues: ["Reasoning output missing."] };
  result.checks.unsupportedCommands = check(supportedCommand(raw));
  const parsed = reasoningSchema.safeParse(raw);
  if (!parsed.success) return { ...result, status: "malformed", issues: ["Output violates the strict Phase 4 reasoning contract."] };
  const reasoning = parsed.data;
  result.checks.incidentReferences = check(reasoning.tick === test.input.tick &&
    sameReferences(reasoning.interpretations.map(i => i.reference), test.expected.incidents));
  result.checks.evidenceGrounding = check(reasoning.tick === test.input.tick && reasoning.interpretations.every(i => {
    const incident = test.input.activeIncidents.find(other => equal(ref(test.input, other), i.reference));
    return incident !== undefined && incident.severity === i.severity && equal(incident.telemetry, i.telemetry);
  }) && groundedAction(reasoning.proposedAction, test.input) && reasoningTextGrounded(reasoning, test.input));
  result.checks.recommendationValidity = check(equal(reasoning.proposedAction ? actionIdentity(reasoning.proposedAction) : null, test.expected.action));
  result.checks.operatorAttention = test.expected.incidents.length === 0 ? "not_applicable" :
    check(result.checks.incidentReferences === "pass" && reasoning.interpretations.every(i => i.recommendedResponse === "operator_attention"));
  result.checks.appropriateAbstention = test.expected.action !== null ? "not_applicable" :
    check(reasoning.proposedAction === null && result.checks.incidentReferences === "pass" &&
      result.checks.evidenceGrounding === "pass");
  result.issues = metrics.filter(m => result.checks[m] === "fail").map(m => `${m} failed.`);
  result.status = result.issues.length === 0 ? "passed" : "failed";
  return result;
}
export async function runEvaluation(cases: EvaluationCase[], adapter: OfflineAdapter): Promise<EvaluationReport> {
  const results: ScoringResult[] = [];
  for (const test of cases) {
    if (cases.filter(c => c.id === test.id).length !== 1 || !validCase(test)) {
      results.push({ ...baseResult(test.id), status: "invalid_case", issues: ["Invalid or ambiguous input/ground truth or duplicate case ID."] });
      continue;
    }
    try {
      results.push(scoreCase(test, await adapter(structuredClone(test.input), test.id)));
    } catch {
      results.push({ ...baseResult(test.id), status: "adapter_error", issues: ["Offline adapter failed."] });
    }
  }
  const totals = { cases: results.length, passed: results.filter(r => r.status === "passed").length, unsuccessful: 0 };
  totals.unsuccessful = totals.cases - totals.passed;
  const counts = Object.fromEntries(metrics.map(m => [m, {
    passed: results.filter(r => r.checks[m] === "pass").length,
    failed: results.filter(r => r.checks[m] === "fail").length,
    notApplicable: results.filter(r => r.checks[m] === "not_applicable").length,
  }])) as EvaluationReport["metrics"];
  return { version: 1, mode: "offline-deterministic", results, totals, metrics: counts };
}
export function summarize(report: EvaluationReport): string {
  return [`Offline deterministic contract checks: ${report.totals.passed}/${report.totals.cases} passed; ${report.totals.unsuccessful} unsuccessful.`,
    ...report.results.map(r => `${r.id}: ${r.status}${r.issues.length ? ` (${r.issues.join(" ")})` : ""}`),
    "Proposal submission, execution and delivery: not measured. Live LLM accuracy: not measured."].join("\n");
}
