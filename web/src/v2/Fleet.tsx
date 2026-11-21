import { BatteryCharge } from "./BatteryCharge";
import { Icon } from "./WarehouseIcon";
import { breakdownLabels, cellLabel, maintenanceForRobot, maintenanceOutcomeLabels, maintenancePhaseLabel,
  maintenanceReasonLabels, robotActivityLabel, robotRoleLabel, robotTone, type PickEntity, type Selection } from "./warehousePresentation";
import type { Snapshot } from "./telemetry";

export function Fleet({ snapshot, selection, pick }: {
  snapshot: Snapshot; selection: Selection | null; pick: PickEntity;
}) {
  const { robots } = snapshot;
  return <section className="v2-panel v2-directory-panel" aria-labelledby="v2-robots-title">
    <header className="v2-directory-header">
      <h2 id="v2-robots-title" tabIndex={-1}>Robots</h2>
      <p>{robots.length} robots</p>
    </header>
    <ul className="v2-fleet-grid" aria-label="Fleet robots">
      {robots.map(robot => {
        const { breakdown, attempt, recent, reason } = maintenanceForRobot(snapshot, robot);
        const isM1 = robot.role === "maintenance";
        const activity = isM1 && (attempt || snapshot.maintenance?.phase === "returning")
          ? maintenancePhaseLabel(snapshot) : robotActivityLabel(robot);
        const assignment = isM1 ? attempt && "Target " + attempt.robotId
          : robot.jobId ? "Job " + robot.jobId : "No job assigned";
        return <li key={robot.id}>
          <button type="button" className="v2-robot-card v2-fleet-tile"
            aria-pressed={selection?.kind === "robot" && selection.id === robot.id}
            data-tone={robotTone(robot)} onClick={event => pick({ kind: "robot", id: robot.id }, event.currentTarget)}>
            <span className="v2-fleet-tile-title"><Icon name="robot" size={19} /><strong>{robot.id}</strong></span>
            <BatteryCharge percent={robot.battery} />
            <span className="v2-card-activity">{activity}</span>
            <span className="v2-directory-muted">{robotRoleLabel(robot)}</span>
            <span className="v2-directory-muted v2-fleet-tile-location">
              <span>{cellLabel(robot)}</span>{assignment && <span>{assignment}</span>}
            </span>
            {robot.activeFault && <span className="v2-fleet-maintenance" data-tone="fault">
              {breakdownLabels[robot.activeFault]}{breakdown && " · " + breakdown.id}
            </span>}
            {attempt && <span className="v2-fleet-maintenance">{
              isM1 ? attempt.id + " · " + attempt.faultId : "M1 · " + maintenancePhaseLabel(snapshot)
            }</span>}
            {reason && <span className="v2-fleet-maintenance">{maintenanceReasonLabels[reason]}</span>}
            {!attempt && recent?.outcome && <span className="v2-fleet-maintenance">{maintenanceOutcomeLabels[recent.outcome]} · {recent.faultId}</span>}
          </button>
        </li>;
      })}
    </ul>
  </section>;
}
