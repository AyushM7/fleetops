import type { FleetObservation, FleetState } from "./contracts.js";

function availableStation(state: FleetState, robot: FleetState["robots"][number], demo = false): FleetState["stations"][number] | undefined {
  if (robot.battery < 30 || (!demo && robot.battery === 100) || robot.status !== "idle" || robot.jobId !== "" || robot.blocked ||
      state.activeIncidents.some(i => i.robotId === robot.id)) return undefined;
  return state.stations.find(s => s.x === robot.x && s.y === robot.y && s.occupiedBy === "" &&
    (s.reservedBy === "" || s.reservedBy === robot.id) &&
    !state.robots.some(other => other.id !== robot.id && other.x === s.x && other.y === s.y));
}

export function observeFleet(state: FleetState, demo?: { robotId: string; stationId: string }): FleetObservation {
  const observations = [`Tick ${state.tick}; ${state.robots.length} robots; ${state.stations.length} stations.`,
    ...state.robots.map(r => `${r.id}: battery ${r.battery}%, ${r.status}, position (${r.x}, ${r.y}).`)];
  const potentialIssues = state.activeIncidents.map(i => `${i.robotId}: ${i.type} (${i.severity}); operator attention required.`);
  if (state.poisoned) potentialIssues.push("Runtime poisoned; manual reconciliation required.");
  const robot = state.robots.find(r => demo ? r.id === demo.robotId : state.activeIncidents.length === 0 && availableStation(state, r) !== undefined);
  const station = robot && availableStation(state, robot, demo !== undefined);
  const chargingAction = !state.poisoned && robot && station && (!demo || station.id === demo.stationId)
    ? { action: "SetCharging" as const, robotId: robot.id, stationId: station.id } : null;
  const candidate = !demo && !state.poisoned ? state.reassignmentCandidates[0] : undefined;
  const recommendedAction = candidate ? { action: "ReassignUnstartedJob" as const, jobId: candidate.jobId,
    fromRobotId: candidate.fromRobotId, toRobotId: candidate.toRobotId } : chargingAction;
  const operatorAttention = state.activeIncidents.map(i => ({ robotId: i.robotId, incidentType: i.type,
    reason: candidate?.fromRobotId === i.robotId && i.type === "traffic_blocked"
      ? "Go admitted a pre-pickup job reassignment candidate; independent operator approval is required. The obstruction remains."
      : "No supported incident recovery action. SetCharging cannot move, unblock, restore offline robots, or charge below 30%." }));
  return { observations, potentialIssues, recommendedAction, incidents: state.activeIncidents, operatorAttention,
    explanation: demo ? "Explicit demonstration requested, not a low-battery incident. Go decides eligibility."
      : "Incidents are current-state classifications from the Go detector; no incident history is retained. ReassignUnstartedJob transfers an eligible blocked source's uncollected job to a Go-admitted idle recipient. It does not remove the obstruction. SetCharging charges in place. Go validates, a human approves, Go executes." };
}
