import { authentication } from "./demo";

export type RobotStatus = "idle" | "moving" | "charging" | "faulted" | "offline";
export type CellKind = "free" | "wall" | "aisle" | "station";
export interface Robot { id: string; x: number; y: number; battery: number; status: RobotStatus; jobId: string; waitTicks: number; blocked: boolean }
export interface Station { id: string; x: number; y: number; occupiedBy: string; reservedBy: string }
export interface Layout { width: number; height: number; cells: { x: number; y: number; kind: CellKind }[] }
export interface Incident { robotId: string; type: "low_battery" | "robot_unresponsive" | "traffic_blocked"; severity: "medium" | "high"; telemetry: Robot }
export interface Job { id: string; status: "pending" | "assigned" | "in_progress" | "done"; assignedRobot: string; fromX: number; fromY: number; toX: number; toY: number }
export interface FleetSnapshot { tick: number; poisoned: boolean; robots: Robot[]; stations: Station[]; layout: Layout; activeIncidents: Incident[]; jobs: Job[] }
export type Feed = { phase: "loading" | "live" | "unavailable"; snapshot: FleetSnapshot | null; updatedAt: Date | null };
export const fleetQuery = `{ fleetState { tick poisoned layout { width height cells { x y kind } } robots { id x y battery status jobId waitTicks blocked } stations { id x y occupiedBy reservedBy } activeIncidents { robotId type severity telemetry { id x y battery status jobId waitTicks blocked } } jobs { id status assignedRobot fromX fromY toX toY } } }`;

export class FleetError extends Error {
  constructor(public readonly kind: "authentication" | "unavailable") { super(kind); }
}
const record = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null;
const integer = (v: unknown): v is number => typeof v === "number" && Number.isSafeInteger(v);
const text = (v: unknown): v is string => typeof v === "string";
const statuses: readonly string[] = ["idle", "moving", "charging", "faulted", "offline"];
const kinds: readonly string[] = ["free", "wall", "aisle", "station"];

export function parseSnapshot(value: unknown): FleetSnapshot {
  const fail = (): never => { throw new FleetError("unavailable"); };
  if (!record(value) || !integer(value.tick) || value.tick < 0 || typeof value.poisoned !== "boolean" || !record(value.layout)) return fail();
  const { width, height, cells } = value.layout;
  if (!integer(width) || !integer(height) || width < 1 || height < 1 || width > 50 || height > 50 || !Array.isArray(cells) || cells.length !== width * height) return fail();
  const point = (v: Record<string, unknown>) => integer(v.x) && integer(v.y) && v.x >= 0 && v.x < width && v.y >= 0 && v.y < height;
  const positions = new Set<string>();
  for (const c of cells) {
    if (!record(c) || !point(c) || !text(c.kind) || !kinds.includes(c.kind)) return fail();
    positions.add(`${c.x},${c.y}`);
  }
  if (positions.size !== cells.length || !Array.isArray(value.robots) || !Array.isArray(value.stations)) return fail();
  for (const r of value.robots) {
    if (!record(r) || !point(r) || !text(r.id) || !text(r.status) || !statuses.includes(r.status) || !integer(r.battery) || r.battery < 0 || r.battery > 100 || !text(r.jobId) || !integer(r.waitTicks) || r.waitTicks < 0 || typeof r.blocked !== "boolean") return fail();
  }
  for (const s of value.stations) {
    if (!record(s) || !point(s) || !text(s.id) || !text(s.occupiedBy) || !text(s.reservedBy)) return fail();
  }
  validateOperationalData(value, width, height);
  return value as unknown as FleetSnapshot;
}

function validateOperationalData(value: Record<string, unknown>, width: number, height: number): void {
  const fail = (): never => { throw new FleetError("unavailable"); };
  const robots = value.robots as Robot[];
  const stations = value.stations as Station[];
  if (new Set(robots.map(r => r.id)).size !== robots.length || new Set(stations.map(s => s.id)).size !== stations.length ||
      !Array.isArray(value.activeIncidents) || !Array.isArray(value.jobs)) return fail();
  const references = new Set<string>();
  for (const incident of value.activeIncidents) {
    if (!record(incident) || !["low_battery", "robot_unresponsive", "traffic_blocked"].includes(incident.type as string) ||
        !["medium", "high"].includes(incident.severity as string) || !record(incident.telemetry)) return fail();
    const robot = robots.find(r => r.id === incident.robotId);
    const key = `${incident.robotId}:${incident.type}`;
    if (!robot || references.has(key) || Object.keys(robot).some(field => robot[field as keyof Robot] !== (incident.telemetry as Record<string, unknown>)[field])) return fail();
    references.add(key);
  }
  const jobIDs = new Set<string>();
  for (const job of value.jobs) {
    if (!record(job) || !text(job.id) || !job.id || jobIDs.has(job.id) || !text(job.assignedRobot) ||
        !["pending", "assigned", "in_progress", "done"].includes(job.status as string)) return fail();
    for (const [key, limit] of [["fromX", width], ["toX", width], ["fromY", height], ["toY", height]] as const) {
      if (!integer(job[key]) || job[key] < 0 || job[key] >= limit) return fail();
    }
    jobIDs.add(job.id);
  }
}

export async function fetchFleet(token: string, signal: AbortSignal, request: typeof fetch = fetch): Promise<FleetSnapshot> {
  const response = await request("/graphql", {
    method: "POST", ...authentication(token), cache: "no-store", redirect: "error", signal,
    body: JSON.stringify({ query: fleetQuery }),
  });
  if (response.status === 401 || response.status === 403) throw new FleetError("authentication");
  if (!response.ok) throw new FleetError("unavailable");
  const body: unknown = await response.json();
  if (!record(body) || body.errors || !record(body.data)) throw new FleetError("unavailable");
  return parseSnapshot(body.data.fleetState);
}

// Schedule only after completion, so slow requests cannot overlap or replace newer data.
export function pollFleet(token: string, emit: (feed: Feed) => void, unauthorized: () => void): () => void {
  let stopped = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let controller: AbortController | undefined;
  let snapshot: FleetSnapshot | null = null;
  let updatedAt: Date | null = null;
  const poll = async () => {
    controller = new AbortController();
    const timeout = setTimeout(() => controller?.abort(), 8000);
    try {
      const next = await fetchFleet(token, controller.signal);
      if (stopped) return;
      snapshot = next; updatedAt = new Date();
      emit({ phase: "live", snapshot, updatedAt });
    } catch (error) {
      if (stopped) return;
      if (error instanceof FleetError && error.kind === "authentication") { stopped = true; unauthorized(); return; }
      emit({ phase: "unavailable", snapshot, updatedAt });
    } finally { clearTimeout(timeout); }
    if (!stopped) timer = setTimeout(() => { void poll(); }, 1000);
  };
  emit({ phase: "loading", snapshot, updatedAt });
  void poll();
  return () => { stopped = true; clearTimeout(timer); controller?.abort(); };
}
