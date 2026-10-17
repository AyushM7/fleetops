import { chargingReasoningFixture, fleetFixture, reasoningFixture } from "../../agent/src/fixtures.js";
import type { EvaluationCase, OfflineAdapter } from "./index.js";

export function foundationCases(): EvaluationCase[] {
  const charging = fleetFixture(50);
  const attention = fleetFixture(14);
  attention.tick = 42;
  attention.robots[0].jobId = "J1";
  attention.activeIncidents = [{ robotId: "R1", type: "low_battery", severity: "high", telemetry: { ...attention.robots[0] } }];
  return [
    { id: "charging-in-place", provenance: "agent/src/fixtures.ts: fleetFixture(50); reasoning.test.ts charging case",
      input: charging, expected: { incidents: [], action: { action: "SetCharging", robotId: "R1", stationId: "S1", purpose: "routine_charging" } } },
    { id: "low-battery-attention", provenance: "agent/src/reasoning.test.ts: incidentSnapshot; detector battery <15 with job; no charging below 30",
      input: attention, expected: { incidents: [{ scope: "snapshot", tick: 42, robotId: "R1", type: "low_battery" }], action: null } },
  ];
}

// This double receives only a detached snapshot and ID, never expected labels or tools.
export const foundationDouble: OfflineAdapter = async (input, id) => ({
  reasoning: id === "charging-in-place" ? chargingReasoningFixture(input) : reasoningFixture(input),
});
