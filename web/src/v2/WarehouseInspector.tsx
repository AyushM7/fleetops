import { useLayoutEffect, useRef, type ReactNode } from "react";
import { Icon } from "./WarehouseIcon";
import { activityLabels, chargerStatus, jobLabels, type Entity, type Selection } from "./warehousePresentation";
import type { Job, Point, Robot, Station } from "./telemetry";

const cell = (point: Point) => "Cell " + point.x + ", " + point.y;

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
    <Detail label="Pickup" icon="circle-dot">{cell(job.from)}</Detail>
    <Detail label="Drop-off" icon="diamond">{cell(job.to)}</Detail>
  </dl>;
}

function RobotDetails({ robot, job, inspect }: { robot: Robot; job?: Job; inspect: (selection: Selection) => void }) {
  const status = robot.status.charAt(0).toUpperCase() + robot.status.slice(1);
  return <>
    <Identity title={robot.id} subtitle={robot.role === "maintenance" ? "Maintenance robot" : "Transport robot"} icon="robot" />
    <dl className="v2-inspector-group">
      <Detail label="Status">{status}</Detail>
      <Detail label="Activity">{activityLabels[robot.activity]}</Detail>
      <Detail label="Battery"><strong>{robot.battery}%</strong></Detail>
    </dl>
    <meter className="v2-battery" min={0} max={100} value={robot.battery} aria-label={robot.id + " battery"} />
    <section className="v2-inspector-group" aria-labelledby="v2-current-job">
      <h3 id="v2-current-job">Current job</h3>
      {job ? <div className="v2-inspector-card">
        <div className="v2-job-title"><Icon name="package" size={18} /><strong>{job.id}</strong>
          <span className="v2-job-type">{job.type === "PRIORITY" ? "Priority" : "Normal"}</span></div>
        <JobEndpoints job={job} />
        <button type="button" className="v2-inspector-link" onClick={() => inspect({ kind: "job", id: job.id })}>
          View job<Icon name="arrow-right" size={16} />
        </button>
      </div> : <p className="v2-secondary">{robot.jobId ? "Job " + robot.jobId + " is outside the current snapshot." : "No job assigned"}</p>}
    </section>
  </>;
}

function JobDetails({ job, inspect }: { job: Job; inspect: (selection: Selection) => void }) {
  const overdue = job.type === "PRIORITY" && job.deadlineMissed && job.status !== "done";
  return <>
    <Identity title={job.id} subtitle={job.type === "PRIORITY" ? "Priority job" : "Normal job"} icon="package" />
    <dl className="v2-inspector-group">
      <Detail label="Status">{jobLabels[job.status]}</Detail>
      <Detail label="Assigned robot">{job.assignedRobot || "Unassigned"}</Detail>
      {job.deadlineTick !== undefined && <Detail label="Deadline">
        <span className={overdue ? "v2-amber" : undefined}>Tick {job.deadlineTick}{overdue ? " · Overdue" : ""}</span>
      </Detail>}
    </dl>
    <div className="v2-inspector-group"><JobEndpoints job={job} /></div>
    {job.assignedRobot && <button type="button" className="v2-inspector-link"
      onClick={() => inspect({ kind: "robot", id: job.assignedRobot })}>Inspect {job.assignedRobot}<Icon name="arrow-right" size={16} /></button>}
  </>;
}

function StationDetails({ station, inspect }: { station: Station; inspect: (selection: Selection) => void }) {
  const robot = station.occupiedBy || station.reservedBy;
  return <>
    <Identity title={station.id} subtitle="Charging station" icon="bolt" />
    <dl className="v2-inspector-group">
      <Detail label="Status">{chargerStatus(station)}</Detail>
      <Detail label="Location">{cell(station)}</Detail>
      <Detail label="Occupied by">{station.occupiedBy || "—"}</Detail>
      <Detail label="Reserved for">{station.reservedBy || "—"}</Detail>
    </dl>
    {robot && <button type="button" className="v2-inspector-link" onClick={() => inspect({ kind: "robot", id: robot })}>
      Inspect {robot}<Icon name="arrow-right" size={16} />
    </button>}
  </>;
}

export function WarehouseInspector({ entity, job, inspect, close }: {
  entity: Entity; job?: Job; inspect: (selection: Selection) => void; close: () => void;
}) {
  const heading = useRef<HTMLHeadingElement>(null);
  const id = entity.kind === "robot" ? entity.robot.id : entity.kind === "job" ? entity.job.id : entity.station.id;
  useLayoutEffect(() => { heading.current?.focus({ preventScroll: true }); }, [entity.kind, id]);

  const title = { robot: "Robot details", job: "Job details", station: "Charger details" }[entity.kind];
  return <aside className="v2-panel v2-inspector" aria-labelledby="v2-inspector-title">
    <header className="v2-panel-title">
      <h2 id="v2-inspector-title" ref={heading} tabIndex={-1}>{title}</h2>
      <button type="button" className="v2-map-control v2-inspector-close" aria-label="Close inspector" title="Close inspector" onClick={close}>
        <Icon name="x" size={17} />
      </button>
    </header>
    <div className="v2-inspector-content">
      {entity.kind === "robot" && <RobotDetails robot={entity.robot} job={job} inspect={inspect} />}
      {entity.kind === "job" && <JobDetails job={entity.job} inspect={inspect} />}
      {entity.kind === "station" && <StationDetails station={entity.station} inspect={inspect} />}
    </div>
  </aside>;
}
