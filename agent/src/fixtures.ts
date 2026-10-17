import type { FleetState, MutationResult } from "./contracts.js";

export function fleetFixture(battery = 100): FleetState {
  return { tick: 0, poisoned: false, activeIncidents: [],
    robots: [{ id: "R1", x: 0, y: 0, battery, status: "idle", jobId: "", waitTicks: 0, blocked: false }],
    stations: [{ id: "S1", x: 0, y: 0, occupiedBy: "", reservedBy: "" }],
  };
}
export const pendingFixture: MutationResult = {
  proposalId: "p1", state: "pending", execution: "none", auditCommitted: true, executionAttempted: false, error: null,
};
