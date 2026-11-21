import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { EntityDirectory } from "./EntityDirectory";
import type { WarehouseFeed } from "./session";
import type { Job, Robot, WarehouseFrame } from "./telemetry";
import type { Selection } from "./warehousePresentation";

const available: Robot = {
  id: "R1", role: "transport", status: "idle", activity: "AVAILABLE", battery: 90, x: 3, y: 4,
  jobId: "", blocked: false, waitTicks: 0,
};
const robots: Robot[] = Array.from({ length: 10 }, (_, index) => ({ ...available, id: "R" + (index + 1) }));
robots[1] = { ...robots[1], activity: "DELIVERING", status: "moving", jobId: "J3", battery: 54 };
robots[2] = { ...robots[2], activity: "CHARGING", status: "charging" };
robots[3] = { ...robots[3], activity: "DELIVERING", status: "moving", blocked: true, waitTicks: 2 };
robots[4] = { ...robots[4], status: "faulted", activity: "FAULTED" };
robots.push({ ...available, id: "M1", activity: "PARKED", role: "maintenance" });
const pending: Job = {
  id: "J1", type: "NORMAL", status: "pending", assignedRobot: "", from: { x: 4, y: 5 }, to: { x: 8, y: 2 },
};
const jobs: Job[] = [
  pending,
  { ...pending, id: "J2", status: "assigned", assignedRobot: "R1" },
  { ...pending, id: "J3", status: "in_progress", type: "PRIORITY", assignedRobot: "R2", deadlineTick: 12, deadlineMissed: true },
  { ...pending, id: "J4", status: "done", type: "PRIORITY", assignedRobot: "R1", deadlineTick: 13,
    deadlineMissed: true, completedTick: 17, outcome: "LATE" },
];
const frame: WarehouseFrame = {
  type: "snapshot", generation: "a".repeat(64), runtimeId: "P" + "1".repeat(32), eventId: 1,
  mode: "deterministic", state: "paused", layout: { width: 20, height: 13, cells: [] }, operations: [],
  snapshot: { robots, jobs, tick: 20, seed: 1, stations: [], faults: [], aisles: [], completedJobs: 140 },
};
const live: WarehouseFeed = { phase: "live", frame, status: null, pending: null, controlWarning: null };

function render(page: "Fleet" | "Jobs", selection: Selection | null = null, feed = live): string {
  return renderToStaticMarkup(<EntityDirectory page={page} selection={selection} feed={feed}
    selectEntity={vi.fn()} viewWarehouse={vi.fn()} inspectRelated={vi.fn()} />);
}

describe("Fleet", () => {
  it("shows every robot with its actual role and activity, with operational tints", () => {
    const html = render("Fleet");
    expect(html.match(/class="v2-robot-card v2-fleet-tile"/g)).toHaveLength(11);
    expect(html.match(/class="v2-card-charge"[^>]*><svg aria-hidden="true"[^>]*>.*?<\/svg>\d+%<\/span>/g)).toHaveLength(11);
    expect(html).toContain('aria-label="Battery charge 54%, healthy"');
    for (const text of ["M1", "Maintenance robot", "Parked", "Available", "Charging", "Traffic wait", "Delivering", "Faulted",
      "Job J3", "Cell 3, 4", "54%"]) expect(html).toContain(text);
    for (const tone of ["moving", "charging", "waiting", "fault"]) expect(html).toContain('data-tone="' + tone + '"');
  });

  it("reflects shared robot selection and shows its confirmed operational details", () => {
    const html = render("Fleet", { kind: "robot", id: "R4" });
    expect(html.match(/aria-pressed="true"/g)).toHaveLength(1);
    expect(html).toContain("Robot details");
    expect(html).toMatch(/<dt>Battery<\/dt><dd><span[^>]+aria-label="Battery charge 90%, healthy"/);
    expect(html).toContain("<dt>Position</dt><dd>Cell 3, 4</dd>");
    expect(html).toContain("<dt>Traffic wait</dt><dd>2 ticks</dd>");
    expect(html).toContain("View on Warehouse map");
    expect(render("Fleet", { kind: "job", id: "J3" })).not.toContain("Job details");
  });
});

describe("Jobs", () => {
  it("shows the lifecycle, deadlines and outcome, with cumulative deliveries separate from retained rows", () => {
    const html = render("Jobs");
    expect(html).toContain("140 completed deliveries");
    const firstDelivery = { ...live, frame: { ...frame, snapshot: { ...frame.snapshot, completedJobs: 1 } } };
    expect(render("Jobs", null, firstDelivery)).toContain("1 completed delivery");
    expect(html).toContain("Retained completions <span>1</span>");
    expect(html.match(/class="v2-job-row"/g)).toHaveLength(4);
    for (const label of ["Pending", "Assigned", "In progress", "Unassigned", "Overdue", "Late", "Cell 4, 5", "Cell 8, 2",
      "Deadline tick 12", "Job status"]) expect(html).toContain(label);
    expect(html.match(/class="v2-overdue"/g)).toHaveLength(1);
  });

  it("inspects a retained job without interpreting a late completion as currently overdue", () => {
    const html = render("Jobs", { kind: "job", id: "J4" });
    expect(html).toContain("Job details");
    expect(html).toContain("<dt>Outcome</dt><dd>Late</dd>");
    expect(html).toContain("<dt>Completed at</dt><dd>Tick 17</dd>");
    expect(html).toContain("Tick 13</span>");
    expect(html).toContain("Inspect R1");
    expect(html).toContain("View on Warehouse map");
    const missingRobot = { ...live, frame: { ...frame, snapshot: { ...frame.snapshot, robots: [] } } };
    expect(render("Jobs", { kind: "job", id: "J4" }, missingRobot)).not.toContain("Inspect R1");
  });

  it("shows empty queues and drops details for a job outside the current snapshot", () => {
    const empty = { ...live, frame: { ...frame, snapshot: { ...frame.snapshot, jobs: [] } } };
    const html = render("Jobs", { kind: "job", id: "J4" }, empty);
    expect(html).toContain("No open jobs in this snapshot.");
    expect(html).toContain("No completed jobs are retained in this snapshot.");
    expect(html).not.toContain("Job details");
    expect(html).not.toContain("View on Warehouse map");
  });
});

describe.each(["Fleet", "Jobs"] as const)("%s freshness", page => {
  it("labels retained data even inside the inspector and waits for a replacement snapshot", () => {
    const selection: Selection = page === "Fleet" ? { kind: "robot", id: "R2" } : { kind: "job", id: "J3" };
    const stale = render(page, selection, { ...live, phase: "reconnecting" });
    expect(stale).toContain("Showing last confirmed snapshot at tick 20.");
    expect(stale).toContain("Last confirmed at tick 20.");
    expect(render(page, selection)).not.toContain("Last confirmed at tick");
    const replacement = render(page, selection, { ...live, phase: "reconnecting", frame: null, pending: "restart" });
    expect(replacement).toContain("Waiting for confirmed");
    expect(replacement).not.toContain("View on Warehouse map");
  });
});
