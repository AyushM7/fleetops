import { useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type MouseEvent, type PointerEvent, type ReactNode } from "react";
import { Icon } from "./WarehouseIcon";
import { breakdownLabels, cellLabel, chargerStatus, jobLabels, maintenancePhaseLabel, robotActivityLabel, robotTone,
  type PickEntity, type RobotTone, type Selection } from "./warehousePresentation";
import type { Job, Layout, Point, Robot, Snapshot, Station, WarehouseFrame } from "./telemetry";
import { mapTargetVisible, placeMapLabels, type Camera, type MapLabel } from "./warehouseLabels";
const mapKeys = [["road", "Aisle"], ["wall", "Wall"], ["bolt", "Charger"], ["parking", "Parking"], ["diamond", "Maintenance bay"]] as const;

export function fitWarehouseCamera(layout: Layout, width: number, height: number, inspectorSpace = 0): Camera {
  // Leave room for the desktop inspector without shifting the initial camera on selection.
  const center = inspectorSpace ? width * 0.448 : width / 2;
  const clearWidth = 2 * Math.min(center, width - inspectorSpace - center);
  const scale = Math.max(1, Math.min((clearWidth - 24) / layout.width, (height - 16) / layout.height));
  return { x: center - layout.width * scale / 2, y: (height - layout.height * scale) / 2, scale };
}

export function mapDragIntent(dx: number, dy: number, touch: boolean): "pending" | "pan" | "scroll" {
  const x = Math.abs(dx);
  const y = Math.abs(dy);
  if (Math.max(x, y) < (touch ? 8 : 3)) return "pending";
  return touch && y >= x ? "scroll" : "pan";
}

export function reframeWarehouseCamera(camera: Camera, previousFit: Camera, nextFit: Camera): Camera {
  const ratio = nextFit.scale / previousFit.scale;
  return {
    x: nextFit.x + (camera.x - previousFit.x) * ratio,
    y: nextFit.y + (camera.y - previousFit.y) * ratio,
    scale: camera.scale * ratio,
  };
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

function MapTarget({ target, point, label, selection, pick, children, visible, tone, radius = 0.48 }: {
  target: Selection; point: Point; label: string; selection: Selection | null; pick: PickEntity; children: ReactNode; visible: boolean; tone?: RobotTone; radius?: number;
}) {
  const active = target.kind === selection?.kind && target.id === selection.id;
  return <g className="v2-map-target" transform={'translate(' + (point.x + 0.5) + ' ' + (point.y + 0.5) + ')'}
    role="button" tabIndex={visible ? 0 : -1} aria-label={label} aria-pressed={active} data-tone={tone}
    aria-hidden={visible ? undefined : true}
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

function Label({ id, label, robot }: { id: string; label: MapLabel; robot?: boolean }) {
  const { x, y, width, height, font } = label;
  return <>
    {(x !== 0 || y !== 0) && <line className="v2-label-leader" x1={0} y1={0} x2={x} y2={y} />}
    <rect className={robot ? "v2-robot-marker" : "v2-station-caption"}
      x={x - width / 2} y={y - height / 2} width={width} height={height} rx={Math.min(.14, height / 4)} />
    <text className={robot ? "v2-robot-id" : "v2-station-label"} x={x} y={y} style={{ fontSize: font }}
      textAnchor="middle" dominantBaseline="central">{id}</text>
  </>;
}

type MarkerProps = { selection: Selection | null; pick: PickEntity; label?: MapLabel; visible: boolean };

function ChargerMarker({ station, selection, pick, label, visible }: MarkerProps & { station: Station }) {
  return <MapTarget target={{ kind: "station", id: station.id }} point={station} visible={visible}
    label={station.id + " charging station, " + chargerStatus(station)} selection={selection} pick={pick}
    tone={station.occupiedBy ? "charging" : undefined}>
    <rect className="v2-station-tile" x={-0.4} y={-0.4} width={0.8} height={0.8} rx={0.1} />
    <Icon name="bolt" size={0.49} x={-0.245} y={-0.245} />
    {label && <Label id={station.id} label={label} />}
  </MapTarget>;
}

function RobotMarker({ robot, selection, pick, label, visible, work, highlight }: MarkerProps & {
  robot: Robot; work?: string; highlight?: "target" | "worker" | "recovered";
}) {
  const tone = robotTone(robot);
  const centered = label && label.x === 0 && label.y === 0;
  const maintenance = robot.role === "maintenance";
  return <MapTarget target={{ kind: "robot", id: robot.id }} point={robot} visible={visible} tone={tone}
    label={robot.id + (maintenance ? " maintenance robot, " : ", ") + (work ?? robotActivityLabel(robot)) +
      (robot.activeFault ? ", " + breakdownLabels[robot.activeFault] : "") +
      (work || robot.activeFault ? ", " + cellLabel(robot) : "") + ", battery " + robot.battery + "%"}
    selection={selection} pick={pick}>
    {highlight && <rect className="v2-maintenance-cell" data-highlight={highlight} x={-.45} y={-.45} width={.9} height={.9} rx={.1}
      aria-hidden="true" />}
    {maintenance
      ? <polygon className="v2-robot-marker is-maintenance" points="-.42,0 -.26,-.29 .26,-.29 .42,0 .26,.29 -.26,.29" />
      : !centered && <rect className="v2-robot-marker" x={-0.38} y={-0.29} width={0.76} height={0.58} rx={0.14} />}
    {label && (maintenance && centered
      ? <text className="v2-robot-id" style={{ fontSize: label.font }} textAnchor="middle" dominantBaseline="central">{robot.id}</text>
      : <Label id={robot.id} label={label} robot />)}
    {tone && <g className="v2-status-glyph" data-status-glyph={tone} aria-hidden="true">
      {tone === "waiting" && <circle cx={0.36} cy={-0.31} r={0.085} />}
      {tone === "moving" && <path d="M .24 -.41 h .20 M .30 -.32 h .20" />}
      {tone === "charging" && <path d="M .39 -.45 l -.10 .12 h .11 l -.05 .12" />}
      {tone === "fault" && <path d="M .29 -.4 l .17 .17 m 0 -.17 l -.17 .17" />}
    </g>}
  </MapTarget>;
}

function jobPin(point: Point, occupied: boolean) {
  // Keep markers visible in the same cell when a robot or charger occupies its center.
  return {
    anchor: occupied ? { x: point.x + 0.32, y: point.y - 0.32 } : point,
    size: occupied ? 0.13 : 0.3,
    occupied,
  };
}

function JobMarker({ job, pin, label, selection, pick, isVisible }: {
  job: Job; pin: ReturnType<typeof jobPin>; label: "Pickup" | "Drop-off"; selection: Selection | null;
  pick: PickEntity; isVisible: (point: Point) => boolean;
}) {
  const { anchor, size, occupied } = pin;
  return <MapTarget target={{ kind: "job", id: job.id }} point={anchor} label={label + " for " + job.id} selection={selection} pick={pick}
    radius={occupied ? 0.17 : 0.48} visible={isVisible(anchor)}>
    {label === "Pickup"
      ? <circle className="v2-job-pin" r={size} />
      : <polygon className="v2-job-pin" points={'0,' + -size + ' ' + size + ',0 0,' + size + ' ' + -size + ',0'} />}
    <circle className="v2-job-center" r={occupied ? 0.04 : 0.08} />
  </MapTarget>;
}

function FindEntity({ snapshot, pick }: { snapshot: Snapshot; pick: PickEntity }) {
  const disclosure = useRef<HTMLDetailsElement>(null);
  const summary = useRef<HTMLElement>(null);
  const groups = [
    { label: "Robots", kind: "robot", items: snapshot.robots.map(robot => [robot.id, robotActivityLabel(robot)]) },
    { label: "Chargers", kind: "station", items: snapshot.stations.map(station => [station.id, chargerStatus(station)]) },
    { label: "Jobs", kind: "job", items: snapshot.jobs.map(job => [job.id, jobLabels[job.status]]) },
  ] as const;
  return <details className="v2-find" ref={disclosure} onKeyDown={event => {
    if (event.key !== "Escape" || !disclosure.current?.open) return;
    event.preventDefault();
    disclosure.current.open = false;
    summary.current?.focus();
  }}>
    <summary ref={summary}>Find entity</summary>
    <div className="v2-find-list">
      {groups.map(group => <div key={group.kind} role="group" aria-label={group.label}>
        <p>{group.label}</p>
        {group.items.length ? group.items.map(([id, status]) => <button type="button" key={id}
          onClick={() => {
            if (!disclosure.current || !summary.current) return;
            disclosure.current.open = false;
            pick({ kind: group.kind, id }, summary.current);
          }}><strong>{id}</strong><span>{status}</span></button>) : <p className="v2-secondary">No jobs in this snapshot</p>}
      </div>)}
    </div>
  </details>;
}

export function WarehouseMap({ frame, selection, job, pick }: { frame: WarehouseFrame; selection: Selection | null; job?: Job; pick: PickEntity }) {
  const { layout, snapshot } = frame;
  const walls = useMemo(() => wallBlocks(layout), [layout]);
  const occupied = (point: Point) => [...snapshot.robots, ...snapshot.stations].some(entity => entity.x === point.x && entity.y === point.y);
  const svg = useRef<SVGSVGElement>(null);
  const stage = useRef<HTMLDivElement>(null);
  const drag = useRef<{ id: number; pointer: Point; start: Camera; panning: boolean } | null>(null);
  const suppressDragClick = useRef(false);
  const [viewport, setViewport] = useState({ width: 1250, height: 394, inspectorSpace: 332 });
  const [stageWidth, setStageWidth] = useState(1250);
  const [manualCamera, setCamera] = useState<Camera | null>(null);
  const fit = fitWarehouseCamera(layout, viewport.width, viewport.height, viewport.inspectorSpace);
  const camera = manualCamera ?? fit;
  const minScale = fit.scale / 2;
  const maxScale = fit.scale * 4;
  const bounds = { width: stageWidth, height: viewport.height };
  const isVisible = (point: Point) => mapTargetVisible(point, camera, bounds);
  const pins = job ? [jobPin(job.from, occupied(job.from)), jobPin(job.to, occupied(job.to))] : [];
  const labels = placeMapLabels(snapshot.robots, snapshot.stations, camera, bounds, selection?.id, pins);

  useLayoutEffect(() => {
    const panel = stage.current;
    if (!panel) return;
    function measure() {
      if (panel?.clientWidth) setStageWidth(panel.clientWidth);
    }
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(panel);
    return () => observer.disconnect();
  }, []);

  useLayoutEffect(() => {
    const active = document.activeElement;
    if (stage.current?.contains(active) && active?.getAttribute("aria-hidden") === "true") {
      stage.current.focus({ preventScroll: true });
    }
  });

  useLayoutEffect(() => {
    const canvas = svg.current;
    if (!canvas) return;
    let previous: { width: number; height: number; inspectorSpace: number } | undefined;
    function measure() {
      if (!canvas?.clientWidth || !canvas.clientHeight) return;
      const inspectorSpace = Number.parseFloat(getComputedStyle(canvas).getPropertyValue("--v2-inspector-space")) || 0;
      const measured = { width: canvas.clientWidth, height: canvas.clientHeight, inspectorSpace };
      if (previous?.width === measured.width && previous.height === measured.height && previous.inspectorSpace === inspectorSpace) return;
      if (previous) {
        const previousFit = fitWarehouseCamera(layout, previous.width, previous.height, previous.inspectorSpace);
        const nextFit = fitWarehouseCamera(layout, measured.width, measured.height, measured.inspectorSpace);
        setCamera(current => current && reframeWarehouseCamera(current, previousFit, nextFit));
      }
      previous = measured;
      setViewport(measured);
    }
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(canvas);
    return () => observer.disconnect();
  }, [layout]);

  function zoom(factor: number): void {
    const cx = (stage.current?.clientWidth ?? viewport.width) / 2;
    const cy = viewport.height / 2;
    setCamera(current => {
      const from = current ?? fit;
      const scale = Math.min(maxScale, Math.max(minScale, from.scale * factor));
      const ratio = scale / from.scale;
      return { x: cx - (cx - from.x) * ratio, y: cy - (cy - from.y) * ratio, scale };
    });
  }

  function pan(event: KeyboardEvent<HTMLDivElement>): void {
    if (event.target !== event.currentTarget) return;
    const offsets: Record<string, Point> = { ArrowLeft: { x: 36, y: 0 }, ArrowRight: { x: -36, y: 0 },
      ArrowUp: { x: 0, y: 36 }, ArrowDown: { x: 0, y: -36 } };
    const offset = offsets[event.key];
    if (!offset) return;
    event.preventDefault();
    setCamera(current => {
      const from = current ?? fit;
      return { ...from, x: from.x + offset.x, y: from.y + offset.y };
    });
  }

  function beginDrag(event: PointerEvent<HTMLDivElement>): void {
    if (!event.isPrimary) {
      // A second touch belongs to the browser's pinch gesture.
      if (event.pointerType === "touch" && drag.current) finishDrag(event.currentTarget, drag.current.id);
      return;
    }
    if (event.button !== 0) return;
    suppressDragClick.current = false;
    drag.current = { id: event.pointerId, pointer: { x: event.clientX, y: event.clientY }, start: camera, panning: false };
  }

  function moveDrag(event: PointerEvent<HTMLDivElement>): void {
    const active = drag.current;
    if (!active || active.id !== event.pointerId) return;
    if (!event.buttons) {
      finishDrag(event.currentTarget, event.pointerId);
      return;
    }
    const dx = event.clientX - active.pointer.x;
    const dy = event.clientY - active.pointer.y;
    if (!active.panning) {
      const intent = mapDragIntent(dx, dy, event.pointerType === "touch");
      if (intent === "pending") return;
      if (intent === "scroll") {
        drag.current = null;
        return;
      }
      active.panning = true;
      suppressDragClick.current = true;
      event.currentTarget.setPointerCapture(event.pointerId);
      event.currentTarget.dataset.dragging = "";
    }
    setCamera({ ...active.start, x: active.start.x + dx, y: active.start.y + dy });
  }

  function finishDrag(element: HTMLDivElement, id: number): void {
    if (drag.current?.id !== id) return;
    drag.current = null;
    delete element.dataset.dragging;
  }

  function preventDragClick(event: MouseEvent<HTMLDivElement>): void {
    if (!suppressDragClick.current || event.detail === 0) return;
    suppressDragClick.current = false;
    event.stopPropagation();
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
    <div className="v2-map-stage" ref={stage} tabIndex={0} role="group" aria-label="Warehouse map. Use arrow keys to pan or Find entity for any robot, charger or job."
      onKeyDown={pan} onClickCapture={preventDragClick} onPointerDown={beginDrag} onPointerMove={moveDrag}
      onPointerUp={event => finishDrag(event.currentTarget, event.pointerId)} onPointerCancel={event => finishDrag(event.currentTarget, event.pointerId)}
      onLostPointerCapture={event => {
        // A marker can lose implicit touch capture when the stage claims the drag.
        if (event.target === event.currentTarget) finishDrag(event.currentTarget, event.pointerId);
      }}>
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
          {snapshot.stations.map(station => <ChargerMarker key={station.id} station={station} selection={selection} pick={pick}
            label={labels.get(station.id)} visible={isVisible(station)} />)}
          {snapshot.robots.map(robot => {
            const active = snapshot.maintenance?.active;
            const last = snapshot.maintenance?.history?.at(-1);
            const isM1 = robot.role === "maintenance";
            const target = active?.robotId === robot.id;
            const recovered = !robot.activeFault && snapshot.maintenance?.phase === "returning" &&
              last?.robotId === robot.id && last.outcome === "completed";
            return <RobotMarker key={robot.id} robot={robot} selection={selection} pick={pick}
              label={labels.get(robot.id)} visible={isVisible(robot)}
              work={isM1 ? active ? maintenancePhaseLabel(snapshot) + ", target " + active.robotId
                : snapshot.maintenance?.phase === "returning" ? maintenancePhaseLabel(snapshot) : undefined :
                target ? robotActivityLabel(robot) + ", M1 assigned" : recovered ? "Recovery verified, " + robotActivityLabel(robot) : undefined}
              highlight={active && isM1 ? "worker" : target ? "target" : recovered ? "recovered" : undefined} />;
          })}
          {job && <><JobMarker job={job} pin={pins[0]} label="Pickup" selection={selection} pick={pick} isVisible={isVisible} />
            <JobMarker job={job} pin={pins[1]} label="Drop-off" selection={selection} pick={pick} isVisible={isVisible} /></>}
        </g>
      </svg>
    </div>
    <footer className="v2-map-footer">
      <ul aria-label="Map legend">{mapKeys.map(([icon, label]) => <li key={label}><Icon name={icon} size={14} />{label}</li>)}</ul>
      <div className="v2-map-meta">
        <span>Seed {snapshot.seed} · Tick {snapshot.tick}</span>
        <FindEntity snapshot={snapshot} pick={pick} />
      </div>
    </footer>
  </section>;
}
