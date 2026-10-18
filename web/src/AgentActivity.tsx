import { useEffect, useRef, useState, type JSX } from "react";
import { pollAgentRuns, type AgentFeed, type AgentPolling, type RunSnapshot } from "./agentRuns";
import { keyString, type AgentRecord, type CandidateKey, type Metric, type Objective, type Source } from "./agentRecords";

export const sourceLabels: Record<Source, string> = { deterministic: "Deterministic local adapter", mocked: "Scripted/mock agent investigation",
  recorded: "Recorded AI run", "live-model": "Agent-reported live-model execution" };
const objectives: Record<Objective, string> = { compare_tradeoffs: "Compare trade-offs", minimize_pickup_steps: "Minimize pickup steps", preserve_battery_headroom: "Preserve battery headroom" };
const reasons: Record<string, string> = { runtime_poisoned: "Runtime requires reconciliation", no_candidates: "No admitted recovery candidates", no_supported_incident: "No supported recovery incident", operator_tradeoff: "Trade-off deferred to operator" };
const bases: Record<string, string> = { pickup_cost: "Pickup cost", battery_headroom: "Battery headroom", tradeoff: "Trade-off", admitted_option: "Admitted option; no optimality claim" };
const metricLabels: Record<Metric, string> = { pickupSteps: "Pickup route steps", deliverySteps: "Delivery route steps", chargingExitSteps: "Charging exit steps", totalRouteSteps: "Total route steps", battery: "Battery", requiredBattery: "Required battery", missionHeadroom: "Mission headroom", admissionHeadroom: "Admission headroom" };
const incidentLabels: Record<string, string> = { traffic_blocked: "Traffic blocked", low_battery: "Low battery", robot_unresponsive: "Robot unresponsive" };
const failures: Record<string, string> = { grounding_rejected: "Grounding rejected", timeout: "Investigation deadline exceeded", cancelled: "Investigation cancelled", budget_exhausted: "Investigation budget exhausted", invalid_tool: "Invalid investigation tool call", invalid_usage: "Invalid usage report", investigation_failed: "Investigation failed", snapshot_changed: "Snapshot changed", snapshot_unavailable: "Snapshot unavailable" };
const stamp = (date: string): string => `${new Date(date).toISOString().slice(0, 19).replace("T", " ")} UTC`;
const tuple = (key: CandidateKey): string => `${key.jobId} · ${key.fromRobotId} → ${key.toRobotId}`;
export const selectionKey = (run: AgentRecord): string => `${run.runtimeId}/${run.id}`;
export function runStatus(run: AgentRecord): string {
  const c = run.agentReportedCompletion;
  return !c ? "Incomplete" : c.status === "failed" ? "Failed" : c.status === "rejected" ? "Validation rejected" : c.decision?.kind === "abstain" ? "Abstained" : "Investigation completed";
}
function summary(run: AgentRecord): string {
  const decision = run.agentReportedCompletion?.decision;
  return decision?.selected ? tuple(decision.selected) : decision?.kind === "abstain" ? reasons[decision.reason] : run.agentReportedCompletion ? "No validated recommendation" : "Completion not recorded";
}
function Alternatives({ run }: { run: AgentRecord }): JSX.Element {
  const selected = run.agentReportedCompletion?.decision?.selected;
  return <div className="agent-table-scroll" tabIndex={0} aria-label="Admitted recovery alternatives"><table className="agent-alternatives">
    <caption>Go-admitted alternatives at the original observation</caption><thead><tr><th scope="col">Job / source / recipient</th><th scope="col">Pickup steps</th><th scope="col">Required battery</th><th scope="col">Admission headroom</th></tr></thead>
    <tbody>{run.serverVerifiedObservation.candidates.map(c => <tr key={keyString(c.key)} data-selected={selected && keyString(c.key) === keyString(selected) ? "true" : undefined}>
      <th scope="row">{tuple(c.key)}{selected && keyString(c.key) === keyString(selected) && <span className="agent-selection-label">Selected</span>}</th>
      <td>{c.metrics.pickupSteps ?? "Unavailable"}</td><td>{c.metrics.requiredBattery === undefined ? "Unavailable" : `${c.metrics.requiredBattery}%`}</td><td>{c.metrics.admissionHeadroom === undefined ? "Unavailable" : `${c.metrics.admissionHeadroom} points`}</td></tr>)}</tbody>
  </table>{!run.serverVerifiedObservation.candidates.length && <p>No admitted recovery candidates were recorded.</p>}</div>;
}
function Comparisons({ run }: { run: AgentRecord }): JSX.Element {
  const comparisons = run.agentReportedCompletion?.decision?.comparisons ?? [];
  return <details className="agent-comparisons"><summary>{run.serverVerifiedDecision === true ? "Verified numerical comparisons" : "Reported numerical comparisons · verification unconfirmed"} ({comparisons.length})</summary>
    {comparisons.length ? <ul>{comparisons.map((c, i) => <li key={i}><span>{tuple(c.left)} versus {tuple(c.right)}</span><strong>{metricLabels[c.metric]}: {c.leftValue} {c.relation === "less" ? "<" : c.relation === "greater" ? ">" : "="} {c.rightValue}</strong></li>)}</ul> : <p>No numerical comparisons recorded.</p>}
  </details>;
}
function Investigation({ run }: { run: AgentRecord }): JSX.Element {
  const c = run.agentReportedCompletion;
  return <details className="agent-investigation"><summary>Investigation activity and measurements</summary>
    <p>Sanitized activity summary. Full provider reasoning, prompts and raw tool inputs/outputs are not available.</p>
    {!c ? <p>Completion and investigation measurements have not been recorded.</p> : <>
      <ol className="agent-timeline">{c.steps.map((s, i) => <li key={i}>{s.kind === "request" ? "Model request" : <><code>{s.tool}</code> · Mastra read tool</>}</li>)}</ol>
      {!c.steps.length && <p>No activity steps recorded.</p>}
    </>}
    <dl className="agent-values"><div><dt>Host-counted model requests</dt><dd>{c?.modelRequests ?? "Unavailable"}</dd></div><div><dt>Host-counted tool executions</dt><dd>{c?.toolExecutions ?? "Unavailable"}</dd></div>
      <div><dt>Host-measured investigation latency</dt><dd>{c ? `${c.latencyMs} ms` : "Unavailable"}</dd></div><div><dt>Server-measured duration</dt><dd>{run.serverElapsedMs === null ? "Unavailable" : `${run.serverElapsedMs} ms`}</dd></div>
      <div><dt>Requested model · agent-reported</dt><dd>{run.agentReportedMetadata.requestedModel || "Unavailable"}</dd></div><div><dt>Actual model · provider-reported via agent</dt><dd>{c?.reportedActualModel || "Unavailable"}</dd></div>
      <div><dt>Token usage · provider-reported via agent</dt><dd>{!c || c.reportedUsage.status === "unavailable" ? "Unavailable" : <>{c.reportedUsage.totalTokens} total · {c.reportedUsage.inputTokens} input · {c.reportedUsage.outputTokens} output{c.reportedUsage.status === "partial" && " · Partial reporting only"}</>}</dd></div>
      <div><dt>Estimated cost</dt><dd>Unavailable · no validated pricing estimate supplied</dd></div></dl>
    <p>Go does not independently attest model identity, token reporting or paid inference.</p>
  </details>;
}
function observedOutcome(run: AgentRecord, current: RunSnapshot | null): string {
  if (!run.proposal) return "No linked proposal; an execution outcome cannot be attributed to this run.";
  const selected = run.agentReportedCompletion?.decision?.selected;
  const job = current?.jobs.find(j => j.id === selected?.jobId);
  if (!selected || !current || !job) return "No associated job outcome is available in this sampled snapshot.";
  return `Observed at tick ${current.tick}: job ${job.id} · ${job.status.replaceAll("_", " ")} · owner ${job.assignedRobot || "none"}. ${job.status === "done" ? "The simulator reports this job done; causation by this proposal is not established." : "A successful transfer does not establish delivery completion."}`;
}
function Stages({ run, current }: { run: AgentRecord; current: RunSnapshot | null }): JSX.Element {
  const p = run.proposal;
  const admission = run.serverSubmissionOutcome === "stale_observation" ? "Go denied submission against a stale observation." : run.serverSubmissionOutcome === "proposal_denied" ? "Go denied proposal admission." : !p ? "No associated proposal. This can reflect abstention, disabled submission, a fresh-read failure or incomplete work." : "Go persisted this proposal; creation is not approval.";
  const execution = !p ? "No linked execution receipt." : p.execution === "succeeded" ? "Go recorded a successful reassignment command. This confirms transfer, not pickup or delivery." : p.execution === "unknown" ? "Execution outcome uncertain. Reconcile stored status; do not retry automatically." : p.execution === "failed" ? "Go recorded command failure." : "No command execution recorded.";
  return <ol className="agent-stages" aria-label="Recovery decision stages">
    <li><strong>AI investigation and recommendation</strong><p>{runStatus(run)} · {summary(run)}</p></li>
    <li><strong>Go validation and proposal persistence</strong><p>{admission}</p>{p && <p>Proposal <code>{p.id}</code> · current stored state <b>{p.state}</b>. <a href="#approval-inbox">Open human decision inbox</a></p>}</li>
    <li><strong>Independent human approval or rejection</strong><p>{!p ? "No linked operator decision." : p.state === "pending" ? "Awaiting an independent operator. A recommendation is not approval." : p.state === "approved" ? "Go recorded operator approval." : p.state === "rejected" ? "Go recorded operator rejection." : "Proposal expired; it cannot be newly approved."}</p></li>
    <li><strong>Go execution receipt</strong><p>{execution}</p></li><li><strong>Observed simulator outcome</strong><p>{observedOutcome(run, current)}</p></li>
  </ol>;
}
export function AgentRunDetails({ run, current }: { run: AgentRecord; current: RunSnapshot | null }): JSX.Element {
  const facts = run.serverVerifiedObservation, c = run.agentReportedCompletion;
  return <div className="agent-run-details" aria-label="Selected agent investigation">
    <h3>{summary(run)}</h3><p className="agent-source">{sourceLabels[run.agentReportedMetadata.reportedSource]}</p>
    <p className={run.serverVerifiedDecision === true ? "agent-verified" : "agent-unverified"}>{run.serverVerifiedDecision === true ? "Go verified the persisted decision summary against its original observation." : "Server verification is not confirmed. Treat this as a reported decision, not a verified recommendation."}</p>
    <dl className="agent-values"><div><dt>Objective</dt><dd>{objectives[run.agentReportedMetadata.objective]}</dd></div><div><dt>Justification category</dt><dd>{c?.decision?.kind === "select" ? bases[c.decision.basis] : c?.decision?.kind === "abstain" ? reasons[c.decision.reason] : "Unavailable"}</dd></div>
      <div><dt>Created by Go</dt><dd><time dateTime={run.startedAt}>{stamp(run.startedAt)}</time></dd></div><div><dt>Original observation tick</dt><dd>{facts.tick}</dd></div></dl>
    {c?.status !== "accepted" && c && <p className="agent-unverified">{failures[c.failure]}</p>}
    <p>Go-classified incidents: {facts.incidents.length ? facts.incidents.map(i => `${i.robotId} · ${incidentLabels[i.type]} · ${i.severity}`).join("; ") : "None recorded"}.</p>
    <details><summary>Run, runtime and original observation identities</summary><dl className="agent-values"><div><dt>Run</dt><dd><code>{run.id}</code></dd></div><div><dt>Runtime</dt><dd><code>{run.runtimeId}</code></dd></div><div><dt>Observation</dt><dd><code>{run.observationId}</code></dd></div></dl></details>
    {current?.observationId !== run.observationId && <p className="agent-uncertainty">The original observation is historical. Candidate admission may have changed; Go performs fresh checks before execution.</p>}
    {facts.poisoned && <p className="agent-unverified">The original runtime observation required reconciliation.</p>}
    <Alternatives run={run} /><Comparisons run={run} /><Investigation run={run} /><Stages run={run} current={current} />
    <p className="agent-uncertainty">Snapshot evidence only. Routes are not reserved; the obstruction may remain. Neither delivery nor optimality is established by a recommendation. Independent approval is required.</p>
  </div>;
}
export function AgentActivityView({ feed, selected, select, refresh }: { feed: AgentFeed; selected: string | null; select: (key: string | null) => void; refresh: () => void }): JSX.Element {
  const run = feed.phase === "ready" ? feed.runs.find(r => selectionKey(r) === selected) : undefined;
  const messages: Partial<Record<AgentFeed["phase"], string>> = {
    loading: "Loading persisted agent activity…", paused: "Agent activity is paused until a fresh runtime is available.",
    demo: "No persisted agent-run access in this deterministic local demo. No live LLM is running; historical deterministic evidence remains in the decision context.",
    forbidden: "Agent provenance is restricted. Operators can review current-runtime runs; proposers can view their own. Viewer credentials cannot access provenance.",
    unavailable: "Agent activity is unavailable. Previously displayed records are cleared while the connection is checked.",
    malformed: "Agent records could not be validated. Unsupported, malformed or mismatched provenance is hidden.",
    denied: "This server did not grant provenance access or could not serve it. Its older response does not distinguish restriction from an outage.",
  };
  return <section className="agent-activity" aria-label="Agent activity"><div className="panel-heading"><h2>Agent activity</h2><button className="secondary-button" onClick={refresh} disabled={["demo", "paused"].includes(feed.phase)}>Refresh activity</button></div>
    {feed.phase !== "ready" ? <p role={feed.phase === "malformed" || feed.phase === "unavailable" ? "alert" : "status"}>{messages[feed.phase]}</p> : <>
      {!feed.runs.length ? <p>No persisted agent runs are available for these credentials in the current runtime.</p> : <>
        <p className="agent-history-note">Recent persisted investigations · source and model usage are agent-reported.</p>
        <ul className="agent-history">{feed.runs.map(r => <li key={selectionKey(r)}><button aria-pressed={selectionKey(r) === selected} onClick={() => select(selectionKey(r))}>
          <span className="agent-run-line"><strong>{runStatus(r)}</strong><span>Tick {r.serverVerifiedObservation.tick}</span></span><span>{summary(r)}</span>
          <span className="agent-source">{sourceLabels[r.agentReportedMetadata.reportedSource]}</span><small>{objectives[r.agentReportedMetadata.objective]} · {stamp(r.startedAt)}</small>
          <small>{r.serverVerifiedObservation.incidents.slice(0, 2).map(i => `${i.robotId} · ${incidentLabels[i.type]}`).join("; ") || "No classified incidents"}{r.serverVerifiedObservation.incidents.length > 2 && ` · ${r.serverVerifiedObservation.incidents.length - 2} further incidents`}</small>
          {r.proposalId && <small>Linked proposal · {r.proposal?.state}</small>}</button></li>)}</ul>
        {run ? <><button className="text-button" onClick={() => select(null)}>Close investigation details</button><AgentRunDetails run={run} current={feed.current} /></> : <p>Select a run to inspect its evidence and execution stages.</p>}
      </>}
      {feed.updatedAt && <p className="agent-history-note">Activity last read {feed.updatedAt.toLocaleTimeString()} · current-runtime records from Go.</p>}
    </>}
  </section>;
}
const initial: AgentFeed = { phase: "loading", runs: [], runtimeId: null, current: null, updatedAt: null };
function ConnectedActivity({ token, revision, onUnauthorized }: { token: string; revision: number; onUnauthorized: () => void }): JSX.Element {
  const [feed, setFeed] = useState<AgentFeed>(initial), [selected, select] = useState<string | null>(null);
  const polling = useRef<AgentPolling | null>(null);
  useEffect(() => { const p = pollAgentRuns(token, setFeed, onUnauthorized); polling.current = p; return () => { p.stop(); polling.current = null; }; }, [token, onUnauthorized]);
  useEffect(() => { select(null); }, [feed.runtimeId]);
  useEffect(() => { if (revision) polling.current?.refresh(); }, [revision]);
  return <AgentActivityView feed={feed} selected={selected} select={select} refresh={() => polling.current?.refresh()} />;
}
export default function AgentActivity({ token, available, revision, onUnauthorized }: { token: string; available: boolean; revision: number; onUnauthorized: () => void }): JSX.Element {
  return available ? <ConnectedActivity key={token} token={token} revision={revision} onUnauthorized={onUnauthorized} /> : <AgentActivityView feed={{ ...initial, phase: "paused" }} selected={null} select={() => {}} refresh={() => {}} />;
}
