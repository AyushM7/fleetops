import type { JSX } from "react";
import { parseSnapshot, type FleetSnapshot, type Robot } from "./fleet";
import type { Proposal } from "./proposals";
import { isScenarioID, type ScenarioID } from "./scenarios";

export interface ExplanationEvidence {
  generation: string; runtimeId: string; scenarioId: ScenarioID; proposalId: string; observation: FleetSnapshot;
}
export interface EvidenceContext {
  generation: string; scenarioId?: ScenarioID; evidence?: ExplanationEvidence; current: FleetSnapshot | null; available: boolean;
}
const record = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null;
export function parseEvidence(raw: unknown, generation: string, scenario: ScenarioID | undefined): ExplanationEvidence {
  if (!record(raw) || raw.generation !== generation || raw.scenarioId !== scenario || !isScenarioID(raw.scenarioId) ||
      typeof raw.runtimeId !== "string" || !/^P[a-f0-9]{32}$/.test(raw.runtimeId) || typeof raw.proposalId !== "string") throw new Error("Evidence identity unavailable.");
  const observation = parseSnapshot(raw.observation);
  if (!Array.isArray(observation.reassignmentCandidates)) throw new Error("Candidate evidence unavailable.");
  const seen = new Set<string>();
  for (const c of observation.reassignmentCandidates) {
    if (!record(c)) throw new Error("Candidate evidence invalid.");
    const source = observation.robots.find(r => r.id === c.fromRobotId);
    const identity = JSON.stringify([c.jobId,c.fromRobotId,c.toRobotId]);
    if (!record(c) || ![c.jobId, c.fromRobotId, c.toRobotId].every(v => typeof v === "string" && v.length > 0) ||
        seen.has(identity) || c.fromRobotId === c.toRobotId || !source || source.status !== "moving" || !source.blocked || source.battery < 30 || source.jobId !== c.jobId ||
        ![c.pickupSteps, c.deliverySteps, c.chargingExitSteps, c.requiredBattery].every(v => Number.isSafeInteger(v) && v >= 0) ||
        !observation.jobs.some(j => j.id === c.jobId && j.status === "assigned" && j.assignedRobot === c.fromRobotId) ||
        !observation.activeIncidents.some(i => i.robotId === c.fromRobotId && i.type === "traffic_blocked") ||
        !observation.robots.some(r => r.id === c.toRobotId && r.status === "idle" && r.jobId === "" && r.battery >= Math.max(30, c.requiredBattery))) throw new Error("Candidate evidence invalid.");
    seen.add(identity);
  }
  return { generation, scenarioId: raw.scenarioId, runtimeId: raw.runtimeId, proposalId: raw.proposalId, observation };
}
const telemetry = (r: Robot): string => `${r.id}: ${r.status}, battery ${r.battery}%, position (${r.x}, ${r.y}), job ${r.jobId || "none"}, waiting ${r.waitTicks} ticks, blocked ${r.blocked ? "yes" : "no"}.`;

function matchesProposal(s: FleetSnapshot, p: Proposal): boolean {
  if (p.action === "reassign_unstarted_job") return s.reassignmentCandidates?.some(c => c.jobId === p.jobId && c.fromRobotId === p.fromRobotId && c.toRobotId === p.toRobotId) === true;
  const r = s.robots.find(r => r.id === p.robotId);
  const station = s.stations.find(station => station.id === p.stationId);
  return !!r && !!station && r.x === station.x && r.y === station.y && r.status === "idle" && !r.blocked && !r.jobId && r.battery >= 30 && r.battery < 100 &&
    !station.occupiedBy && (!station.reservedBy || station.reservedBy === r.id) && !s.activeIncidents.some(i => i.robotId === r.id) &&
    !s.robots.some(other => other.id !== r.id && other.x === station.x && other.y === station.y);
}

function ActionEvidence({ observation: s, proposal: p }: { observation: FleetSnapshot; proposal: Proposal }): JSX.Element {
  if (p.action === "reassign_unstarted_job") {
    const c = s.reassignmentCandidates?.find(c => c.jobId === p.jobId && c.fromRobotId === p.fromRobotId && c.toRobotId === p.toRobotId);
    const job = s.jobs.find(j => j.id === p.jobId);
    if (!c || !job) return <p className="decision-error">Proposal targets do not match the original Go evidence. Eligibility is unverified.</p>;
    return <><p><strong>Proposed intervention:</strong> ReassignUnstartedJob · {p.jobId} · {p.fromRobotId} → {p.toRobotId}.</p>
      <p>Job {job.id}: {job.status}, owner {job.assignedRobot}; pickup ({job.fromX}, {job.fromY}), delivery ({job.toX}, {job.toY}).</p>
      {s.robots.filter(r => r.id === p.fromRobotId || r.id === p.toRobotId).map(r => <p key={r.id}>{telemetry(r)}</p>)}
      <p>Go admitted {c.toRobotId} at this observation. Go candidate battery requirement: {c.requiredBattery}%; the command also requires at least 30% for both robots. Candidate admission is snapshot-scoped.</p>
      <p><strong>Why this action:</strong> Transferring an uncollected job may enable progress. The obstruction remains; delivery is not guaranteed.</p></>;
  }
  const r = s.robots.find(r => r.id === p.robotId);
  const station = s.stations.find(station => station.id === p.stationId);
  if (!r || !station || r.x !== station.x || r.y !== station.y) return <p className="decision-error">Charging evidence unavailable for these targets.</p>;
  return <><p><strong>Proposed intervention:</strong> SetCharging · {p.robotId} at {p.stationId}.</p><p>{telemetry(r)}</p>
    <p>{station.id}: position ({station.x}, {station.y}), occupied by {station.occupiedBy || "none"}, reserved by {station.reservedBy || "none"}.</p>
    <p><strong>Why this action:</strong> Charging in place is supported at an eligible free station. It cannot navigate to a station or repair an offline robot. Battery increases require later simulation ticks.</p></>;
}

export function Evidence({ context: c, proposal }: { context: EvidenceContext; proposal?: Proposal }): JSX.Element {
  const e = c.evidence;
  const verified = c.available && c.current && !c.current.poisoned && e && !e.observation.poisoned &&
    e.generation === c.generation && e.scenarioId === c.scenarioId && c.current.tick >= e.observation.tick &&
    (proposal ? e.proposalId === proposal.id && proposal.currentRuntime && matchesProposal(e.observation,proposal) : e.proposalId === "");
  if (!verified || !e || !c.current) return <p className="inbox-message decision-error" role="status">Evidence unavailable or stale. Current reasoning cannot be verified; prior observations do not authorize this run.</p>;
  const s = e.observation;
  const noAction = !proposal;
  return <>{noAction && <p className="inbox-message">{s.activeIncidents.length ? `Operator attention: no eligible recovery proposal at startup (Go tick ${s.tick}). Eligibility may change later.` : "No recovery recommendation needed at startup. Observe normal simulator activity."}</p>}<details className="reasoning-evidence"><summary>Evidence &amp; reasoning</summary>
    <p className="evidence-label">Deterministic presentation · No live LLM</p>
    <p>Original Go observation · tick {s.tick}. Current Go telemetry · tick {c.current.tick}.</p>
    {s.tick !== c.current.tick && <p>Original evidence is historical within this run; it is not current authorization. No new analysis is performed as time advances.</p>}
    <dl className="reasoning-stages">
      <div><dt>Go observation / classification</dt><dd>{s.activeIncidents.length ? s.activeIncidents.map(i => <p key={`${i.robotId}:${i.type}`}>{i.robotId}: {i.type} ({i.severity}) at tick {s.tick}. {telemetry(i.telemetry)}</p>) : "No Go-classified incidents at the original observation."}</dd></div>
      <div><dt>Recommendation source</dt><dd>{noAction ? "No adapter or model invoked for this preset. No supported proposal was submitted at startup." : "Deterministic local Mastra adapter; no paid model inference. The explanation shown here is host-authored from Go evidence."}</dd></div>
      <div><dt>Host grounding / policy</dt><dd>{noAction ? "No model output to validate." : "The local adapter passed the strict reasoning schema, exact telemetry/reference checks, bounded statement policy and fresh-snapshot check before submission."}</dd></div>
    </dl>
    {proposal ? <ActionEvidence observation={s} proposal={proposal} /> : <p>{s.activeIncidents.length
      ? `Operator attention required. Go admitted ${s.reassignmentCandidates?.length ?? 0} transfer candidates at tick ${s.tick}. No eligible recovery proposal is available at startup. Eligibility may change as simulation advances; this preset does not resubmit automatically.`
      : `No recovery recommendation is necessary at startup. Current activity: ${c.current.robots.filter(r => r.status === "moving").length} moving robots; ${c.current.jobs.filter(j => j.status !== "done").length} open jobs at tick ${c.current.tick}. Normal movement is simulator activity.`}</p>}
    {noAction && s.activeIncidents.length > 0 && s.robots.filter(r => r.status === "idle" && !r.jobId).map(r => <p key={r.id}>{telemetry(r)}{r.battery < 30 && " Below the supported command's 30% battery minimum."}</p>)}
    <dl className="reasoning-stages">
      <div><dt>Proposal / Go validation</dt><dd>{proposal ? `Stored proposal ${proposal.id}: ${proposal.state}; execution ${proposal.execution}. Submission passed Go admission. Authorization, expiry, pending state, runtime and safety must still be revalidated at approval and execution.` : "No proposal; no approval requested."}</dd></div>
      <div><dt>Human decision</dt><dd>{proposal?.state === "approved" ? "Approval is recorded in the stored proposal." : proposal?.state === "rejected" ? "Rejection is recorded; it does not execute a command." : proposal ? "Independent operator approval is required. A recommendation is not an approval." : "Operator attention is not an automatically executed repair."}</dd></div>
      <div><dt>Recorded Go execution</dt><dd>{proposal ? proposal.execution === "succeeded" ? "Stored Go execution succeeded. Later simulator effects are separate." : proposal.execution === "none" ? "No execution recorded." : "Execution failed or remains uncertain. Reconcile the receipt and stored status." : "No recommendation command was executed by this preset."}</dd></div>
      <div><dt>Observed simulator outcome</dt><dd>{proposal?.action === "reassign_unstarted_job" ? `Job ${proposal.jobId}: ${c.current.jobs.find(j => j.id === proposal.jobId)?.status ?? "unavailable"} at tick ${c.current.tick}.` : proposal?.action === "set_charging" ? `${c.current.robots.filter(r => r.id === proposal.robotId).map(telemetry).join(" ")} Observed at tick ${c.current.tick}.` : `Current Go tick ${c.current.tick}; autonomous activity does not imply an AI intervention.`} No causal benefit or future delivery is inferred.</dd></div>
    </dl>
    <details><summary>Snapshot identity</summary><p>Scenario {e.scenarioId}</p><p>Generation {e.generation}</p><p>Runtime {e.runtimeId}</p><p>Incident references use snapshot scope, original tick, robot ID and incident type; they are not stable across runs.</p></details>
  </details></>;
}
