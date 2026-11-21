import { activeBreakdown, type Job, type MaintenanceAttempt, type MaintenanceIssue, type MaintenanceState,
  type MaintenanceTarget, type OperationsActivity, type Point, type Robot, type Snapshot, type Station } from "./telemetry";

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
  GOING_TO_REPAIR: "To repair", AT_REPAIR_SITE: "At repair site", REPAIRING: "Repairing",
};

export const cellLabel = (point: Point): string => "Cell " + point.x + ", " + point.y;
export const robotRoleLabel = (robot: Robot): string => robot.role === "maintenance" ? "Maintenance robot" : "Transport robot";

export const breakdownLabels: Record<NonNullable<Robot["activeFault"]>, string> = {
  motor_failure: "Motor failure", sensor_failure: "Sensor failure",
  robot_unresponsive: "Communication loss", critical_battery: "Critical battery",
};
export const maintenancePhaseLabels: Record<MaintenanceState["phase"], string> = {
  available: "Available", assigned: "Assigned", traveling: "Traveling to repair", at_target: "At repair site",
  repairing: "Repairing", verifying: "Verifying recovery", returning: "Returning", unavailable: "Unavailable",
};
export const maintenanceOutcomeLabels: Record<NonNullable<MaintenanceAttempt["outcome"]>, string> = {
  completed: "Recovery verified", failed: "Repair failed", unresolved: "Repair unresolved", cancelled: "Attempt cancelled",
};
export const maintenanceReasonLabels: Record<MaintenanceIssue["reason"] | "attempt_already_recorded", string> = {
  maintenance_unavailable: "M1 unavailable", external_power_required: "External power required",
  awaiting_offline: "Waiting for offline confirmation", service_cell_unreachable: "No legal service cell",
  insufficient_maintenance_energy: "Insufficient maintenance energy", target_changed: "Repair target changed",
  service_position_lost: "Service cell lost", travel_blocked: "Travel blocked",
  target_recovery_blocked: "Recovery verification blocked", self_repair_unsupported: "M1 requires assistance",
  attempt_already_recorded: "Repair attempt already recorded",
};

export function maintenancePhaseLabel(snapshot: Snapshot): string {
  const m1 = snapshot.robots.find(robot => robot.id === "M1");
  const phase = snapshot.maintenance?.phase;
  if (m1?.activeFault || m1?.status === "offline") return "Unavailable";
  if (m1?.status === "charging") return "Charging";
  if (phase === "returning" && m1?.activity === "GOING_TO_CHARGE") return "Returning to charge";
  if (phase === "returning" && m1?.activity === "PARKED") return "Parked";
  if (phase) return maintenancePhaseLabels[phase];
  if (m1?.status === "idle" && !snapshot.stations.some(station => station.occupiedBy === m1.id || station.reservedBy === m1.id)) return "Available";
  return m1 ? robotActivityLabel(m1) : "Unknown";
}

export function sameMaintenanceTarget(a: MaintenanceTarget, b: MaintenanceTarget): boolean {
  return a.robotId === b.robotId && a.faultId === b.faultId;
}

export function maintenanceForRobot(snapshot: Snapshot, robot: Robot) {
  const breakdown = activeBreakdown(snapshot, robot);
  const work = snapshot.maintenance;
  const active = work?.active;
  const ref = breakdown && { robotId: robot.id, faultId: breakdown.id };
  const attempt = robot.role === "maintenance" || (ref && active && sameMaintenanceTarget(ref, active)) ? active : undefined;
  const history = (work?.history ?? []).filter(item => robot.role === "maintenance" ||
    (item.robotId === robot.id && (!robot.activeFault || ref && sameMaintenanceTarget(ref, item))));
  const recent = history.at(-1);
  const reason: keyof typeof maintenanceReasonLabels | undefined = ref && (work?.issues?.find(issue => sameMaintenanceTarget(ref, issue))?.reason ??
    (recent?.outcome ? "attempt_already_recorded" : undefined));
  return { breakdown, attempt, history, recent, reason };
}

export function acceptedMaintenanceAttempt(snapshot: Snapshot, activity: OperationsActivity): MaintenanceAttempt | undefined {
  const decision = activity.decision;
  const receipt = activity.go;
  if (decision?.kind !== "maintenance" || activity.stage !== "finished" || activity.status !== "applied" ||
      receipt?.execution !== "succeeded" || !receipt.auditCommitted || !receipt.auditId) return;
  const active = snapshot.maintenance?.active;
  const matches = (attempt: MaintenanceAttempt) => attempt.aiDirected === true &&
    attempt.startedTick >= activity.tick && sameMaintenanceTarget(attempt, decision.maintenance);
  return active && matches(active) ? active : snapshot.maintenance?.history?.find(matches);
}

export function maintenanceJobResult(snapshot: Snapshot, attempt: MaintenanceAttempt): string | undefined {
  const finished = attempt.finishedTick;
  if (attempt.outcome !== "completed" || finished === undefined || !attempt.jobId) return;
  if (snapshot.maintenance?.history?.some(item => item.jobId === attempt.jobId && item.robotId === attempt.robotId &&
    item.outcome === "completed" && (item.finishedTick ?? 0) > finished)) return;
  const job = snapshot.jobs.find(item => item.id === attempt.jobId && item.assignedRobot === attempt.robotId);
  if (!job) return;
  if (job.status === "done" && job.completedTick !== undefined && job.completedTick > finished) {
    return "Delivered at tick " + job.completedTick;
  }
  const robot = snapshot.robots.find(item => item.id === attempt.robotId);
  if (robot?.jobId === job.id && robot.status === "moving" && !robot.activeFault) {
    if (job.status === "in_progress" && job.cargoRobotId === robot.id) return "Delivery resumed";
    if (job.status === "assigned") return "Pickup route resumed";
  }
}

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
