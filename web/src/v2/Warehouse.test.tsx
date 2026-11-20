import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import V2Shell from "./V2Shell";
import { Warehouse } from "./Warehouse";
import { fitWarehouseCamera } from "./WarehouseMap";
import { mapTargetVisible, placeMapLabels } from "./warehouseLabels";
import { robotActivityLabel, robotTone, warehouseCounts, type Selection } from "./warehousePresentation";
import type { Job, Layout, Robot, Station, WarehouseFrame } from "./telemetry";

const stations: Station[] = [1, 6, 12, 18].map((x, index) => ({
  id: "C" + (index + 1), x, y: 0, occupiedBy: index === 0 ? "R1" : "", reservedBy: index === 1 ? "R2" : "",
}));
const layout: Layout = {
  width: 20, height: 13,
  cells: Array.from({ length: 260 }, (_, index) => {
    const x = index % 20;
    const y = Math.floor(index / 20);
    if (y === 0 && x === 9) return { x, y, kind: "maintenance_bay" };
    if (y === 0 && stations.some(station => station.x === x)) return { x, y, kind: "station" };
    if (y === 12) return { x, y, kind: x % 2 === 0 ? "parking" : "wall" };
    return { x, y, kind: y > 1 && y < 10 ? x === 4 ? "aisle" : "wall" : "free" };
  }),
};
const job: Job = {
  id: "J18", status: "in_progress", type: "PRIORITY", assignedRobot: "R8",
  from: { x: 4, y: 3 }, to: { x: 18, y: 1 }, deadlineTick: 90, deadlineMissed: true,
};
const robots: Robot[] = Array.from({ length: 11 }, (_, index) => ({
  id: "R" + (index + 1),
  x: index === 10 ? 9 : index === 0 ? 1 : index === 7 ? 4 : index * 2,
  y: index === 10 || index === 0 ? 0 : index === 7 ? 5 : 12,
  status: index === 0 ? "charging" : index === 7 ? "moving" : "idle",
  activity: index === 0 ? "CHARGING" : index === 1 ? "GOING_TO_CHARGE" : index === 7 ? "DELIVERING" : "PARKED",
  role: index === 10 ? "maintenance" : "transport", battery: index === 7 ? 21 : 89, blocked: false, waitTicks: 0,
  jobId: index === 7 ? job.id : "",
}));
const frame: WarehouseFrame = {
  type: "snapshot", generation: "a".repeat(64), eventId: 1, runtimeId: "P" + "1".repeat(32),
  mode: "deterministic", state: "running", operations: [], layout,
  snapshot: { tick: 162, seed: 7, completedJobs: 18, priorityDeadlinesMissed: 45, robots, stations, faults: [], aisles: [],
    jobs: [job, { ...job, id: "J5", status: "done" }, { ...job, id: "J6", type: "NORMAL" }] },
};

function render(selection: Selection | null = null, current: WarehouseFrame | null = frame) {
  return renderToStaticMarkup(<Warehouse frame={current} selection={selection} selectEntity={vi.fn()} viewFleet={vi.fn()} hidden={false} />);
}

describe("Warehouse V2", () => {
  it("counts confirmed completions, unfinished missed priority jobs, and only charging robots", () => {
    expect(warehouseCounts(frame.snapshot)).toEqual({ completed: 18, overdue: 1, charging: 1 });
    expect(warehouseCounts({ ...frame.snapshot, completedJobs: undefined, jobs: [], robots: [] }))
      .toEqual({ completed: 0, overdue: 0, charging: 0 });
    const output = render();
    for (const label of ["Completed deliveries", "Priority overdue", "Charging robots", "Warehouse map"]) expect(output).toContain(label);
    expect(output).toContain("<dd>18</dd>");
    expect(output).toMatch(/v2-metric" data-tone="fault".*?Priority overdue<\/dt><dd>1<\/dd>/);
    expect(output).toMatch(/v2-metric" data-tone="charging".*?Charging robots<\/dt><dd>1<\/dd>/);
    expect(output).not.toContain("Robot details");
  });

  it("renders the full supplied layout, 11 robots, four chargers, and the fleet strip", () => {
    const output = render();
    expect(output.match(/data-cell-kind=/g)).toHaveLength(260);
    expect(output.match(/data-cell-kind="parking"/g)).toHaveLength(10);
    expect(output).toContain('data-cell-kind="maintenance_bay"');
    expect(output).toContain('data-cell-kind="aisle"');
    expect(output).toContain('data-cell-kind="wall"');
    expect(output.match(/class="v2-wall-block"/g)).toHaveLength(12);
    expect(output).toMatch(/class="v2-wall-block"[^>]*height="7.84"/);
    expect(output.match(/aria-label="C\d charging station/g)).toHaveLength(4);
    expect(output.match(/aria-label="Inspect R\d/g)).toHaveLength(11);
    expect(output).toContain('aria-label="R11 maintenance robot, Parked, battery 89%"');
    expect(output).not.toContain("Pickup for");
  });

  it("links robot selection to its fleet card and real job markers without altering the default camera", () => {
    const overview = render();
    const selected = render({ kind: "robot", id: "R8" });
    expect(selected.match(/data-testid="warehouse-floor" transform="[^"]+"/)?.[0])
      .toBe(overview.match(/data-testid="warehouse-floor" transform="[^"]+"/)?.[0]);
    expect(selected).toContain("Robot details");
    expect(selected).toMatch(/aria-label="R8, Delivering, battery 21%" aria-pressed="true"/);
    expect(selected).toMatch(/aria-pressed="true" data-tone="moving" aria-label="Inspect R8"/);
    expect(selected).toContain('aria-label="Pickup for J18"');
    expect(selected).toContain('aria-label="Drop-off for J18"');
    expect(selected).toContain("View job");
    expect(selected).toContain('class="v2-status-chip" data-tone="moving"');
    expect(selected).toContain('<dialog class="v2-panel v2-inspector" aria-labelledby="v2-inspector-title">');
  });

  it("keeps a job marker visible within its real cell when a robot occupies the center", () => {
    const overlap = { ...frame, snapshot: { ...frame.snapshot, jobs: [{ ...job, from: { x: 4, y: 5 } }] } };
    const output = render({ kind: "robot", id: "R8" }, overlap);
    expect(output).toMatch(/transform="translate\(4\.82 5\.18\)" role="button" tabindex="0" aria-label="Pickup for J18"/);
    expect(output).toMatch(/aria-label="R8, Delivering, battery 21%" aria-pressed="true"/);
  });

  it("shows job and charger context from the actual selected records", () => {
    const jobDetails = render({ kind: "job", id: "J18" });
    expect(jobDetails).toContain("Job details");
    expect(jobDetails).toContain("Tick 90 · Overdue");
    expect(jobDetails).toContain("Cell 4, 3");
    expect(jobDetails).toContain("Cell 18, 1");
    const charger = render({ kind: "station", id: "C2" });
    expect(charger).toContain("Charger details");
    expect(charger).toContain("Reserved for");
    expect(charger).not.toContain("C2 · Reserved");
    expect(charger).toContain("C2 charging station, Reserved");
    expect(charger).toContain("Inspect R2");
    expect(render({ kind: "job", id: "missing" })).not.toContain("Job details");
  });

  it("fits the real aspect ratio inside the inspector-safe viewport and explicitly centers Fit", () => {
    const initial = fitWarehouseCamera(layout, 1250, 394, 332);
    expect(initial.y).toBe(8);
    expect(initial.x + initial.scale * layout.width).toBeLessThan(918);
    const explicit = fitWarehouseCamera(layout, 918, 394);
    expect(explicit.x + explicit.scale * layout.width / 2).toBe(459);
    const mobile = fitWarehouseCamera(layout, 366, 260);
    expect(mobile.x).toBe(12);
    expect(mobile.x + mobile.scale * layout.width).toBe(354);
  });

  it("maps confirmed activity with fault and traffic taking precedence over moving", () => {
    expect(robotTone(robots[0])).toBe("charging");
    expect(robotTone(robots[7])).toBe("moving");
    expect(robotTone(robots[2])).toBeUndefined();
    const waiting = { ...robots[7], blocked: true };
    const fault = { ...waiting, status: "offline" } as const;
    expect(robotTone(waiting)).toBe("waiting");
    expect(robotActivityLabel(waiting)).toBe("Traffic wait");
    expect(robotTone(fault)).toBe("fault");
    expect(robotActivityLabel(fault)).toBe("Offline");
  });

  it("keeps phone-fit labels readable without overlapping a charger caption", () => {
    const bounds = { width: 365, height: 230 };
    const camera = fitWarehouseCamera(layout, bounds.width, bounds.height);
    const crowded = [ { ...robots[7], x: 1, y: 1 }, { ...robots[10], x: 2, y: 1 } ];
    const labels = placeMapLabels(crowded, [stations[0]], camera, bounds);
    const charger = labels.get("C1");
    const robot = labels.get("R8");
    expect(labels.size).toBe(3);
    expect(robot!.font * camera.scale).toBe(11);
    expect(charger!.font * camera.scale).toBe(11);
    expect(charger!.y + charger!.height / 2).toBeLessThan(1 + robot!.y - robot!.height / 2);
    expect(mapTargetVisible(crowded[0], { ...camera, x: -100 }, bounds)).toBe(false);
  });

  it("provides a map-independent finder for every kind of entity", () => {
    const output = render();
    expect(output).toContain("<summary>Find entity</summary>");
    expect(output).toContain('aria-label="Robots"');
    expect(output).toContain('aria-label="Chargers"');
    expect(output).toContain('aria-label="Jobs"');
    expect(output).toContain("<strong>J18</strong><span>In progress</span>");
  });

  it("keeps zoom and Fit named while waiting states show no fabricated totals or active simulation buttons", () => {
    for (const label of ["Zoom in", "Zoom out", "Fit warehouse map"]) expect(render()).toContain('aria-label="' + label + '"');
    const loading = render(null, null);
    expect(loading.match(/<dd>—<\/dd>/g)).toHaveLength(3);
    expect(loading).toContain("Waiting for confirmed warehouse telemetry");
    const shell = renderToStaticMarkup(<V2Shell onLegacy={vi.fn()} />);
    expect(shell).toContain("Waiting for Go");
    expect(shell).toContain('aria-describedby="v2-control-note"');
    expect(shell.match(/<button type="button" aria-disabled="true">/g)).toHaveLength(3);
    expect(shell).not.toContain("Controls pending");
  });
});
