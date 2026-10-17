import type { JSX, KeyboardEvent } from "react";
import type { FleetSnapshot, Robot } from "./fleet";

export interface RobotSelection {
  selectedRobotID?: string | null;
  onSelectRobot?: (id: string) => void;
}

export function robotDescription(robot: Robot): string {
  return `${robot.id}: ${robot.status}${robot.blocked ? ", traffic blocked" : ""}, battery ${robot.battery}%, job ${robot.jobId || "none"}, position (${robot.x},${robot.y})`;
}

function selectWithKeyboard(event: KeyboardEvent<SVGGElement>, id: string, select?: (id: string) => void): void {
  if (event.key !== "Enter" && event.key !== " ") return;
  event.preventDefault();
  select?.(id);
}

export function Warehouse({ fleet, selectedRobotID, onSelectRobot }: { fleet: FleetSnapshot } & RobotSelection): JSX.Element {
  const { layout, robots, stations, activeIncidents } = fleet;
  const size = 40;
  return <div className="map-scroll"><svg className="warehouse" viewBox={`-24 -25 ${layout.width * size + 30} ${layout.height * size + 37}`} role="group" aria-label={`Warehouse: ${layout.width} columns by ${layout.height} rows, ${robots.length} robots`}>
    {Array.from({ length: layout.width }, (_, x) => <text key={`x${x}`} x={x * size + 20} y={-10} className="coordinate" textAnchor="middle">{x}</text>)}
    {Array.from({ length: layout.height }, (_, y) => <text key={`y${y}`} x={-12} y={y * size + 24} className="coordinate" textAnchor="middle">{y}</text>)}
    {layout.cells.map(cell => <rect key={`${cell.x},${cell.y}`} x={cell.x * size} y={cell.y * size} width={size} height={size} className={`cell cell-${cell.kind}`} />)}
    {stations.map(s => <g key={s.id}><text x={s.x * size + 20} y={s.y * size + 36} textAnchor="middle" className="station-label">{s.id}</text><title>{`${s.id}: ${s.occupiedBy ? `occupied by ${s.occupiedBy}` : s.reservedBy ? `reserved for ${s.reservedBy}` : "available"}`}</title></g>)}
    {robots.map(r => <g key={r.id} transform={`translate(${r.x * size + 20},${r.y * size + 18})`}
      className={`robot robot-${r.status}${r.blocked ? " robot-blocked" : ""}${activeIncidents.some(i => i.robotId === r.id) ? " robot-affected" : ""}${selectedRobotID === r.id ? " robot-selected" : ""}`}
      role={onSelectRobot ? "button" : undefined} tabIndex={onSelectRobot ? 0 : undefined}
      aria-label={`Select ${robotDescription(r)}`} aria-pressed={onSelectRobot ? selectedRobotID === r.id : undefined}
      onClick={() => onSelectRobot?.(r.id)} onKeyDown={event => selectWithKeyboard(event, r.id, onSelectRobot)}>
      <title>{robotDescription(r)}</title>
      <rect className="selection-ring" x={-19} y={-17} width={38} height={35} rx={10} />
      <rect className="robot-body" x={-14} y={-12} width={28} height={25} rx={6} />
      <text textAnchor="middle" y={4}>{r.id}</text>
      {r.jobId && <text textAnchor="middle" y={27} className="robot-job">{r.jobId}</text>}
      {r.blocked && <text x={16} y={-12} className="blocked-mark">!</text>}
      {r.status === "offline" && <text x={16} y={-12} className="offline-mark">×</text>}
    </g>)}
  </svg></div>;
}

export function RobotCard({ robot: r, selectedRobotID, onSelectRobot }: { robot: Robot } & RobotSelection): JSX.Element {
  const status = r.status.charAt(0).toUpperCase() + r.status.slice(1);
  return <li className={`robot-card${selectedRobotID === r.id ? " is-selected" : ""}`} data-robot-id={r.id}>
    <button className="robot-selector" type="button" aria-label={`Inspect ${robotDescription(r)}`} aria-pressed={selectedRobotID === r.id} onClick={() => onSelectRobot?.(r.id)}>
      <span className="robot-heading"><strong><span className={`dot dot-${r.status}`} />{r.id}</strong><span className={`status status-${r.status}`}>{status}</span></span>
      <span className="robot-meta"><span>Job <b>{r.jobId || "—"}</b></span><span>({r.x}, {r.y})</span><strong className={r.battery < 30 ? "low-battery" : ""}>{r.battery}%</strong></span>
      <meter className={r.battery < 30 ? "battery-low" : ""} min={0} max={100} value={r.battery} aria-label={`${r.id} battery ${r.battery}%`} />
      {r.blocked && <span className="blocked-note">Traffic blocked · waiting {r.waitTicks} ticks</span>}
      {(r.status === "faulted" || r.status === "offline") && <span className="fault-note">{r.status === "offline" ? "Robot unresponsive" : "Robot fault reported"}</span>}
    </button>
  </li>;
}
