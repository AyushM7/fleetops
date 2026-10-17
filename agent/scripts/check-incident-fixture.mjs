import assert from "node:assert/strict";
import { createFleetClient, runOffline } from "../dist/index.js";

assert.equal(process.version, "v24.21.0");
const api = createFleetClient(process.env.FLEETOPS_INCIDENT_FIXTURE_URL, process.env.FLEETOPS_INCIDENT_FIXTURE_TOKEN);
const expected = [
  ["R1", "low_battery", "high"], ["R2", "robot_unresponsive", "high"],
  ["R3", "traffic_blocked", "medium"], ["R4", "traffic_blocked", "high"],
  ["R8", "low_battery", "medium"], ["R9", "low_battery", "medium"],
];
const state = await api.readFleet();
const first = await runOffline(api);
const second = await runOffline(api, { allowSubmit: true });
assert.deepEqual(first.incidents.map(i => [i.robotId, i.type, i.severity]), expected);
assert.deepEqual(second.incidents, first.incidents);
for (const incident of first.incidents) {
  assert.deepEqual(incident.telemetry, state.robots.find(r => r.id === incident.robotId));
}
assert.equal(first.recommendedAction, null);
assert.equal(second.recommendedAction, null);
assert.equal(first.proposal.status, "disabled");
assert.equal(second.proposal.status, "not_requested");
assert.equal(first.operatorAttention.length, 6);
console.log("PASS: 6 Go-classified incidents; boundary and healthy cases; coherent telemetry; deterministic agent consumption; zero mutations or model requests.");
