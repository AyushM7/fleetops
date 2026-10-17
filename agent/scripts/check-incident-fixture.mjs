import assert from "node:assert/strict";
import { Agent } from "@mastra/core/agent";
import { noopLogger } from "@mastra/core/logger";
import { createFleetClient, runOffline, observedFacts, validateReasoning } from "../dist/index.js";
import { reasoningSchema } from "../dist/contracts.js";
import { offlineModelFixture, reasoningFixture } from "../src/fixtures.ts";

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
let localModelCalls = 0;
const agent = new Agent({ id: "incident-contract-check", name: "Incident contract check",
  model: offlineModelFixture(reasoningFixture(state), () => { localModelCalls++; }), maxRetries: 0 });
agent.__setLogger(noopLogger);
const generated = await agent.generate(JSON.stringify({ observedFacts: observedFacts(state) }), {
  maxSteps: 1, toolChoice: "none", structuredOutput: { schema: reasoningSchema },
});
assert.equal(validateReasoning(generated.object, state).interpretations.length, 6);
const stale = structuredClone(generated.object);
stale.interpretations[0].reference.tick++;
assert.throws(() => validateReasoning(stale, state));
assert.equal(localModelCalls, 1);
console.log("PASS: 6 Go-classified incidents; actual Mastra generation with a local provider double; grounded snapshot references; stale-reference rejection; zero mutations or paid model requests.");
