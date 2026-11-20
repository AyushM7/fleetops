export const warehouseScenario = "warehouse-v2";

const cellKinds = ["free", "wall", "aisle", "station", "parking", "maintenance_bay"] as const;
const robotStates = ["idle", "moving", "charging", "faulted", "offline"] as const;
const activities = [
  "AVAILABLE", "GOING_TO_PICKUP", "PICKING_UP", "DELIVERING", "WAITING_FOR_TRAFFIC",
  "GOING_TO_CHARGE", "CHARGING", "UNDOCKING", "GOING_TO_PARK", "RELOCATING",
  "PARKED", "FAULTED", "OUT_OF_SERVICE",
] as const;
const jobStates = ["pending", "assigned", "in_progress", "done"] as const;
const jobTypes = ["NORMAL", "PRIORITY"] as const;
const outcomes = ["DELIVERED", "ON_TIME", "LATE"] as const;
const modes = ["deterministic", "checking_assistance", "ai_assisted"] as const;
const runStates = ["paused", "running", "stopped"] as const;
export const warehouseEvents = ["snapshot", "tick", "state", "operations"] as const;

export type Point = { x: number; y: number };
type Cell = Point & { kind: typeof cellKinds[number] };
export type Layout = { width: number; height: number; cells: Cell[] };
export type Robot = Point & {
  id: string; status: typeof robotStates[number]; activity: typeof activities[number];
  role: "transport" | "maintenance"; battery: number; jobId: string; blocked: boolean; waitTicks: number;
};
export type Job = {
  id: string; status: typeof jobStates[number]; type: typeof jobTypes[number]; assignedRobot: string;
  from: Point; to: Point; deadlineMissed?: boolean; deadlineTick?: number; createdTick?: number;
  assignedTick?: number; queueAgeTicks?: number; cargoRobotId?: string;
  completedTick?: number; outcome?: typeof outcomes[number];
};
export type Station = Point & { id: string; occupiedBy: string; reservedBy: string };
type Fault = { id: string; type: string; robots: string[]; tick: number; aisle?: number };
type Aisle = { id: number; occupiedBy: string };
type Deadlock = { robots: string[]; yieldingRobot?: string };
export type Snapshot = {
  tick: number; seed: number; completedJobs?: number; priorityDeadlinesMet?: number;
  priorityDeadlinesMissed?: number; robots: Robot[]; jobs: Job[]; stations: Station[];
  faults: Fault[]; aisles: Aisle[]; trafficDeadlocks?: Deadlock[];
};
type Assignment = { jobId: string; robotId: string };
type Decision =
  | { kind: "assign"; assignments: Assignment[] }
  | { kind: "reassign"; reassignment: { JobID: string; FromRobotID: string; ToRobotID: string } }
  | { kind: "abstain"; reason: string };
type Usage = { model: string; inputTokens: number; outputTokens: number };
type OperationsActivity = {
  observationId: string; observedAt: string; tick: number; stage: "observed" | "proposed" | "finished";
  status: string; recorded: boolean; proposedAt?: string; finishedAt?: string;
  modelStatus?: string; model?: string; requestId?: number; reservedMicroUsd?: number; accountedMicroUsd?: number;
  decision?: Decision; usage?: Usage; goReason?: string;
  go?: { auditId: number; priorAuditId?: number; execution: "none" | "succeeded" | "failed" | "unknown"; auditCommitted: boolean };
};
type WarehouseData = {
  state: typeof runStates[number]; mode: typeof modes[number]; snapshot: Snapshot; operations: OperationsActivity[];
};
export type WarehouseStatus = { generation: string; ready: false } |
  ({ generation: string; runtimeId: string; ready: true; layout: Layout } & WarehouseData);
export type WarehouseFrame = {
  type: typeof warehouseEvents[number]; runtimeId: string; generation: string; eventId: number; layout: Layout;
} & WarehouseData;

const record = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v);
const text = (v: unknown): v is string => typeof v === "string";
const id = (v: unknown): v is string => text(v) && v.length > 0;
const count = (v: unknown): v is number => typeof v === "number" && Number.isSafeInteger(v) && v >= 0;
const optionalCount = (v: unknown): boolean => v === undefined || count(v);
const optionalText = (v: unknown): boolean => v === undefined || text(v);
const member = <T extends string>(v: unknown, values: readonly T[]): v is T => text(v) && values.some(value => value === v);
const generationId = (v: unknown): v is string => text(v) && /^[a-f0-9]{64}$/.test(v);
const runtimeId = (v: unknown): v is string => text(v) && /^P[a-f0-9]{32}$/.test(v);
const timestamp = (v: unknown): v is string => text(v) && Number.isFinite(Date.parse(v));
const point = (v: unknown): v is Point & Record<string, unknown> => record(v) && count(v.x) && v.x < 20 && count(v.y) && v.y < 13;
const cellKey = (v: Point): number => v.y * 20 + v.x;
const cell = (v: unknown): v is Cell => record(v) && point(v) && member(v.kind, cellKinds);
const invalid = (): never => { throw new Error("Invalid Warehouse V2 telemetry."); };

export function isWarehouseStatus(value: unknown): boolean {
  return record(value) && value.scenarioId === warehouseScenario && generationId(value.generation) && typeof value.ready === "boolean";
}

export function parseVisitorIdentity(value: unknown): { generation: string; scenarioId: string; ready: boolean } {
  if (!record(value) || !generationId(value.generation) || !id(value.scenarioId) || typeof value.ready !== "boolean") return invalid();
  return { generation: value.generation, scenarioId: value.scenarioId, ready: value.ready };
}

function parseLayout(value: unknown): Layout {
  if (!record(value) || value.width !== 20 || value.height !== 13 ||
      !Array.isArray(value.cells) || value.cells.length !== 260 || !value.cells.every(cell) ||
      new Set(value.cells.map(cellKey)).size !== 260) return invalid();
  return { width: value.width, height: value.height, cells: value.cells };
}

function isRobot(v: unknown): v is Robot {
  return record(v) && point(v) && id(v.id) && member(v.status, robotStates) && member(v.activity, activities) &&
    member(v.role, ["transport", "maintenance"]) && count(v.battery) && v.battery <= 100 &&
    text(v.jobId) && typeof v.blocked === "boolean" && count(v.waitTicks);
}

function isJob(v: unknown): v is Job {
  return record(v) && id(v.id) && member(v.status, jobStates) && member(v.type, jobTypes) &&
    text(v.assignedRobot) && point(v.from) && point(v.to) && optionalText(v.cargoRobotId) &&
    (v.deadlineMissed === undefined || typeof v.deadlineMissed === "boolean") &&
    [v.deadlineTick, v.createdTick, v.assignedTick, v.queueAgeTicks, v.completedTick].every(optionalCount) &&
    (v.outcome === undefined || member(v.outcome, outcomes));
}

function isStation(v: unknown): v is Station {
  return record(v) && point(v) && id(v.id) && text(v.occupiedBy) && text(v.reservedBy);
}

function isFault(v: unknown): v is Fault {
  return record(v) && id(v.id) && id(v.type) && count(v.tick) && optionalCount(v.aisle) &&
    Array.isArray(v.robots) && v.robots.every(id);
}

function isAisle(v: unknown): v is Aisle {
  return record(v) && count(v.id) && text(v.occupiedBy);
}

function isDeadlock(v: unknown): v is Deadlock {
  return record(v) && Array.isArray(v.robots) && v.robots.every(id) && optionalText(v.yieldingRobot);
}

function uniqueIds(values: { id: string }[]): boolean {
  return new Set(values.map(value => value.id)).size === values.length;
}

function parseSnapshot(v: unknown, layout: Layout): Snapshot {
  if (!record(v) || !count(v.tick) || !count(v.seed) ||
      ![v.completedJobs, v.priorityDeadlinesMet, v.priorityDeadlinesMissed].every(optionalCount) ||
      !Array.isArray(v.robots) || v.robots.length !== 11 || !v.robots.every(isRobot) || !uniqueIds(v.robots) ||
      !Array.isArray(v.stations) || v.stations.length !== 4 || !v.stations.every(isStation) || !uniqueIds(v.stations) ||
      !Array.isArray(v.jobs) || !v.jobs.every(isJob) || !uniqueIds(v.jobs) ||
      !Array.isArray(v.faults) || !v.faults.every(isFault) || !Array.isArray(v.aisles) || !v.aisles.every(isAisle) ||
      (v.trafficDeadlocks !== undefined && (!Array.isArray(v.trafficDeadlocks) || !v.trafficDeadlocks.every(isDeadlock)))) return invalid();
  const cells = new Map(layout.cells.map(value => [cellKey(value), value.kind]));
  if (new Set(v.robots.map(cellKey)).size !== v.robots.length ||
      new Set(v.stations.map(cellKey)).size !== v.stations.length ||
      v.robots.some(robot => cells.get(cellKey(robot)) === "wall") ||
      v.stations.some(station => cells.get(cellKey(station)) !== "station")) return invalid();
  return {
    tick: v.tick, seed: v.seed, robots: v.robots, stations: v.stations, jobs: v.jobs, faults: v.faults, aisles: v.aisles,
    completedJobs: v.completedJobs as number | undefined, priorityDeadlinesMet: v.priorityDeadlinesMet as number | undefined,
    priorityDeadlinesMissed: v.priorityDeadlinesMissed as number | undefined, trafficDeadlocks: v.trafficDeadlocks,
  };
}

function isDecision(v: unknown): v is Decision {
  if (!record(v)) return false;
  if (v.kind === "abstain") return id(v.reason);
  if (v.kind === "assign") {
    return Array.isArray(v.assignments) && v.assignments.length > 0 &&
      v.assignments.every(item => record(item) && id(item.jobId) && id(item.robotId));
  }
  const pair = v.reassignment;
  // Go embeds the safety request as-is, using its exported field names.
  return v.kind === "reassign" && record(pair) && id(pair.JobID) && id(pair.FromRobotID) && id(pair.ToRobotID);
}

function isUsage(v: unknown): v is Usage {
  return record(v) && id(v.model) && count(v.inputTokens) && count(v.outputTokens);
}

function isOperation(v: unknown): v is OperationsActivity {
  if (!record(v) || !runtimeId(v.observationId) || !timestamp(v.observedAt) || !count(v.tick) ||
      !member(v.stage, ["observed", "proposed", "finished"]) || !id(v.status) || typeof v.recorded !== "boolean" ||
      ![v.requestId, v.reservedMicroUsd, v.accountedMicroUsd].every(optionalCount) ||
      ![v.modelStatus, v.model, v.goReason].every(optionalText) ||
      ![v.proposedAt, v.finishedAt].every(value => value === undefined || timestamp(value)) ||
      (v.decision !== undefined && !isDecision(v.decision)) || (v.usage !== undefined && !isUsage(v.usage))) return false;
  return v.go === undefined || (record(v.go) && count(v.go.auditId) && optionalCount(v.go.priorAuditId) &&
    member(v.go.execution, ["none", "succeeded", "failed", "unknown"]) && typeof v.go.auditCommitted === "boolean");
}

function parseData(v: Record<string, unknown>, snapshot: unknown, layout: Layout): WarehouseData {
  const operations = v.operationsActivity === undefined ? [] : v.operationsActivity;
  if (!member(v.state, runStates) || !member(v.operationsMode, modes) || !Array.isArray(operations) ||
      operations.length > 12 || !operations.every(isOperation)) return invalid();
  return { state: v.state, mode: v.operationsMode, snapshot: parseSnapshot(snapshot, layout), operations };
}

export function parseWarehouseStatus(value: unknown): WarehouseStatus {
  const identity = parseVisitorIdentity(value);
  if (!record(value) || identity.scenarioId !== warehouseScenario) return invalid();
  const { generation } = identity;
  if (!identity.ready) return { generation, ready: false };
  const evidence = value.evidence;
  if (!record(evidence) || evidence.generation !== generation || evidence.scenarioId !== warehouseScenario ||
      !runtimeId(evidence.runtimeId) || !record(evidence.observation)) return invalid();
  const layout = parseLayout(evidence.observation.layout);
  return { generation, runtimeId: evidence.runtimeId, ready: true, layout, ...parseData(value, value.warehouse, layout) };
}

export function parseWarehouseFrame(event: MessageEvent<string>, status: Extract<WarehouseStatus, { ready: true }>): WarehouseFrame {
  const value: unknown = JSON.parse(event.data);
  if (!record(value) || !member(event.type, warehouseEvents) || !count(value.eventId) || value.eventId === 0 ||
      value.generation !== status.generation || value.runtimeId !== status.runtimeId ||
      event.lastEventId !== status.generation + ":" + value.eventId) return invalid();
  const data = parseData(value, value.snapshot, status.layout);
  if (value.tick !== data.snapshot.tick || data.snapshot.seed !== status.snapshot.seed) return invalid();
  return { ...data, type: event.type, generation: status.generation, runtimeId: status.runtimeId, eventId: value.eventId, layout: status.layout };
}
