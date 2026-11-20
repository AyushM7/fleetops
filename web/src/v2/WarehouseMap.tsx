import { useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent, type ReactNode } from "react";
import { Icon } from "./WarehouseIcon";
import { activityLabels, chargerStatus, type PickEntity, type Selection } from "./warehousePresentation";
import type { Job, Layout, Point, Robot, Station, WarehouseFrame } from "./telemetry";

type Camera = Point & { scale: number };
const mapKeys = [["road", "Aisle"], ["wall", "Wall"], ["bolt", "Charger"], ["parking", "Parking"], ["diamond", "Maintenance bay"]] as const;

export function fitWarehouseCamera(layout: Layout, width: number, height: number, inspectorSpace = 0): Camera {
  // Leave room for the desktop inspector without shifting the initial camera on selection.
  const center = inspectorSpace ? width * 0.448 : width / 2;
  const clearWidth = 2 * Math.min(center, width - inspectorSpace - center);
  const scale = Math.max(1, Math.min((clearWidth - 24) / layout.width, (height - 16) / layout.height));
  return { x: center - layout.width * scale / 2, y: (height - layout.height * scale) / 2, scale };
}

function wallBlocks(layout: Layout) {
  const blocked = new Set(layout.cells.filter(cell => cell.kind === "wall").map(cell => cell.y * layout.width + cell.x));
  const latest = new Map<string, Point & { width: number; height: number }>();
  const blocks: (Point & { width: number; height: number })[] = [];
  for (let y = 0; y < layout.height; y++) {
    for (let x = 0; x < layout.width; x++) {
      if (!blocked.has(y * layout.width + x)) continue;
      const start = x;
      while (x + 1 < layout.width && blocked.has(y * layout.width + x + 1)) x++;
      const width = x - start + 1;
      const key = start + ":" + width;
      const block = latest.get(key);
      if (block && block.y + block.height === y) {
        block.height++;
      } else {
        const next = { x: start, y, width, height: 1 };
        latest.set(key, next);
        blocks.push(next);
      }
    }
  }
  return blocks;
}

function MapTarget({ target, point, label, selection, pick, children, radius = 0.48 }: {
  target: Selection; point: Point; label: string; selection: Selection | null; pick: PickEntity; children: ReactNode; radius?: number;
}) {
  const active = target.kind === selection?.kind && target.id === selection.id;
  return <g className="v2-map-target" transform={'translate(' + (point.x + 0.5) + ' ' + (point.y + 0.5) + ')'}
    role="button" tabIndex={0} aria-label={label} aria-pressed={active}
    onClick={event => pick(target, event.currentTarget)}
    onKeyDown={event => {
      if (event.key !== "Enter" && event.key !== " ") return;
      event.preventDefault();
      pick(target, event.currentTarget);
    }}>
    <title>{label}</title>
    <rect className="v2-target-hit" x={-radius} y={-radius} width={radius * 2} height={radius * 2} fill="transparent" />
    <rect className="v2-target-ring" x={-radius + 0.02} y={-radius + 0.02} width={(radius - 0.02) * 2} height={(radius - 0.02) * 2} rx={Math.min(0.18, radius / 2)} />
    {children}
  </g>;
}

function ChargerMarker({ station, selection, pick }: { station: Station; selection: Selection | null; pick: PickEntity }) {
  const status = chargerStatus(station);
  return <MapTarget target={{ kind: "station", id: station.id }} point={station} label={station.id + " charging station, " + status}
    selection={selection} pick={pick}>
    <rect className={'v2-station-tile' + (station.occupiedBy ? ' is-occupied' : '')} x={-0.4} y={-0.4} width={0.8} height={0.8} rx={0.1} />
    <Icon name="bolt" size={0.49} x={-0.245} y={-0.245} />
    <rect x={-0.45} y={0.49} width={0.9} height={0.43} fill="transparent" />
    <text className={station.occupiedBy ? "v2-station-label is-occupied" : "v2-station-label"} y={0.83}
      textAnchor="middle">{station.id}{status === "Reserved" ? " · Reserved" : ""}</text>
  </MapTarget>;
}

function RobotMarker({ robot, selection, pick }: { robot: Robot; selection: Selection | null; pick: PickEntity }) {
  return <MapTarget target={{ kind: "robot", id: robot.id }} point={robot}
    label={robot.id + (robot.role === "maintenance" ? " maintenance robot, " : ", ") + activityLabels[robot.activity] + ", battery " + robot.battery + "%"}
    selection={selection} pick={pick}>
    {robot.role === "maintenance"
      ? <polygon className="v2-robot-marker is-maintenance" points="-.42,0 -.26,-.29 .26,-.29 .42,0 .26,.29 -.26,.29" />
      : <rect className={'v2-robot-marker' + (robot.activity === "CHARGING" ? ' is-charging' : '')}
        x={-0.38} y={-0.29} width={0.76} height={0.58} rx={0.14} />}
    <text className="v2-robot-id" y={0.015} textAnchor="middle" dominantBaseline="central">{robot.id}</text>
    {(robot.blocked || robot.activity === "WAITING_FOR_TRAFFIC") && <circle className="v2-marker-wait" cx={0.34} cy={-0.29} r={0.08} />}
    {(robot.status === "faulted" || robot.status === "offline") && <circle className="v2-marker-fault" cx={0.34} cy={-0.29} r={0.08} />}
  </MapTarget>;
}

function JobMarker({ job, point, label, selection, pick, occupied }: {
  job: Job; point: Point; label: "Pickup" | "Drop-off"; selection: Selection | null; pick: PickEntity; occupied: boolean;
}) {
  // Keep markers visible in the same cell when a robot or charger occupies its center.
  const anchor = occupied ? { x: point.x + 0.32, y: point.y - 0.32 } : point;
  const size = occupied ? 0.13 : 0.3;
  return <MapTarget target={{ kind: "job", id: job.id }} point={anchor} label={label + " for " + job.id} selection={selection} pick={pick}
    radius={occupied ? 0.17 : 0.48}>
    {label === "Pickup"
      ? <circle className="v2-job-pin" r={size} />
      : <polygon className="v2-job-pin" points={'0,' + -size + ' ' + size + ',0 0,' + size + ' ' + -size + ',0'} />}
    <circle className="v2-job-center" r={occupied ? 0.04 : 0.08} />
  </MapTarget>;
}

export function WarehouseMap({ frame, selection, job, pick }: { frame: WarehouseFrame; selection: Selection | null; job?: Job; pick: PickEntity }) {
  const { layout, snapshot } = frame;
  const walls = useMemo(() => wallBlocks(layout), [layout]);
  const occupied = (point: Point) => [...snapshot.robots, ...snapshot.stations].some(entity => entity.x === point.x && entity.y === point.y);
  const svg = useRef<SVGSVGElement>(null);
  const stage = useRef<HTMLDivElement>(null);
  const drag = useRef<{ id: number; pointer: Point; start: Camera } | null>(null);
  const [viewport, setViewport] = useState({ width: 1250, height: 394, inspectorSpace: 332 });
  const [manualCamera, setCamera] = useState<Camera | null>(null);
  const fit = fitWarehouseCamera(layout, viewport.width, viewport.height, viewport.inspectorSpace);
  const camera = manualCamera ?? fit;
  const minScale = fit.scale / 2;
  const maxScale = fit.scale * 4;

  useLayoutEffect(() => {
    const canvas = svg.current;
    if (!canvas) return;
    let previous: { width: number; height: number; inspectorSpace: number } | undefined;
    function measure() {
      if (!canvas?.clientWidth || !canvas.clientHeight) return;
      const inspectorSpace = Number.parseFloat(getComputedStyle(canvas).getPropertyValue("--v2-inspector-space")) || 0;
      const measured = { width: canvas.clientWidth, height: canvas.clientHeight, inspectorSpace };
      if (previous?.width === measured.width && previous.height === measured.height && previous.inspectorSpace === inspectorSpace) return;
      if (previous) setCamera(null);
      previous = measured;
      setViewport(measured);
    }
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(canvas);
    return () => observer.disconnect();
  }, []);

  function zoom(factor: number): void {
    const scale = Math.min(maxScale, Math.max(minScale, camera.scale * factor));
    const ratio = scale / camera.scale;
    const cx = (stage.current?.clientWidth ?? viewport.width) / 2;
    const cy = viewport.height / 2;
    setCamera({ x: cx - (cx - camera.x) * ratio, y: cy - (cy - camera.y) * ratio, scale });
  }

  function pan(event: KeyboardEvent<HTMLDivElement>): void {
    if (event.target !== event.currentTarget) return;
    const offsets: Record<string, Point> = { ArrowLeft: { x: 36, y: 0 }, ArrowRight: { x: -36, y: 0 },
      ArrowUp: { x: 0, y: 36 }, ArrowDown: { x: 0, y: -36 } };
    const offset = offsets[event.key];
    if (!offset) return;
    event.preventDefault();
    setCamera({ ...camera, x: camera.x + offset.x, y: camera.y + offset.y });
  }

  function beginDrag(event: PointerEvent<HTMLDivElement>): void {
    if (event.button !== 0 || !(event.target instanceof Element) || event.target.closest("[role=button]")) return;
    drag.current = { id: event.pointerId, pointer: { x: event.clientX, y: event.clientY }, start: camera };
    event.currentTarget.setPointerCapture(event.pointerId);
  }

  function moveDrag(event: PointerEvent<HTMLDivElement>): void {
    const active = drag.current;
    if (!active || active.id !== event.pointerId) return;
    setCamera({ ...active.start, x: active.start.x + event.clientX - active.pointer.x, y: active.start.y + event.clientY - active.pointer.y });
  }

  function cancelDrag(event: PointerEvent<HTMLDivElement>): void {
    if (drag.current?.id === event.pointerId) drag.current = null;
  }

  return <section className="v2-panel v2-map-panel" aria-labelledby="v2-map-title">
    <header className="v2-panel-title">
      <h2 id="v2-map-title">Warehouse map</h2>
      <span>{layout.width} × {layout.height}</span>
      <div className="v2-map-tools" aria-label="Map controls" role="group">
        <button type="button" className="v2-map-control" aria-label="Zoom out" title="Zoom out" disabled={camera.scale <= minScale}
          onClick={() => zoom(0.8)}><Icon name="minus" size={16} /></button>
        <button type="button" className="v2-map-control" aria-label="Zoom in" title="Zoom in" disabled={camera.scale >= maxScale}
          onClick={() => zoom(1.25)}><Icon name="plus" size={16} /></button>
        <button type="button" className="v2-map-control" aria-label="Fit warehouse map" title="Fit warehouse map"
          onClick={() => setCamera(fitWarehouseCamera(layout, stage.current?.clientWidth ?? viewport.width, viewport.height))}>
          <Icon name="maximize" size={16} />
        </button>
      </div>
    </header>
    <div className="v2-map-stage" ref={stage} tabIndex={0} role="group" aria-label="Warehouse map. Use arrow keys to pan."
      onKeyDown={pan} onPointerDown={beginDrag} onPointerMove={moveDrag} onPointerUp={cancelDrag}
      onPointerCancel={cancelDrag} onLostPointerCapture={cancelDrag}>
      <svg ref={svg} className="v2-map-svg" role="group" aria-label="Live warehouse floor plan"
        viewBox={'0 0 ' + viewport.width + ' ' + viewport.height}>
        <defs><pattern id="v2-wall-grid" width={1} height={1} patternUnits="userSpaceOnUse">
          <rect className="v2-wall-fill" width={1} height={1} />
          <path className="v2-wall-grid-line" d="M 0 0 H 1 M 0 0 V 1" />
        </pattern></defs>
        <g data-testid="warehouse-floor" transform={'translate(' + camera.x + ' ' + camera.y + ') scale(' + camera.scale + ')'}>
          {layout.cells.map(cell => <rect key={cell.x + "," + cell.y} data-cell-kind={cell.kind}
            x={cell.x} y={cell.y} width={1} height={1} className={'v2-cell v2-cell-' + cell.kind} />)}
          {walls.map(wall => <rect key={wall.x + "," + wall.y} className="v2-wall-block" fill="url(#v2-wall-grid)"
            x={wall.x + 0.08} y={wall.y + 0.08} width={wall.width - 0.16} height={wall.height - 0.16} rx={0.12} />)}
          <rect className="v2-floor-outline" width={layout.width} height={layout.height} rx={0.12} fill="none" />
          {layout.cells.filter(cell => cell.kind === "parking" || cell.kind === "maintenance_bay").map(cell =>
            <g key={cell.x + "," + cell.y} transform={'translate(' + cell.x + ' ' + cell.y + ')'}>
              <rect className="v2-bay-tile" x={0.13} y={0.13} width={0.74} height={0.74} rx={0.12} />
              {cell.kind === "parking"
                ? <text className="v2-parking-id" x={0.5} y={0.52} textAnchor="middle" dominantBaseline="central">P</text>
                : <polygon className="v2-bay-symbol" points=".5,.25 .75,.5 .5,.75 .25,.5" />}
            </g>)}
          {snapshot.stations.map(station => <ChargerMarker key={station.id} station={station} selection={selection} pick={pick} />)}
          {snapshot.robots.map(robot => <RobotMarker key={robot.id} robot={robot} selection={selection} pick={pick} />)}
          {job && <><JobMarker job={job} point={job.from} label="Pickup" selection={selection} pick={pick} occupied={occupied(job.from)} />
            <JobMarker job={job} point={job.to} label="Drop-off" selection={selection} pick={pick} occupied={occupied(job.to)} /></>}
        </g>
      </svg>
    </div>
    <footer className="v2-map-footer">
      <ul aria-label="Map legend">{mapKeys.map(([icon, label]) => <li key={label}><Icon name={icon} size={14} />{label}</li>)}</ul>
      <span>Seed {snapshot.seed} · Tick {snapshot.tick}</span>
    </footer>
  </section>;
}
