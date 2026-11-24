import { useCallback, useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import type { WarehouseFeed } from "./session";
import { operationsModeLabel, type MaintenanceAttempt, type OperationsActivity, type Robot, type Snapshot } from "./telemetry";
import { Icon } from "./WarehouseIcon";
import { useInspectorDialog, useInspectorFocus } from "./WarehouseInspector";
import { MaintenanceAttemptDetails } from "./Maintenance";
import { RecoveryApprovals } from "./RecoveryApprovals";
import { acceptedMaintenanceAttempt, breakdownLabels, findEntity, isPriorityOverdue, jobOutcomeLabels,
  maintenanceForRobot, maintenanceOutcomeLabels, maintenancePhaseLabel, maintenanceReasonLabels, type Selection } from "./warehousePresentation";

type Tone = "moving" | "charging" | "waiting" | "fault" | "assisted";
type Entry = {
  key: string; tick: number; title: string; state: string; tone?: Tone; references: Selection[];
} & (
  | { source: "decision"; activity: OperationsActivity }
  | { source: "delivery"; job: Snapshot["jobs"][number] }
  | { source: "fault"; fault: Snapshot["faults"][number] }
  | { source: "breakdown"; robot: Robot }
  | { source: "maintenance"; attempt: MaintenanceAttempt }
);

const lifecycle: Record<string, { label: string; tone?: Tone }> = {
  observed: { label: "Checking assistance", tone: "moving" },
  proposed: { label: "Proposed to Go", tone: "waiting" },
  applied: { label: "Applied by Go", tone: "charging" },
  abstained: { label: "Abstained" },
  cancelled: { label: "Cancelled" },
  observation_unavailable: { label: "Observation unavailable", tone: "fault" },
  model_busy: { label: "Model busy", tone: "waiting" },
  budget_denied: { label: "Budget denied", tone: "waiting" },
  ledger_unavailable: { label: "Ledger unavailable", tone: "fault" },
  model_unavailable: { label: "Model unavailable", tone: "fault" },
  usage_unconfirmed: { label: "Usage unconfirmed", tone: "waiting" },
  invalid_output: { label: "Invalid decision output", tone: "fault" },
  audit_unavailable: { label: "Activity audit unavailable", tone: "fault" },
  go_rejected: { label: "Rejected by Go", tone: "fault" },
  execution_failed: { label: "Go execution failed", tone: "fault" },
  execution_unconfirmed: { label: "Go execution unconfirmed", tone: "waiting" },
};

const faultLabels: Record<string, string> = {
  low_battery: "Low battery recorded",
  robot_unresponsive: "Unresponsive robot recorded",
  aisle_conflict: "Aisle conflict recorded",
};

const abstentionLabels: Record<string, string> = {
  no_safe_option: "No safe option",
  prefer_existing_dispatch: "Preferred existing dispatch",
  insufficient_headroom: "Insufficient headroom",
};

const timeFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "medium" });
const usd = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", maximumFractionDigits: 6 });

function activitySource(activity: OperationsActivity): { label: string; tone?: Tone } {
  if (activity.modelStatus === "decided" || activity.decision) return { label: "Model decision", tone: "assisted" };
  if (activity.usage) return { label: "Model response", tone: "assisted" };
  if (activity.requestId || activity.model) return { label: "Model response unconfirmed" };
  return { label: "Go observation" };
}

function decisionEntry(activity: OperationsActivity): Entry {
  const decision = activity.decision;
  const references: Selection[] = [];
  if (decision?.kind === "assign") {
    for (const { jobId, robotId } of decision.assignments) {
      references.push({ kind: "job", id: jobId }, { kind: "robot", id: robotId });
    }
  }
  if (decision?.kind === "reassign") {
    const { JobID, FromRobotID, ToRobotID } = decision.reassignment;
    references.push({ kind: "job", id: JobID }, { kind: "robot", id: FromRobotID }, { kind: "robot", id: ToRobotID });
  }
  if (decision?.kind === "maintenance") references.push({ kind: "robot", id: decision.maintenance.robotId });
  const status = Object.hasOwn(lifecycle, activity.status) ? lifecycle[activity.status] : { label: "Unrecognized activity status" };
  const state = activity.status === "applied" && !(activity.go?.execution === "succeeded" && activity.go.auditCommitted)
    ? lifecycle.execution_unconfirmed : status;
  const title = decision?.kind === "assign" ? "Job assignment"
    : decision?.kind === "reassign" ? "Job reassignment"
    : decision?.kind === "maintenance" ? "Maintenance dispatch"
    : decision?.kind === "abstain" ? "No action recommended" : "Dispatch review";
  return { source: "decision", key: activity.observationId, tick: activity.tick, activity, title, state: state.label, tone: state.tone, references };
}

function warehouseEntries(snapshot: Snapshot): Entry[] {
  const entries: Entry[] = snapshot.faults.map(fault => ({
    source: "fault", key: "fault:" + fault.id, tick: fault.tick, fault,
    title: Object.hasOwn(faultLabels, fault.type) ? faultLabels[fault.type] : Object.hasOwn(breakdownLabels, fault.type)
      ? breakdownLabels[fault.type as keyof typeof breakdownLabels] + " recorded" : "Warehouse fault recorded", state: "Recorded",
    tone: fault.type === "robot_unresponsive" ? "fault" : "waiting",
    references: fault.robots.map(id => ({ kind: "robot", id })),
  }));
  for (const job of snapshot.jobs) {
    if (job.status !== "done" || job.completedTick === undefined) continue;
    entries.push({
      source: "delivery", key: "delivery:" + job.id, tick: job.completedTick, job, title: "Job delivered",
      state: job.outcome ? jobOutcomeLabels[job.outcome] : "Completed", tone: job.outcome === "LATE" ? "waiting" : "charging",
      references: [{ kind: "job", id: job.id }, ...(job.assignedRobot ? [{ kind: "robot" as const, id: job.assignedRobot }] : [])],
    });
  }
  return entries.sort((a, b) => b.tick - a.tick).slice(0, 12);
}

function maintenanceEntries(snapshot: Snapshot): Entry[] {
  const work = snapshot.maintenance;
  const entries: Entry[] = [];
  for (const robot of snapshot.robots) {
    if (!robot.activeFault) continue;
    const { breakdown, attempt, reason } = maintenanceForRobot(snapshot, robot);
    if (attempt && robot.role !== "maintenance") continue;
    entries.push({
      source: "breakdown", robot, key: "breakdown:" + (breakdown?.id ?? robot.id),
      title: breakdownLabels[robot.activeFault] + (breakdown ? " · " + breakdown.id : ""),
      tick: breakdown?.tick ?? snapshot.tick, state: reason ? maintenanceReasonLabels[reason] : "Active breakdown",
      tone: "fault", references: [{ kind: "robot", id: robot.id }],
    });
  }
  const history = work?.history?.slice(-8).reverse() ?? [];
  for (const attempt of work?.active ? [work.active, ...history] : history) {
    entries.push({
      source: "maintenance", attempt, key: "maintenance:" + attempt.id, title: attempt.id + " · " + attempt.faultId,
      tick: attempt.finishedTick ?? attempt.startedTick,
      state: attempt.outcome ? maintenanceOutcomeLabels[attempt.outcome] : maintenancePhaseLabel(snapshot),
      tone: attempt.outcome === "completed" ? "charging" : attempt.outcome ? "fault" : "moving",
      references: [{ kind: "robot", id: attempt.robotId }, ...(attempt.jobId ? [{ kind: "job" as const, id: attempt.jobId }] : [])],
    });
  }
  return entries;
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return <div className="v2-detail"><dt>{label}</dt><dd>{children}</dd></div>;
}

function Time({ value }: { value: string }) {
  return <time dateTime={value}>{timeFormat.format(new Date(value))}</time>;
}

function StateChip({ entry }: { entry: Entry }) {
  return <span className="v2-status-chip" data-tone={entry.tone}>{entry.state}</span>;
}

function EntityReference({ selection, snapshot, inspect }: {
  selection: Selection; snapshot: Snapshot; inspect: (selection: Selection) => void;
}) {
  if (!findEntity(snapshot, selection)) return <span className="v2-ops-missing">{selection.id} · Outside current snapshot</span>;
  const kind = selection.kind === "job" ? "job" : "robot";
  return <button type="button" className="v2-ops-reference" aria-label={"Inspect " + kind + " " + selection.id}
    onClick={() => inspect(selection)}>{selection.id}<Icon name="arrow-right" size={15} /></button>;
}

function Recommendation({ activity, snapshot, inspect }: {
  activity: OperationsActivity; snapshot: Snapshot; inspect: (selection: Selection) => void;
}) {
  const decision = activity.decision;
  const ref = (kind: "job" | "robot", id: string) => <EntityReference selection={{ kind, id }} snapshot={snapshot} inspect={inspect} />;
  if (decision?.kind === "assign") return <ul className="v2-ops-recommendations">
    {decision.assignments.map(({ jobId, robotId }) => <li key={jobId}>{ref("job", jobId)}<span>Assign to</span>{ref("robot", robotId)}</li>)}
  </ul>;
  if (decision?.kind === "reassign") {
    const { JobID, FromRobotID, ToRobotID } = decision.reassignment;
    return <dl>
      <Field label="Job">{ref("job", JobID)}</Field>
      <Field label="From">{ref("robot", FromRobotID)}</Field>
      <Field label="To">{ref("robot", ToRobotID)}</Field>
    </dl>;
  }
  if (decision?.kind === "maintenance") {
    return <dl>
      <Field label="Target">{ref("robot", decision.maintenance.robotId)}</Field>
      <Field label="Fault occurrence"><code>{decision.maintenance.faultId}</code></Field>
    </dl>;
  }
  if (decision?.kind === "abstain") return <p>{Object.hasOwn(abstentionLabels, decision.reason) ? abstentionLabels[decision.reason] : "Reason unavailable"}.</p>;
  return <p>{activity.stage === "observed" ? "Waiting for the review to finish." : "No recommendation was recorded."}</p>;
}

function CommandDetails({ activity }: { activity: OperationsActivity }) {
  const receipt = activity.go;
  const execution = receipt?.auditCommitted
    ? { succeeded: "Succeeded", failed: "Failed", none: "No execution", unknown: "Unconfirmed" }[receipt.execution]
    : "Unconfirmed";
  return <section className="v2-ops-detail-section">
    <h3>Go validation &amp; automatic execution</h3>
    <dl>
      {activity.proposedAt && <Field label="Proposal prepared"><Time value={activity.proposedAt} /></Field>}
      {receipt ? <>
        <Field label="Execution">{execution}</Field>
        <Field label="Command audit">{receipt.auditCommitted ? "Committed" : "Unconfirmed"}</Field>
        {!!receipt.auditId && <Field label="Audit receipt">{receipt.auditId}</Field>}
        {!!receipt.priorAuditId && <Field label="Prior audit receipt">{receipt.priorAuditId}</Field>}
      </> : <Field label="Execution">{activity.stage === "finished" ? "No command receipt" : "Awaiting result"}</Field>}
      {activity.goReason && <Field label="Go reason">{activity.goReason === "stale_observation" ? "Observation became stale"
        : activity.goReason === "admission_rejected" ? "Admission rejected" : activity.goReason}</Field>}
      {activity.finishedAt && <Field label="Review finished"><Time value={activity.finishedAt} /></Field>}
    </dl>
    {receipt?.auditCommitted && receipt.execution === "succeeded" && <p>{activity.decision?.kind === "maintenance"
      ? "Go admitted the maintenance dispatch. The simulator records the repair outcome separately."
      : "The receipt confirms the dispatch change. Check Jobs for later delivery outcomes."}</p>}
  </section>;
}

function ModelDetails({ activity }: { activity: OperationsActivity }) {
  const { model, modelStatus, usage, requestId, reservedMicroUsd, accountedMicroUsd } = activity;
  return <section className="v2-ops-detail-section">
    <h3>Model &amp; accounting</h3>
    <dl>
      <Field label={usage ? "Reported model" : "Model identity"}>{usage?.model ?? "Unavailable"}</Field>
      {!usage && model && <Field label="Configured model">{model}</Field>}
      {modelStatus && <Field label="Model status">{modelStatus === "decided" ? "Decision returned"
        : Object.hasOwn(lifecycle, modelStatus) ? lifecycle[modelStatus].label : "Unrecognized model status"}</Field>}
      {requestId !== undefined && <Field label="Ledger request">{requestId}</Field>}
      <Field label="Token usage">{usage ? usage.inputTokens.toLocaleString() + " in / " + usage.outputTokens.toLocaleString() + " out" : "Unavailable"}</Field>
      {reservedMicroUsd !== undefined && <Field label="Reserved (USD)">{usd.format(reservedMicroUsd / 1_000_000)}</Field>}
      <Field label="Accounted (USD)">{accountedMicroUsd === undefined ? "Unavailable" : usd.format(accountedMicroUsd / 1_000_000)}</Field>
    </dl>
    {(reservedMicroUsd !== undefined || accountedMicroUsd !== undefined) && <p>Amounts are Go’s spending ledger figures.</p>}
  </section>;
}

export function DecisionDetails({ activity, snapshot, inspect }: {
  activity: OperationsActivity; snapshot: Snapshot; inspect: (selection: Selection) => void;
}) {
  const attempt = acceptedMaintenanceAttempt(snapshot, activity);
  return <>
    <section className="v2-ops-detail-section">
      <h3>Go observation</h3>
      <dl>
        <Field label="Observed"><Time value={activity.observedAt} /></Field>
        <Field label="Observation ID"><code>{activity.observationId}</code></Field>
        <Field label="Activity journal">{activity.recorded ? "Confirmed" : "Unconfirmed"}</Field>
      </dl>
      <p>The browser receives the observation ID and tick. Candidate previews, historical warehouse conditions, and model rationale are unavailable here.</p>
    </section>
    <section className="v2-ops-detail-section">
      <h3>Recommendation</h3>
      <Recommendation activity={activity} snapshot={snapshot} inspect={inspect} />
      {activity.proposedAt && <p>Go matched the proposal to its offered options before checking live admission.</p>}
    </section>
    <CommandDetails activity={activity} />
    {attempt && <section className="v2-ops-detail-section">
      <h3>Simulator maintenance attempt</h3>
      <MaintenanceAttemptDetails snapshot={snapshot} attempt={attempt} inspect={inspect} />
    </section>}
    <ModelDetails activity={activity} />
  </>;
}

function MaintenanceEventDetails({ entry, snapshot, inspect }: {
  entry: Extract<Entry, { source: "maintenance" | "breakdown" }>; snapshot: Snapshot; inspect: (selection: Selection) => void;
}) {
  if (entry.source === "maintenance") return <section className="v2-ops-detail-section">
    <h3>Simulator maintenance attempt</h3>
    <MaintenanceAttemptDetails snapshot={snapshot} attempt={entry.attempt} inspect={inspect} />
  </section>;
  const { breakdown, reason } = maintenanceForRobot(snapshot, entry.robot);
  return <section className="v2-ops-detail-section">
    <h3>Active breakdown</h3>
    <dl>
      {breakdown && <Field label="Fault occurrence"><code>{breakdown.id}</code></Field>}
      <Field label="Robot"><EntityReference selection={{ kind: "robot", id: entry.robot.id }} snapshot={snapshot} inspect={inspect} /></Field>
      {reason && <Field label="Blocker">{maintenanceReasonLabels[reason]} <code>{reason}</code></Field>}
    </dl>
  </section>;
}

function WarehouseEventDetails({ entry, snapshot, inspect }: {
  entry: Extract<Entry, { source: "delivery" | "fault" }>; snapshot: Snapshot; inspect: (selection: Selection) => void;
}) {
  return <section className="v2-ops-detail-section">
    <h3>{entry.source === "delivery" ? "Delivery outcome" : "Fault record"}</h3>
    <dl>
      {entry.source === "delivery" ? <Field label="Job type">{entry.job.type === "PRIORITY" ? "Priority" : "Normal"}</Field> : <>
        <Field label="Fault ID">{entry.fault.id}</Field>
        {entry.fault.aisle !== undefined && <Field label="Aisle">{entry.fault.aisle}</Field>}
      </>}
      {entry.references.map(selection => <Field key={selection.kind + ":" + selection.id} label={selection.kind === "job" ? "Job" : "Robot"}>
        <EntityReference selection={selection} snapshot={snapshot} inspect={inspect} />
      </Field>)}
    </dl>
    <p>{entry.source === "delivery" ? "Go reported this completion in the warehouse snapshot. The snapshot supplies no decision or command link."
      : "This records the fault at the event tick. Check Fleet for the robot’s current state."}</p>
  </section>;
}

type ActivitySelection = { key: string; pointer: boolean };

function ActivityGroup({ title, entries, selected, pick, empty, description }: {
  title: string; entries: Entry[]; selected?: string; pick: (selection: ActivitySelection, opener: HTMLElement) => void;
  empty: string; description: string;
}) {
  const id = useId();
  return <section className="v2-panel v2-ops-group" aria-labelledby={id}>
    <header><h2 id={id}>{title}</h2><p>{description}</p></header>
    {entries.length ? <ol className="v2-ops-list">
      {entries.map(entry => {
        const source = entry.source === "decision" ? activitySource(entry.activity)
          : entry.source === "maintenance" ? { label: entry.attempt.aiDirected ? "AI-directed" : "Deterministic Go",
            tone: entry.attempt.aiDirected ? "assisted" : undefined } : { label: "Go telemetry" };
        const references = entry.references.slice(0, 4).map(ref => ref.id).join(" · ");
        return <li key={entry.key}>
          <button type="button" className="v2-ops-row" aria-pressed={entry.key === selected}
            onClick={event => pick({ key: entry.key, pointer: event.detail > 0 }, event.currentTarget)}>
            <Icon name={entry.source === "delivery" ? "package" : "activity"} size={18} />
            <span className="v2-ops-row-title"><strong>{entry.title}</strong>
              <span>{references}{entry.references.length > 4 ? " +" + (entry.references.length - 4) : ""}{references && " · "}{entry.source === "decision" ? "Observed at tick " : "Tick "}{entry.tick}</span>
            </span>
            <span className="v2-ops-row-status"><StateChip entry={entry} /><span data-tone={source.tone}>{source.label}</span></span>
            <Icon name="arrow-right" size={15} />
          </button>
        </li>;
      })}
    </ol> : <p className="v2-ops-empty">{empty}</p>}
  </section>;
}

const waitLabel = {
  connecting: "Waiting for confirmed operations telemetry.",
  reconnecting: "Reconnecting to the operations feed.",
  live: "Waiting for the first warehouse snapshot.",
  unavailable: "The warehouse run is unavailable. Waiting for Go to report a ready run.",
  expired: "This visitor session has ended.",
};

export function OperationsAI({ feed, inspect }: { feed: WarehouseFeed; inspect: (selection: Selection) => void }) {
  const frame = feed.frame;
  const scope = frame ? frame.generation + ":" + frame.runtimeId : null;
  const [selection, setSelection] = useState<(ActivitySelection & { scope: string }) | null>(null);
  const decisions = frame ? frame.operations.map(decisionEntry).reverse() : [];
  const events = frame ? warehouseEntries(frame.snapshot) : [];
  const repairs = frame ? maintenanceEntries(frame.snapshot) : [];
  const selected = selection && selection.scope === scope
    ? [...decisions, ...repairs, ...events].find(entry => entry.key === selection.key) : undefined;
  if (selection && !selected) setSelection(null);
  const select = useCallback((entry: ActivitySelection | null) => setSelection(entry && scope ? { ...entry, scope } : null), [scope]);
  const { surface, pick, close, restoreFocus } = useInspectorFocus(select, "[data-ops-heading]", true);
  const { dialog, heading, content } = useInspectorDialog(selected?.key, scope ?? undefined, close, restoreFocus);
  const titleId = useId();
  const snapshot = frame?.snapshot;
  const trafficWaits = snapshot?.robots.filter(robot => robot.blocked || robot.activity === "WAITING_FOR_TRAFFIC").length;
  const overdue = snapshot?.jobs.filter(isPriorityOverdue).length;
  const last = useRef<{ scope: string | null; entry: Entry } | null>(null);
  useLayoutEffect(() => {
    if (selected) last.current = { scope, entry: selected };
  }, [selected, scope]);
  const detail = selected ?? (last.current?.scope === scope ? last.current.entry : undefined);

  return <div className="v2-operations" ref={surface}>
    <section className="v2-panel v2-ops-overview" aria-label="Operations overview">
      <header><h2 tabIndex={-1} data-ops-heading>Operations overview</h2>{snapshot && <span>Tick {snapshot.tick}</span>}</header>
      {frame && snapshot ? <dl>
        <div><dt>Dispatch</dt><dd data-tone={frame.mode === "ai_assisted" ? "assisted" : undefined}>{operationsModeLabel[frame.mode]}</dd></div>
        <div><dt>Open jobs</dt><dd>{snapshot.jobs.filter(job => job.status !== "done").length}</dd></div>
        <div><dt>Traffic waits</dt><dd data-tone={trafficWaits ? "waiting" : undefined}>{trafficWaits}</dd></div>
        <div><dt>Overdue priority jobs</dt><dd data-tone={overdue ? "fault" : undefined}>{overdue}</dd></div>
      </dl> : <p className="v2-ops-empty" role="status">{waitLabel[feed.phase]}</p>}
    </section>
    {frame && feed.phase !== "live" && <p className="v2-directory-notice" role="status">
      {feed.phase === "unavailable" ? "Warehouse run unavailable." : "Waiting for a live feed."} Showing last confirmed data at tick {frame.snapshot.tick}.
    </p>}
    <div className={"v2-ops-work" + (selected ? " has-inspector" : "")}>
      {frame && snapshot && <div className="v2-ops-history">
        {scope && <RecoveryApprovals key={scope} generation={frame.generation} snapshot={snapshot} live={feed.phase === "live"} />}
        <ActivityGroup title="Decision activity" description="Operations Manager reviews for this run, newest observation first."
          entries={decisions} selected={selected?.key} pick={pick} empty="No decision activity recorded in this run." />
        <ActivityGroup title="Maintenance & incidents" description="Active breakdowns and physical attempts reported by Go."
          entries={repairs} selected={selected?.key} pick={pick} empty="No breakdowns or maintenance attempts in this run." />
        <ActivityGroup title="Warehouse events" description="Faults and deliveries reported by Go, newest tick first."
          entries={events} selected={selected?.key} pick={pick} empty="No faults or deliveries in the current snapshot." />
        <p className="v2-ops-workflow">Go validates and executes routine dispatch automatically. Proposals require operator approval.</p>
      </div>}
      <dialog ref={dialog} className="v2-panel v2-inspector v2-ops-inspector" inert={!selected} aria-labelledby={titleId}
        onCancel={event => { event.preventDefault(); close(); }}>
        <header className="v2-panel-title">
          <h2 id={titleId} ref={heading} tabIndex={-1} data-pointer-selection={selection?.pointer || undefined}>{detail?.title ?? "Activity details"}</h2>
          <button type="button" className="v2-map-control v2-inspector-close" aria-label="Close inspector" onClick={close}><Icon name="x" size={17} /></button>
        </header>
        <div className="v2-inspector-content" ref={content}>
          {detail && snapshot && <>
            {feed.phase !== "live" && <p className="v2-directory-notice">Last confirmed at tick {snapshot.tick}.</p>}
            <div className="v2-ops-inspector-summary">
              <StateChip entry={detail} /><span>{detail.source === "decision" ? "Observed at tick " : "Tick "}{detail.tick}</span>
              {detail.source === "decision" && <span data-tone={activitySource(detail.activity).tone}>{activitySource(detail.activity).label}</span>}
            </div>
            {detail.source === "decision" ? <DecisionDetails activity={detail.activity} snapshot={snapshot} inspect={inspect} />
              : detail.source === "maintenance" || detail.source === "breakdown"
                ? <MaintenanceEventDetails entry={detail} snapshot={snapshot} inspect={inspect} />
              : <WarehouseEventDetails entry={detail} snapshot={snapshot} inspect={inspect} />}
          </>}
        </div>
      </dialog>
    </div>
  </div>;
}
