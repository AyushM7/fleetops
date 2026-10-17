import type { ActiveIncident, FleetState } from "../../agent/src/contracts.js";
import { fleetFixture } from "../../agent/src/fixtures.js";

export function incident(state: FleetState, robotIndex: number, type: ActiveIncident["type"], severity: ActiveIncident["severity"]): void {
  const robot = state.robots[robotIndex];
  state.activeIncidents.push({ robotId: robot.id, type, severity, telemetry: { ...robot } });
}

// Exact reasoning fixture from agent/src/reassignment.test.ts, not a new Go capture.
export function recoveryFixture(): FleetState {
  const state = fleetFixture(); state.tick = 42;
  state.robots[0] = { id: "R1", x: 0, y: 3, battery: 100, status: "moving", jobId: "J1", blocked: true, waitTicks: 42 };
  state.robots.push({ id: "R3", x: 4, y: 3, battery: 98, status: "idle", jobId: "", blocked: false, waitTicks: 0 });
  incident(state, 0, "traffic_blocked", "high");
  state.jobs = [{ id: "J1", status: "assigned", assignedRobot: "R1", fromX: 4, fromY: 4, toX: 6, toY: 4 }];
  state.reassignmentCandidates = [{ jobId: "J1", fromRobotId: "R1", toRobotId: "R3", pickupSteps: 1, deliverySteps: 2, chargingExitSteps: 5, requiredBattery: 13 }];
  return state;
}

export function noCandidateFixture(): FleetState {
  const state = recoveryFixture(); state.reassignmentCandidates = [];
  return state;
}

export function offlineJobFixture(): FleetState {
  const state = noCandidateFixture(); state.robots[0].status = "offline";
  state.robots[0].blocked = false; state.robots[0].waitTicks = 0; state.activeIncidents = [];
  incident(state, 0, "robot_unresponsive", "high");
  return state;
}

export function lowBatteryFixture(): FleetState {
  const state = noCandidateFixture(); state.robots[0].battery = 14;
  state.robots[0].blocked = false; state.robots[0].waitTicks = 0; state.activeIncidents = [];
  incident(state, 0, "low_battery", "high");
  return state;
}

export function multipleFixture(withCandidate: boolean): FleetState {
  const state = withCandidate ? recoveryFixture() : lowBatteryFixture();
  state.robots.push({ id: "R2", x: 2, y: 3, battery: 100, status: "offline", jobId: "", blocked: false, waitTicks: 0 });
  incident(state, 2, "robot_unresponsive", "high");
  return state;
}

export function ineligibleFixture(pickedUp: boolean): FleetState {
  const state = noCandidateFixture();
  if (pickedUp) state.jobs[0].status = "in_progress";
  else state.robots[1].battery = 29;
  return state;
}

export function reservedStationFixture(): FleetState {
  const state = fleetFixture(50);
  state.robots.push({ ...state.robots[0], id: "R2", x: 3, y: 3, battery: 100 });
  state.stations[0].reservedBy = "R2";
  return state;
}
