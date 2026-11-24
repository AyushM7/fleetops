import { useCallback, useEffect, useId, useLayoutEffect, useRef, type ReactNode } from "react";
import { BatteryCharge } from "./BatteryCharge";
import { RobotMaintenanceDetails } from "./Maintenance";
import { Icon } from "./WarehouseIcon";
import {
  cellLabel, chargerStatus, isPriorityOverdue, jobLabels, jobOutcomeLabels, robotActivityLabel, robotRoleLabel, robotTone,
  type Entity, type RobotTone, type Selection,
} from "./warehousePresentation";
import type { Job, Robot, Snapshot, Station } from "./telemetry";

export function useInspectorFocus<T>(selectEntity: (selection: T | null) => void, fallback: string, visible: boolean) {
  const surface = useRef<HTMLDivElement>(null);
  const opener = useRef<HTMLElement | SVGElement | null>(null);
  const close = useCallback(() => selectEntity(null), [selectEntity]);

  useLayoutEffect(() => {
    if (!visible) opener.current = null;
  }, [visible]);

  const pick = (selection: T, source: HTMLElement | SVGElement): void => {
    opener.current = source;
    selectEntity(selection);
  };

  const restoreFocus = useCallback(() => {
    const source = opener.current;
    opener.current = null;
    if (!visible) return;
    const target = source?.isConnected && source.getAttribute("tabindex") !== "-1" && source.getClientRects().length
      ? source : surface.current?.querySelector<HTMLElement>(fallback);
    target?.focus({ preventScroll: true });
    if (target instanceof HTMLElement) target.scrollIntoView({ block: "nearest", inline: "nearest", behavior: "instant" });
  }, [fallback, visible]);

  return { surface, pick, close, restoreFocus };
}

function Detail({ label, icon, children }: { label: string; icon?: "circle-dot" | "diamond"; children: ReactNode }) {
  return <div className="v2-detail"><dt>{icon && <Icon name={icon} size={15} />}{label}</dt><dd>{children}</dd></div>;
}

function Identity({ title, subtitle, icon }: { title: string; subtitle: string; icon: "robot" | "package" | "bolt" }) {
  return <div className="v2-identity">
    <span className="v2-identity-icon"><Icon name={icon} size={23} /></span>
    <div><h3>{title}</h3><p>{subtitle}</p></div>
  </div>;
}

function JobEndpoints({ job }: { job: Job }) {
  return <dl className="v2-endpoints">
    <Detail label="Pickup" icon="circle-dot">{cellLabel(job.from)}</Detail>
    <Detail label="Drop-off" icon="diamond">{cellLabel(job.to)}</Detail>
  </dl>;
}

function ActivityChip({ tone, children }: { tone?: RobotTone; children: ReactNode }) {
  const icons = { moving: "activity", charging: "bolt", waiting: "clock", fault: "x" } as const;
  return <span className="v2-status-chip" data-tone={tone}>
    {tone && <Icon name={icons[tone]} size={13} />}{children}
  </span>;
}

function RobotDetails({ robot, job, snapshot, inspect, expanded }: {
  robot: Robot; job?: Job; snapshot: Snapshot; inspect: (selection: Selection) => void; expanded: boolean;
}) {
  const status = robot.status.charAt(0).toUpperCase() + robot.status.slice(1);
  return <>
    <Identity title={robot.id} subtitle={robotRoleLabel(robot)} icon="robot" />
    <dl className="v2-inspector-group">
      <Detail label="Status">{status}</Detail>
      <Detail label="Activity"><ActivityChip tone={robotTone(robot)}>{robotActivityLabel(robot)}</ActivityChip></Detail>
      <Detail label="Battery"><BatteryCharge percent={robot.battery} /></Detail>
      {(expanded || robot.role === "maintenance" || robot.activeFault) && <Detail label="Position">{cellLabel(robot)}</Detail>}
      {job?.cargoRobotId && <Detail label="Cargo custody">Onboard {job.cargoRobotId} · {job.id}</Detail>}
      {expanded && robot.waitTicks > 0 && <Detail label="Traffic wait">{robot.waitTicks} {robot.waitTicks === 1 ? "tick" : "ticks"}</Detail>}
    </dl>
    <meter className="v2-battery" min={0} max={100} value={robot.battery} aria-label={robot.id + " battery"} />
    <RobotMaintenanceDetails snapshot={snapshot} robot={robot} inspect={inspect} />
    {robot.role === "transport" &&
    <section className="v2-inspector-group" aria-label="Current job">
      <h3>{robot.activeFault && job ? "Interrupted job" : "Current job"}</h3>
      {job ? <div className="v2-inspector-card">
        <div className="v2-job-title"><Icon name="package" size={18} /><strong>{job.id}</strong>
          <span className="v2-job-type">{job.type === "PRIORITY" ? "Priority" : "Normal"}</span></div>
        <JobEndpoints job={job} />
        <button type="button" className="v2-inspector-link" onClick={() => inspect({ kind: "job", id: job.id })}>
          View job<Icon name="arrow-right" size={16} />
        </button>
      </div> : <p className="v2-secondary">{robot.jobId ? "Job " + robot.jobId + " is outside the current snapshot." : "No job assigned"}</p>}
    </section>}
  </>;
}

function JobDetails({ job, inspect, expanded }: {
  job: Job; inspect?: (selection: Selection) => void; expanded: boolean;
}) {
  const overdue = isPriorityOverdue(job);
  return <>
    <Identity title={job.id} subtitle={job.type === "PRIORITY" ? "Priority job" : "Normal job"} icon="package" />
    <dl className="v2-inspector-group">
      <Detail label="Status">{jobLabels[job.status]}</Detail>
      <Detail label="Assigned robot">{job.assignedRobot || "Unassigned"}</Detail>
      {job.cargoRobotId && <Detail label="Cargo custody">Onboard {job.cargoRobotId}</Detail>}
      {job.deadlineTick !== undefined && <Detail label="Deadline">
        <span className={overdue ? "v2-overdue" : undefined}>Tick {job.deadlineTick}{overdue ? " · Overdue" : ""}</span>
      </Detail>}
      {expanded && job.status === "done" && job.outcome && <Detail label="Outcome">{jobOutcomeLabels[job.outcome]}</Detail>}
      {expanded && job.status === "done" && job.completedTick !== undefined &&
        <Detail label="Completed at">Tick {job.completedTick}</Detail>}
    </dl>
    <div className="v2-inspector-group"><JobEndpoints job={job} /></div>
    {job.assignedRobot && inspect && <button type="button" className="v2-inspector-link"
      onClick={() => inspect({ kind: "robot", id: job.assignedRobot })}>Inspect {job.assignedRobot}<Icon name="arrow-right" size={16} /></button>}
  </>;
}

function StationDetails({ station, inspect }: { station: Station; inspect: (selection: Selection) => void }) {
  const robot = station.occupiedBy || station.reservedBy;
  return <>
    <Identity title={station.id} subtitle="Charging station" icon="bolt" />
    <dl className="v2-inspector-group">
      <Detail label="Status"><ActivityChip tone={station.occupiedBy ? "charging" : undefined}>{chargerStatus(station)}</ActivityChip></Detail>
      <Detail label="Location">{cellLabel(station)}</Detail>
      <Detail label="Occupied by">{station.occupiedBy || "—"}</Detail>
      <Detail label="Reserved for">{station.reservedBy || "—"}</Detail>
    </dl>
    {robot && <button type="button" className="v2-inspector-link" onClick={() => inspect({ kind: "robot", id: robot })}>
      Inspect {robot}<Icon name="arrow-right" size={16} />
    </button>}
  </>;
}

export function useInspectorDialog(id: string | undefined, kind: string | undefined, close: () => void, restoreFocus: () => void) {
  const dialog = useRef<HTMLDialogElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const content = useRef<HTMLDivElement>(null);

  useLayoutEffect(() => {
    const panel = dialog.current;
    if (!panel) return;
    if (!id) {
      if (panel.open) {
        const focused = document.activeElement;
        panel.close();
        if (focused === document.body || panel.contains(focused)) restoreFocus();
      }
      return;
    }
    const narrow = window.matchMedia("(max-width: 1179px)");
    function sync(target: HTMLElement | null) {
      if (!panel) return;
      if (!panel.open || panel.matches(":modal") !== narrow.matches) {
        if (panel.open) panel.close();
        if (narrow.matches) panel.showModal();
        else panel.show();
      }
      target?.focus({ preventScroll: true });
    }
    function changePresentation() {
      const focused = document.activeElement;
      sync(focused instanceof HTMLElement && panel?.contains(focused) ? focused : heading.current);
    }
    if (content.current) content.current.scrollTop = 0;
    sync(heading.current);
    narrow.addEventListener("change", changePresentation);
    return () => narrow.removeEventListener("change", changePresentation);
  }, [id, kind, restoreFocus]);

  useEffect(() => {
    if (!id) return;
    function dismiss(event: KeyboardEvent): void {
      if (event.key !== "Escape" || event.defaultPrevented || !dialog.current?.open || dialog.current.matches(":modal")) return;
      event.preventDefault();
      close();
    }
    document.addEventListener("keydown", dismiss);
    return () => document.removeEventListener("keydown", dismiss);
  }, [id, close]);

  return { dialog, heading, content };
}

export function WarehouseInspector({
  entity, job, snapshot, inspect, close, restoreFocus, onWarehouse, lastConfirmedTick, canInspectAssigned = true,
}: {
  entity: Entity | null; job?: Job; snapshot?: Snapshot; inspect: (selection: Selection) => void; close: () => void; restoreFocus: () => void;
  onWarehouse?: () => void; lastConfirmedTick?: number; canInspectAssigned?: boolean;
}) {
  const titleId = useId();
  const last = useRef<{ entity: Entity; job?: Job } | null>(null);
  useLayoutEffect(() => {
    if (entity) last.current = { entity, job };
  }, [entity, job]);
  const current = entity ? { entity, job } : last.current;
  const id = entity?.kind === "robot" ? entity.robot.id : entity?.kind === "job" ? entity.job.id : entity?.station.id;
  const kind = entity?.kind;

  const { dialog, heading, content } = useInspectorDialog(id, kind, close, restoreFocus);

  const detail = snapshot ? current?.entity : undefined;
  const title = detail && { robot: "Robot details", job: "Job details", station: "Charger details" }[detail.kind];
  return <dialog ref={dialog} className="v2-panel v2-inspector" inert={!entity} aria-labelledby={detail ? titleId : undefined}
    onCancel={event => { event.preventDefault(); close(); }}>
    {detail && <>
      <header className="v2-panel-title">
        <h2 id={titleId} ref={heading} tabIndex={-1}>{title}</h2>
        <button type="button" className="v2-map-control v2-inspector-close" aria-label="Close inspector" title="Close inspector" onClick={close}>
          <Icon name="x" size={17} />
        </button>
      </header>
      <div className="v2-inspector-content" ref={content}>
        {lastConfirmedTick !== undefined && <p className="v2-directory-notice">Last confirmed at tick {lastConfirmedTick}.</p>}
        {detail.kind === "robot" && snapshot && <RobotDetails robot={detail.robot} job={current?.job} snapshot={snapshot} inspect={inspect} expanded={!!onWarehouse} />}
        {detail.kind === "job" && <JobDetails job={detail.job} inspect={canInspectAssigned ? inspect : undefined} expanded={!!onWarehouse} />}
        {detail.kind === "station" && <StationDetails station={detail.station} inspect={inspect} />}
      </div>
      {onWarehouse && <footer className="v2-directory-inspector-footer">
        <button type="button" className="v2-inspector-link" onClick={onWarehouse}>
          View on Warehouse map<Icon name="arrow-right" size={16} />
        </button>
      </footer>}
    </>}
  </dialog>;
}
