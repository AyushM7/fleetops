import type { Job, Point, Robot, Snapshot, Station } from "./telemetry";

export type Selection = { kind: "robot" | "job" | "station"; id: string };
export type PickEntity = (selection: Selection, opener: HTMLElement | SVGElement) => void;
export type Entity =
  | { kind: "robot"; robot: Robot }
  | { kind: "job"; job: Job }
  | { kind: "station"; station: Station };

export const activityLabels: Record<Robot["activity"], string> = {
  AVAILABLE: "Available", GOING_TO_PICKUP: "To pickup", PICKING_UP: "Picking up", DELIVERING: "Delivering",
  WAITING_FOR_TRAFFIC: "Traffic wait", GOING_TO_CHARGE: "To charge", CHARGING: "Charging",
  UNDOCKING: "Undocking", GOING_TO_PARK: "To parking", RELOCATING: "Relocating",
  PARKED: "Parked", FAULTED: "Faulted", OUT_OF_SERVICE: "Out of service",
};

export const cellLabel = (point: Point): string => "Cell " + point.x + ", " + point.y;
export const robotRoleLabel = (robot: Robot): string => robot.role === "maintenance" ? "Maintenance robot" : "Transport robot";

export type RobotTone = "moving" | "charging" | "waiting" | "fault";

export function robotTone(robot: Robot): RobotTone | undefined {
  if (robot.status === "faulted" || robot.status === "offline" ||
      robot.activity === "FAULTED" || robot.activity === "OUT_OF_SERVICE") return "fault";
  if (robot.blocked || robot.activity === "WAITING_FOR_TRAFFIC") return "waiting";
  if (robot.activity === "CHARGING") return "charging";
  if (robot.status === "moving") return "moving";
}

export function robotActivityLabel(robot: Robot): string {
  if (robot.status === "offline") return "Offline";
  if (robot.status === "faulted") return "Faulted";
  if (robot.activity === "FAULTED" || robot.activity === "OUT_OF_SERVICE") return activityLabels[robot.activity];
  if (robot.blocked) return "Traffic wait";
  return activityLabels[robot.activity];
}

export const jobLabels: Record<Job["status"], string> = {
  pending: "Pending", assigned: "Assigned", in_progress: "In progress", done: "Completed",
};

export const jobOutcomeLabels: Record<NonNullable<Job["outcome"]>, string> = {
  DELIVERED: "Delivered", ON_TIME: "On time", LATE: "Late",
};

export function isPriorityOverdue(job: Job): boolean {
  return job.type === "PRIORITY" && job.status !== "done" && job.deadlineMissed === true;
}

export function chargerStatus(station: Station): "Occupied" | "Reserved" | "Available" {
  if (station.occupiedBy) return "Occupied";
  return station.reservedBy ? "Reserved" : "Available";
}

export function warehouseCounts(snapshot: Snapshot) {
  return {
    completed: snapshot.completedJobs ?? 0,
    overdue: snapshot.jobs.filter(isPriorityOverdue).length,
    charging: snapshot.robots.filter(robot => robot.activity === "CHARGING").length,
  };
}

export function findEntity(snapshot: Snapshot, selection: Selection | null): Entity | null {
  if (selection?.kind === "robot") {
    const robot = snapshot.robots.find(item => item.id === selection.id);
    return robot ? { kind: "robot", robot } : null;
  }
  if (selection?.kind === "job") {
    const job = snapshot.jobs.find(item => item.id === selection.id);
    return job ? { kind: "job", job } : null;
  }
  if (selection?.kind === "station") {
    const station = snapshot.stations.find(item => item.id === selection.id);
    return station ? { kind: "station", station } : null;
  }
  return null;
}
