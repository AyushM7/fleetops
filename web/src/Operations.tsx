import type { JSX } from "react";
import type { FleetSnapshot, Incident, Job } from "./fleet";
import type { RobotSelection } from "./Warehouse";

const incidentLabels: Record<Incident["type"], string> = {
  low_battery: "Low battery with an assigned job", robot_unresponsive: "Robot offline", traffic_blocked: "Traffic blocked",
};
const jobLabels: Record<Job["status"], string> = {
  pending: "Waiting for assignment", assigned: "Assigned · awaiting pickup", in_progress: "Picked up · delivering", done: "Delivered",
};

type OperationsProps = { fleet: FleetSnapshot; stale: boolean } & RobotSelection;

export function ActiveIncidents({ fleet, stale, selectedRobotID, onSelectRobot }: OperationsProps): JSX.Element {
  return <section className="operations-panel" aria-label="Active incidents">
      <div className="panel-heading"><div><h2>Active incidents</h2></div><span className="condition-count">{fleet.activeIncidents.length} active</span></div>
      {stale && <p className="inbox-message decision-error">Last received incidents; current conditions cannot be confirmed.</p>}
      {fleet.activeIncidents.length === 0 ? <p className="inbox-message">{stale ? "No incidents in the last received snapshot." : "No active incidents in this snapshot."}</p>
        : <ul className="incident-list">{fleet.activeIncidents.map(i => <li key={`${i.robotId}:${i.type}`} className={`incident-card${selectedRobotID === i.robotId ? " is-related" : ""}`}>
          <div className="proposal-summary"><h3>{i.robotId} · {incidentLabels[i.type]}</h3><span className={`severity severity-${i.severity}`}>{i.severity === "high" ? "High severity" : "Medium severity"}</span></div>
          <p className="incident-context">Job {i.telemetry.jobId || "none"} · waiting {i.telemetry.waitTicks} ticks</p>
          <div className="incident-tools"><details className="incident-evidence"><summary>Telemetry and context</summary><p>Battery {i.telemetry.battery}% · {i.telemetry.status} · position ({i.telemetry.x}, {i.telemetry.y})</p>
          <p>Job {i.telemetry.jobId || "none"} · waiting {i.telemetry.waitTicks} ticks · blocked {i.telemetry.blocked ? "yes" : "no"}</p>
          <p className="incident-help">Operator attention required. An incident does not by itself authorize an action.</p></details>
          {onSelectRobot && <button type="button" className="text-button" aria-pressed={selectedRobotID === i.robotId} onClick={() => onSelectRobot(i.robotId)}>Locate {i.robotId}</button>}</div>
        </li>)}</ul>}
    </section>;
}

export function JobProgress({ fleet, stale }: OperationsProps): JSX.Element {
  return <section className="operations-panel" aria-label="Job progress">
      <div className="panel-heading"><div><h2>Job progress</h2><p>Approval transfers the assignment. Delivery is confirmed only when Go reports it.</p></div></div>
      {stale && <p className="inbox-message decision-error">Last received job state; progress may be stale.</p>}
      {fleet.jobs.length === 0 ? <p className="inbox-message">No jobs in this snapshot.</p> : <div className="jobs-scroll"><table className="jobs-table"><thead><tr><th>Job</th><th>Assigned robot</th><th>Pickup → delivery</th><th>Progress</th></tr></thead><tbody>
        {fleet.jobs.map(j => { const robot = fleet.robots.find(r => r.id === j.assignedRobot);
          return <tr key={j.id} data-job-id={j.id}><th scope="row">{j.id}</th><td>{j.assignedRobot || "Unassigned"}{robot && j.status !== "done" && <small>{robot.status} · ({robot.x}, {robot.y})</small>}</td><td>({j.fromX}, {j.fromY}) → ({j.toX}, {j.toY})</td><td>{jobLabels[j.status]}</td></tr>;
        })}
      </tbody></table></div>}
    </section>;
}

export function Operations(props: OperationsProps): JSX.Element {
  return <div className={`operations ${props.stale ? "stale" : ""}`}><ActiveIncidents {...props} /><JobProgress {...props} /></div>;
}
