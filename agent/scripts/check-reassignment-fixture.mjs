import assert from "node:assert/strict";
import { Agent } from "@mastra/core/agent";
import { noopLogger } from "@mastra/core/logger";
import { createFleetClient, runOffline, observedFacts, validateReasoning } from "../dist/index.js";
import { reasoningSchema } from "../dist/contracts.js";
import { instructions } from "../dist/agent.js";
import { submitReviewedAction } from "../dist/submission.js";
import { offlineModelFixture, reasoningFixture } from "../dist/fixtures.js";

assert.equal(process.version, "v24.21.0");
const endpoint = process.env.FLEETOPS_REASSIGNMENT_FIXTURE_URL;
const network = globalThis.fetch;
// All network access is restricted to the trusted local Go fixture.
globalThis.fetch = (url, init) => {
  assert.equal(new URL(String(url)).href, new URL(endpoint).href);
  return network(url, init);
};
const api = createFleetClient(endpoint, process.env.FLEETOPS_REASSIGNMENT_FIXTURE_TOKEN);
const state = await api.readFleet();
const offline = await runOffline(api);
assert.equal(offline.proposal.status, "disabled");
assert.equal(offline.recommendedAction.action, "ReassignUnstartedJob");
const candidate = state.reassignmentCandidates.find(c => c.toRobotId === "R3");
assert.ok(candidate);
// Deterministic recommendation uses the first Go candidate in numeric robot order.
assert.deepEqual(candidate, state.reassignmentCandidates[0]);
const source = state.robots.find(r => r.id === candidate.fromRobotId);
const recipient = state.robots.find(r => r.id === candidate.toRobotId);
const job = state.jobs.find(j => j.id === candidate.jobId);
const reasoning = { ...reasoningFixture(state), proposedAction: {
  action: "ReassignUnstartedJob", jobId: job.id, fromRobotId: source.id, toRobotId: recipient.id,
  purpose: "incident_recovery", rationale: "Go admitted this recipient; ownership transfer may restore job progress. The obstruction remains.",
  evidence: { tick: state.tick, reference: { scope: "snapshot", tick: state.tick, robotId: source.id, type: "traffic_blocked" }, source, recipient, job, candidate },
} };
let localCalls = 0;
const agent = new Agent({ id: "reassignment-contract", name: "Recovery contract", instructions,
  model: offlineModelFixture(reasoning, () => { localCalls++; }), maxRetries: 0 });
agent.__setLogger(noopLogger);
const generated = await agent.generate(JSON.stringify({ observedFacts: observedFacts(state), supportedAction: offline.recommendedAction }), {
  maxSteps: 1, toolChoice: "none", structuredOutput: { schema: reasoningSchema },
});
assert.deepEqual(validateReasoning(generated.object, state), reasoning);
assert.equal(localCalls, 1);
const pending = await submitReviewedAction(api, state, offline.recommendedAction, { allowSubmit: true });
assert.equal(pending.proposal.status, "pending_approval");
assert.equal(pending.submissionCheck, "current");
assert.deepEqual(await api.readFleet(), state);
console.log(JSON.stringify({ proposalId: pending.proposal.proposalId, status: pending.proposal.status, localModelCalls: localCalls, paidRequests: 0 }));
