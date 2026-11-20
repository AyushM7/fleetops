import { Icon } from "./WarehouseIcon";
import { cellLabel, robotActivityLabel, robotRoleLabel, robotTone, type PickEntity, type Selection } from "./warehousePresentation";
import type { Robot } from "./telemetry";

export function Fleet({ robots, selection, pick }: {
  robots: Robot[]; selection: Selection | null; pick: PickEntity;
}) {
  return <section className="v2-panel v2-directory-panel" aria-labelledby="v2-robots-title">
    <header className="v2-directory-header">
      <h2 id="v2-robots-title" tabIndex={-1}>Robots</h2>
      <p>{robots.length} robots</p>
    </header>
    <ul className="v2-fleet-grid" aria-label="Fleet robots">
      {robots.map(robot => <li key={robot.id}>
        <button type="button" className="v2-robot-card v2-fleet-tile"
          aria-pressed={selection?.kind === "robot" && selection.id === robot.id}
          data-tone={robotTone(robot)} onClick={event => pick({ kind: "robot", id: robot.id }, event.currentTarget)}>
          <span className="v2-fleet-tile-title"><Icon name="robot" size={19} /><strong>{robot.id}</strong></span>
          <span className="v2-fleet-tile-battery"><Icon name="battery" size={17} />{robot.battery}%</span>
          <span className="v2-card-activity">{robotActivityLabel(robot)}</span>
          <span className="v2-directory-muted">{robotRoleLabel(robot)}</span>
          <span className="v2-directory-muted v2-fleet-tile-location">
            <span>{cellLabel(robot)}</span><span>{robot.jobId ? "Job " + robot.jobId : "No job assigned"}</span>
          </span>
        </button>
      </li>)}
    </ul>
  </section>;
}
