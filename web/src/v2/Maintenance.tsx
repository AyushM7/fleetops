import { useId, type ReactNode } from "react";
import { Icon } from "./WarehouseIcon";
import type { MaintenanceAttempt, Robot, Snapshot } from "./telemetry";
import { breakdownLabels, cellLabel, maintenanceForRobot, maintenanceJobResult, maintenanceOutcomeLabels,
  maintenancePhaseLabel, maintenanceReasonLabels, type Selection } from "./warehousePresentation";

type Inspect = (selection: Selection, opener: HTMLButtonElement) => void;

function Reference({ kind, id, inspect }: { kind: "robot" | "job"; id: string; inspect?: Inspect }) {
  return inspect ? <button type="button" className="v2-maintenance-reference"
    aria-label={"Inspect " + kind + " " + id} onClick={event => inspect({ kind, id }, event.currentTarget)}>
    {id}<Icon name="arrow-right" size={14} />
  </button> : <span>{id}</span>;
}

function Detail({ label, children }: { label: string; children: ReactNode }) {
  return <div className="v2-detail"><dt>{label}</dt><dd>{children}</dd></div>;
}

export function RepairProgress({ attempt }: { attempt: MaintenanceAttempt }) {
  return <span className="v2-repair-progress">
    <progress aria-label={"Stationary repair work for " + attempt.robotId}
      max={attempt.requiredRepairTicks} value={attempt.repairTicks} />
    <span>{attempt.repairTicks} / {attempt.requiredRepairTicks} repair ticks</span>
  </span>;
}

export function MaintenanceAttemptDetails({ snapshot, attempt, inspect }: {
  snapshot: Snapshot; attempt: MaintenanceAttempt; inspect?: Inspect;
}) {
  const job = attempt.jobId && snapshot.jobs.find(item => item.id === attempt.jobId);
  const result = maintenanceJobResult(snapshot, attempt);
  return <dl className="v2-maintenance-details">
    <Detail label="Attempt">{attempt.id}</Detail>
    <Detail label="Target">
      <Reference id={attempt.robotId} kind="robot" inspect={inspect} /> <span>· {attempt.faultId} · {breakdownLabels[attempt.fault]}</span>
    </Detail>
    <Detail label="Dispatch">{attempt.aiDirected ? "AI-directed" : "Deterministic Go"}</Detail>
    <Detail label="Service cell">{cellLabel(attempt.serviceCell)}</Detail>
    <Detail label="Started">Tick {attempt.startedTick}</Detail>
    <Detail label="Status">{attempt.outcome ? maintenanceOutcomeLabels[attempt.outcome] : maintenancePhaseLabel(snapshot)}</Detail>
    <Detail label="Repair work"><RepairProgress attempt={attempt} /></Detail>
    {attempt.finishedTick !== undefined && <Detail label="Finished">Tick {attempt.finishedTick}</Detail>}
    {attempt.reason && <Detail label="Reason">{maintenanceReasonLabels[attempt.reason]} <code>{attempt.reason}</code></Detail>}
    {attempt.jobId && <Detail label="Interrupted job">
      <Reference id={attempt.jobId} kind="job" inspect={job ? inspect : undefined} />{result && <span> · {result}</span>}
    </Detail>}
  </dl>;
}

export function WarehouseMaintenance({ snapshot, inspect }: { snapshot: Snapshot; inspect: Inspect }) {
  const title = useId();
  const work = snapshot.maintenance;
  const active = work?.active;
  const latest = work?.history?.at(-1);
  const breakdowns = snapshot.robots.flatMap(robot => robot.activeFault ? [{ robot, fault: robot.activeFault }] : []);
  return <section className="v2-panel v2-maintenance-summary" aria-labelledby={title}>
    <header>
      <h2 id={title}><Icon name="diamond" size={16} />Maintenance</h2>
      <span className="v2-status-chip" data-tone={active ? "moving" : undefined}>M1 · {maintenancePhaseLabel(snapshot)}</span>
    </header>
    {active ? <div className="v2-maintenance-summary-line">
      <span><strong>{active.id}</strong> · <Reference kind="robot" id={active.robotId} inspect={inspect} /> · {active.faultId}</span>
      <span>{cellLabel(active.serviceCell)} service site</span>
      <RepairProgress attempt={active} />
    </div> : latest?.outcome ? <div className="v2-maintenance-summary-line">
      <span><strong>{maintenanceOutcomeLabels[latest.outcome]}</strong> · <Reference kind="robot" id={latest.robotId} inspect={inspect} /> · {latest.faultId}</span>
      {latest.finishedTick !== undefined && <span>Tick {latest.finishedTick}</span>}
      {latest.reason && <span>{maintenanceReasonLabels[latest.reason]}</span>}
    </div> : <p className="v2-secondary">No active repair attempt.</p>}
    {breakdowns.length > 0 && <ul className="v2-maintenance-incidents" aria-label="Active breakdowns">
      {breakdowns.map(({ robot, fault }) => {
        const { breakdown, reason } = maintenanceForRobot(snapshot, robot);
        return <li key={robot.id}>
          <Reference kind="robot" id={robot.id} inspect={inspect} /> · {breakdown && breakdown.id + " · "}
          {breakdownLabels[fault]}{reason && " · " + maintenanceReasonLabels[reason]}
        </li>;
      })}
    </ul>}
  </section>;
}

export function RobotMaintenanceDetails({ snapshot, robot, inspect }: { snapshot: Snapshot; robot: Robot; inspect: Inspect }) {
  const { breakdown, attempt, history, recent, reason } = maintenanceForRobot(snapshot, robot);
  if (robot.role !== "maintenance" && !robot.activeFault && !recent) return null;
  return <section className="v2-inspector-group" aria-label="Maintenance details">
    <h3>{robot.role === "maintenance" ? "Maintenance work" : "Maintenance & recovery"}</h3>
    {robot.activeFault && <dl className="v2-maintenance-details">
      <Detail label="Active breakdown">{breakdownLabels[robot.activeFault]}{breakdown && " · " + breakdown.id}</Detail>
      {reason && <Detail label="Blocker">{maintenanceReasonLabels[reason]} <code>{reason}</code></Detail>}
    </dl>}
    {attempt ? <MaintenanceAttemptDetails snapshot={snapshot} attempt={attempt} inspect={inspect} />
      : robot.role === "maintenance" && <p className="v2-secondary">{maintenancePhaseLabel(snapshot)} · No active repair attempt.</p>}
    {robot.role === "maintenance" ? history.length > 0 && <ul className="v2-maintenance-history" aria-label="Recent maintenance outcomes">
      {history.slice(-3).reverse().map(item => <li key={item.id}>
        <strong>{item.id}</strong> · <Reference kind="robot" id={item.robotId} inspect={inspect} /> · {item.faultId}
        <span>{item.outcome && maintenanceOutcomeLabels[item.outcome]} · Tick {item.finishedTick}</span>
        {item.reason && <span>{maintenanceReasonLabels[item.reason]}</span>}
      </li>)}
    </ul> : recent && !attempt && <MaintenanceAttemptDetails snapshot={snapshot} attempt={recent} inspect={inspect} />}
  </section>;
}
