import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { Icon } from "./WarehouseIcon";
import { WarehouseInspector } from "./WarehouseInspector";
import { WarehouseMap } from "./WarehouseMap";
import { findEntity, robotActivityLabel, robotTone, warehouseCounts, type PickEntity, type Selection } from "./warehousePresentation";
import type { Snapshot, WarehouseFrame } from "./telemetry";

function WarehouseMetrics({ snapshot }: { snapshot?: Snapshot }) {
  const counts = snapshot ? warehouseCounts(snapshot) : null;
  return <dl className="v2-metrics" aria-label="Warehouse summary">
    <div className="v2-panel v2-metric">
      <dt><span className="v2-metric-icon"><Icon name="package" size={18} /></span>Completed deliveries</dt>
      <dd>{counts?.completed ?? "—"}</dd>
    </div>
    <div className="v2-panel v2-metric" data-tone={counts?.overdue ? "fault" : undefined}>
      <dt><span className="v2-metric-icon"><Icon name="clock" size={18} /></span>Priority overdue</dt>
      <dd>{counts?.overdue ?? "—"}</dd>
    </div>
    <div className="v2-panel v2-metric" data-tone={counts?.charging ? "charging" : undefined}>
      <dt><span className="v2-metric-icon"><Icon name="bolt" size={18} /></span>Charging robots</dt>
      <dd>{counts?.charging ?? "—"}</dd>
    </div>
  </dl>;
}

function FleetStrip({ robots, selection, pick, viewFleet }: {
  robots: Snapshot["robots"]; selection: Selection | null; pick: PickEntity; viewFleet: () => void;
}) {
  const strip = useRef<HTMLUListElement>(null);
  const [overflowing, setOverflowing] = useState(false);
  useLayoutEffect(() => {
    const list = strip.current;
    if (!list) return;
    const measure = () => setOverflowing(list.scrollWidth > list.clientWidth + 1);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(list);
    return () => observer.disconnect();
  }, [robots.length]);

  return <section className="v2-panel v2-fleet" aria-labelledby="v2-fleet-title">
    <header><h2 id="v2-fleet-title">Fleet</h2><p>{robots.length} robots</p>
      <button type="button" className="v2-fleet-link" onClick={viewFleet}>View fleet<Icon name="arrow-right" size={13} /></button></header>
    <ul ref={strip} className="v2-fleet-scroll" aria-label="Fleet robots"
      aria-description={overflowing ? "Scroll horizontally to see more robots." : undefined} tabIndex={overflowing ? 0 : undefined}>
      {robots.map(robot => <li key={robot.id}>
      <button type="button" className="v2-robot-card" aria-pressed={selection?.kind === "robot" && selection.id === robot.id}
        data-tone={robotTone(robot)} aria-label={'Inspect ' + robot.id + (robot.role === "maintenance" ? " maintenance robot" : "")}
        aria-describedby={"v2-fleet-activity-" + robot.id + " v2-fleet-battery-" + robot.id}
        onClick={event => pick({ kind: "robot", id: robot.id }, event.currentTarget)}>
        <strong>{robot.id}{robot.role === "maintenance" && <Icon name="diamond" size={13} />}</strong>
        <span className="v2-card-charge" id={"v2-fleet-battery-" + robot.id}>{robot.battery}%</span>
        <span className="v2-card-activity" id={"v2-fleet-activity-" + robot.id}>{robotActivityLabel(robot)}</span>
      </button>
    </li>)}</ul>
  </section>;
}

export function Warehouse({ frame, selection, selectEntity, viewFleet, hidden }: {
  frame: WarehouseFrame | null; selection: Selection | null; selectEntity: (selection: Selection | null) => void;
  viewFleet: () => void; hidden: boolean;
}) {
  const opener = useRef<HTMLElement | SVGElement | null>(null);
  const surface = useRef<HTMLDivElement>(null);
  const restore = useRef(false);
  const entity = frame ? findEntity(frame.snapshot, selection) : null;
  const hasInspector = entity !== null;
  const job = entity?.kind === "job" ? entity.job
    : entity?.kind === "robot" ? frame?.snapshot.jobs.find(item => item.id === entity.robot.jobId) : undefined;

  const close = useCallback(() => {
    restore.current = true;
    selectEntity(null);
  }, [selectEntity]);

  useLayoutEffect(() => {
    if (hasInspector || !restore.current) return;
    const source = opener.current;
    restore.current = false;
    opener.current = null;
    if (!hidden) {
      if (source?.isConnected && source.getAttribute("tabindex") !== "-1") source.focus({ preventScroll: true });
      else surface.current?.querySelector<HTMLElement>(".v2-map-stage")?.focus({ preventScroll: true });
    }
  }, [hasInspector, hidden]);

  useEffect(() => {
    if (!hasInspector || hidden) return;
    function dismiss(event: KeyboardEvent) {
      if (event.key !== "Escape" || event.defaultPrevented || window.matchMedia("(max-width: 1179px)").matches) return;
      event.preventDefault();
      close();
    }
    document.addEventListener("keydown", dismiss);
    return () => document.removeEventListener("keydown", dismiss);
  }, [hasInspector, hidden, close]);

  const pick: PickEntity = (nextSelection, source) => {
    opener.current = source;
    selectEntity(nextSelection);
  };

  return <div className="v2-warehouse" hidden={hidden} ref={surface}>
    <WarehouseMetrics snapshot={frame?.snapshot} />
    {frame ? <>
      <div className={'v2-map-row' + (entity ? ' has-inspector' : '')}>
        <WarehouseMap frame={frame} selection={selection} job={job} pick={pick} />
        <WarehouseInspector entity={hidden ? null : entity} job={job} inspect={selectEntity} close={close} />
      </div>
      <FleetStrip robots={frame.snapshot.robots} selection={selection} pick={pick} viewFleet={viewFleet} />
    </> : <section className="v2-panel v2-empty"><h2>Warehouse map</h2><p>Waiting for confirmed warehouse telemetry.</p></section>}
  </div>;
}
