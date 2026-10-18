import { parseProposal, type Proposal } from "./proposals";

export const metrics = ["pickupSteps", "deliverySteps", "chargingExitSteps", "totalRouteSteps", "battery", "requiredBattery", "missionHeadroom", "admissionHeadroom"] as const;
export type Metric = typeof metrics[number];
export type Source = "deterministic" | "mocked" | "recorded" | "live-model";
export type Objective = "compare_tradeoffs" | "minimize_pickup_steps" | "preserve_battery_headroom";
export interface CandidateKey { jobId: string; fromRobotId: string; toRobotId: string }
export interface RunCandidate { key: CandidateKey; metrics: Partial<Record<Metric, number>> }
export interface Comparison { left: CandidateKey; right: CandidateKey; metric: Metric; leftValue: number; rightValue: number; relation: "less" | "equal" | "greater" }
export interface RunDecision { kind: "select" | "abstain"; selected: CandidateKey | null; basis: string; reason: string; comparisons: Comparison[] }
export interface RunCompletion {
  status: "accepted" | "rejected" | "failed"; failure: string; latencyMs: number; modelRequests: number; toolExecutions: number;
  steps: { kind: "request" | "tool"; tool: string }[];
  reportedUsage: { status: "reported" | "partial" | "unavailable"; inputTokens: number | null; outputTokens: number | null; totalTokens: number | null };
  reportedActualModel: string; submissionCheck: string; decision: RunDecision | null;
}
export interface AgentRecord {
  id: string; runtimeId: string; observationId: string; startedAt: string; completedAt: string | null;
  serverSubmissionOutcome: string; serverElapsedMs: number | null; serverVerifiedDecision: boolean | null; estimatedCostStatus: "unavailable";
  agentReportedMetadata: { version: "agent-run-v1"; contract: "recovery-decision-v2"; objective: Objective; reportedSource: Source; requestedModel: string };
  serverVerifiedObservation: { tick: number; poisoned: boolean; blocked: boolean; incidents: { robotId: string; type: string; severity: string }[]; candidates: RunCandidate[] };
  agentReportedCompletion: RunCompletion | null; proposalId: string | null; proposal: Proposal | null;
}
export class AgentDataError extends Error { constructor() { super("Unsupported or malformed agent record."); } }
export const invalid = (): never => { throw new AgentDataError(); };
export const object = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v);
export function shape(v: unknown, keys: string): Record<string, unknown> {
  if (!object(v) || Object.keys(v).some(k => !keys.split(" ").includes(k))) return invalid(); return v;
}
export function number(v: unknown, max = Number.MAX_SAFE_INTEGER): number {
  if (typeof v !== "number" || !Number.isSafeInteger(v) || v < 0 || v > max) return invalid(); return v;
}
function text(v: unknown): string { if (typeof v !== "string") return invalid(); return v; }
export function id(v: unknown): string { const s = text(v); if (!/^[A-Za-z0-9_-]{1,128}$/.test(s)) return invalid(); return s; }
export function observation(v: unknown): string { const s = text(v); if (!/^[a-f0-9]{64}$/.test(s)) return invalid(); return s; }
function date(v: unknown): string { const s = text(v); if (s.length > 40 || !/^\d{4}-\d\d-\d\dT/.test(s) || !Number.isFinite(Date.parse(s))) return invalid(); return s; }
function flag(v: unknown): boolean { if (typeof v !== "boolean") return invalid(); return v; }
function member<T extends string>(v: unknown, values: readonly T[]): T { if (!values.includes(v as T)) return invalid(); return v as T; }
function list(v: unknown, limit: number, nullable = false): unknown[] {
  if (nullable && v === null) return []; if (!Array.isArray(v) || v.length > limit) return invalid(); return v;
}
function model(v: unknown): string {
  const s = text(v); if (s && (!/^[A-Za-z0-9._-]{1,32}\/[A-Za-z0-9._:-]{1,96}$/.test(s) || /sk-|secret|bearer|token/i.test(s))) return invalid(); return s;
}
export const keyString = (key: CandidateKey): string => `${key.jobId}/${key.fromRobotId}/${key.toRobotId}`;
function key(v: unknown): CandidateKey { const r = shape(v, "jobId fromRobotId toRobotId"); return { jobId: id(r.jobId), fromRobotId: id(r.fromRobotId), toRobotId: id(r.toRobotId) }; }
function candidate(v: unknown): RunCandidate {
  const r = shape(v, "key metrics"), data = shape(r.metrics, metrics.join(" "));
  const parsed: RunCandidate["metrics"] = {};
  for (const m of metrics) if (data[m] !== undefined) parsed[m] = number(data[m], ["battery", "requiredBattery", "missionHeadroom", "admissionHeadroom"].includes(m) ? 100 : Number.MAX_SAFE_INTEGER);
  return { key: key(r.key), metrics: parsed };
}
function comparison(v: unknown, candidates: RunCandidate[]): Comparison {
  const r = shape(v, "left right metric leftValue rightValue relation"), left = key(r.left), right = key(r.right), metric = member(r.metric, metrics);
  const leftValue = number(r.leftValue), rightValue = number(r.rightValue), relation = member(r.relation, ["less", "equal", "greater"] as const);
  if (keyString(left) === keyString(right) || candidates.find(c => keyString(c.key) === keyString(left))?.metrics[metric] !== leftValue ||
    candidates.find(c => keyString(c.key) === keyString(right))?.metrics[metric] !== rightValue || relation !== (leftValue < rightValue ? "less" : leftValue > rightValue ? "greater" : "equal")) return invalid();
  return { left, right, metric, leftValue, rightValue, relation };
}
function decision(v: unknown, candidates: RunCandidate[]): RunDecision {
  const r = shape(v, "kind selected basis reason comparisons"), kind = member(r.kind, ["select", "abstain"] as const);
  const selected = r.selected === null ? null : key(r.selected);
  const comparisons = list(r.comparisons, 128, true).map(c => comparison(c, candidates));
  const basis = member(r.basis, ["", "pickup_cost", "battery_headroom", "tradeoff", "admitted_option"]);
  const reason = member(r.reason, ["", "runtime_poisoned", "no_candidates", "no_supported_incident", "operator_tradeoff"]);
  if (kind === "select" ? !selected || !basis || reason !== "" || !candidates.some(c => keyString(c.key) === keyString(selected)) : selected !== null || basis !== "" || reason === "") return invalid();
  if (new Set(comparisons.map(c => `${keyString(c.left)}:${keyString(c.right)}:${c.metric}`)).size !== comparisons.length) return invalid();
  return { kind, selected, basis, reason, comparisons };
}
function completion(v: unknown, candidates: RunCandidate[]): RunCompletion {
  const r = shape(v, "steps submissionCheck status failure latencyMs modelRequests toolExecutions reportedUsage reportedActualModel decision");
  const status = member(r.status, ["accepted", "rejected", "failed"] as const);
  const failure = member(r.failure, ["none", "grounding_rejected", "timeout", "cancelled", "budget_exhausted", "invalid_tool", "invalid_usage", "investigation_failed", "snapshot_changed", "snapshot_unavailable"]);
  const d = r.decision === null ? null : decision(r.decision, candidates);
  if (status === "accepted" ? !d || failure !== "none" : d !== null || failure === "none") return invalid();
  const steps = list(r.steps, 14, true).map(v => { const s = shape(v, "kind tool"); const kind = member(s.kind, ["request", "tool"] as const);
    const tool = kind === "request" ? member(s.tool, [""]) : member(s.tool, ["readIncident", "readRobots", "readJobs", "listRecoveryCandidates"]); return { kind, tool }; });
  const usage = shape(r.reportedUsage, "status inputTokens outputTokens totalTokens"), usageStatus = member(usage.status, ["reported", "partial", "unavailable"] as const);
  const tokens = [usage.inputTokens, usage.outputTokens, usage.totalTokens].map(v => v === null ? null : number(v, 10_000_000));
  if (usageStatus === "unavailable" ? tokens.some(v => v !== null) : tokens.some(v => v === null) || tokens[2]! < tokens[0]! + tokens[1]!) return invalid();
  const modelRequests = number(r.modelRequests, 6), toolExecutions = number(r.toolExecutions, 8);
  if (steps.filter(s => s.kind === "request").length > modelRequests || steps.filter(s => s.kind === "tool").length > toolExecutions) return invalid();
  return { status, failure, decision: d, steps, latencyMs: number(r.latencyMs, 3_600_000), modelRequests, toolExecutions,
    reportedUsage: { status: usageStatus, inputTokens: tokens[0], outputTokens: tokens[1], totalTokens: tokens[2] },
    reportedActualModel: model(r.reportedActualModel), submissionCheck: member(r.submissionCheck, ["disabled", "not_requested", "current", "snapshot_changed", "unavailable"]) };
}
export function parseAgentRecord(value: unknown): AgentRecord {
  const outer = shape(value, "id runtimeId observationId data proposal");
  if (typeof outer.data !== "string" || new TextEncoder().encode(outer.data).length > 98_304) return invalid();
  let decoded: unknown; try { decoded = JSON.parse(outer.data); } catch { return invalid(); }
  const r = shape(decoded, "id runtimeId observationId startedAt completedAt agentReportedMetadata serverVerifiedObservation agentReportedCompletion serverVerifiedDecision estimatedCostStatus proposalId serverSubmissionOutcome serverElapsedMs");
  const runID = id(r.id), runtimeId = id(r.runtimeId), observationId = observation(r.observationId);
  if (runID !== outer.id || runtimeId !== outer.runtimeId || observationId !== outer.observationId) return invalid();
  const m = shape(r.agentReportedMetadata, "version contract objective reportedSource requestedModel");
  const metadata = { version: member(m.version, ["agent-run-v1"] as const), contract: member(m.contract, ["recovery-decision-v2"] as const),
    objective: member(m.objective, ["compare_tradeoffs", "minimize_pickup_steps", "preserve_battery_headroom"] as const),
    reportedSource: member(m.reportedSource, ["deterministic", "mocked", "recorded", "live-model"] as const), requestedModel: model(m.requestedModel) };
  const f = shape(r.serverVerifiedObservation, "tick poisoned blocked incidents candidates");
  const candidates = list(f.candidates, 64, true).map(candidate);
  if (new Set(candidates.map(c => keyString(c.key))).size !== candidates.length) return invalid();
  const incidents = list(f.incidents, 64, true).map(v => { const i = shape(v, "robotId type severity"); return { robotId: id(i.robotId),
    type: member(i.type, ["low_battery", "robot_unresponsive", "traffic_blocked"]), severity: member(i.severity, ["medium", "high"]) }; });
  const c = r.agentReportedCompletion === null ? null : completion(r.agentReportedCompletion, candidates);
  const startedAt = date(r.startedAt), completedAt = r.completedAt === null ? null : date(r.completedAt);
  const elapsed = r.serverElapsedMs === null ? null : number(r.serverElapsedMs);
  const verified = r.serverVerifiedDecision === undefined || r.serverVerifiedDecision === null ? null : flag(r.serverVerifiedDecision);
  if ((c === null) !== (completedAt === null) || completedAt !== null && Date.parse(completedAt) < Date.parse(startedAt) || c === null && elapsed !== null || verified === true && c?.status !== "accepted") return invalid();
  let proposal: Proposal | null = null;
  if (outer.proposal !== null) { try { proposal = parseProposal(outer.proposal); } catch { return invalid(); } }
  const proposalId = r.proposalId === null ? null : id(r.proposalId), selected = c?.decision?.selected;
  if ((proposal === null) !== (proposalId === null) || proposal && (proposal.id !== proposalId || !proposal.currentRuntime || proposal.action !== "reassign_unstarted_job" || !selected ||
    proposal.jobId !== selected.jobId || proposal.fromRobotId !== selected.fromRobotId || proposal.toRobotId !== selected.toRobotId)) return invalid();
  const outcome = member(r.serverSubmissionOutcome, ["", "not_completed", "not_requested", "pending_proposal_created", "stale_observation", "proposal_denied"]);
  if (proposal && outcome !== "pending_proposal_created" || !proposal && outcome === "pending_proposal_created") return invalid();
  return { id: runID, runtimeId, observationId, startedAt, completedAt, agentReportedMetadata: metadata,
    serverVerifiedObservation: { tick: number(f.tick), poisoned: flag(f.poisoned), blocked: flag(f.blocked), incidents, candidates },
    agentReportedCompletion: c, serverVerifiedDecision: verified, estimatedCostStatus: member(r.estimatedCostStatus, ["unavailable"] as const),
    serverSubmissionOutcome: outcome, serverElapsedMs: elapsed, proposalId, proposal };
}
