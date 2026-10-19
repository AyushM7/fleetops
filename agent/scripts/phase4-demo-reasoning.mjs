// Acceptance-only adapter. This exercises Mastra but is NOT a live LLM demo.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { existsSync } from "node:fs";
import { registerHooks } from "node:module";
import { Agent } from "@mastra/core/agent";
import { noopLogger } from "@mastra/core/logger";
import { createFleetClient, observedFacts, validateReasoning } from "../dist/index.js";
import { instructions } from "../dist/agent.js";
import { reasoningSchema } from "../dist/contracts.js";
import { createFleetTools } from "../dist/tools.js";

// The source fixtures use NodeNext .js specifiers. Resolve those imports only
// within agent/src, matching the existing evaluation source runner.
const sourceRoot = new URL("../src/", import.meta.url).href;
registerHooks({
  resolve(specifier, context, nextResolve) {
    if (context.parentURL?.startsWith(sourceRoot) && specifier.startsWith(".") && specifier.endsWith(".js")) {
      const source = new URL(specifier.slice(0, -3) + ".ts", context.parentURL);
      if (existsSync(source)) return nextResolve(source.href, context);
    }
    return nextResolve(specifier, context);
  },
});
const { offlineModelFixture, reasoningFixture, chargingReasoningFixture } = await import("../src/fixtures.ts");

assert.equal(process.version, "v24.21.0");
assert.ok(process.argv.includes("--submit"), "Explicit --submit opt-in is required.");
const endpoint = new URL(process.env.FLEETOPS_P405_URL);
assert.equal(endpoint.hostname, "127.0.0.1");
const network = globalThis.fetch;
globalThis.fetch = (url, options) => {
  assert.equal(new URL(String(url)).href, endpoint.href);
  return network(url, options);
};
const api = createFleetClient(endpoint.href, process.env.FLEETOPS_P405_PROPOSER_TOKEN);
const state = await api.readFleet();
const candidate = state.reassignmentCandidates[0];
const source = state.robots.find(r => r.id === candidate?.fromRobotId);
const recipient = state.robots.find(r => r.id === candidate?.toRobotId);
const job = state.jobs.find(j => j.id === candidate?.jobId);
const expected = candidate ? { ...reasoningFixture(state), proposedAction: {
  action: "ReassignUnstartedJob", jobId: job.id, fromRobotId: source.id, toRobotId: recipient.id,
  purpose: "incident_recovery", rationale: "Deterministic test adapter: Go admitted this transfer; the obstruction remains and delivery is not guaranteed.",
  evidence: { tick: state.tick, reference: { scope: "snapshot", tick: state.tick, robotId: source.id, type: "traffic_blocked" }, source, recipient, job, candidate },
} } : chargingReasoningFixture(state);
let localCalls = 0;
const model = new Agent({ id: "p405-local-adapter", name: "Acceptance test adapter", instructions,
  model: offlineModelFixture(expected, () => { localCalls++; }), maxRetries: 0 });
model.__setLogger(noopLogger);
const output = await model.generate(JSON.stringify({ observedFacts: observedFacts(state) }), {
  maxSteps: 1, toolChoice: "none", structuredOutput: { schema: reasoningSchema },
});
const grounded = validateReasoning(output.object, state);
assert.deepEqual(grounded, expected);
assert.equal(localCalls, 1);
const freshAPI = { ...api, async readFleet() { const current = await api.readFleet(); assert.deepEqual(current, state); return current; } };
const session = createFleetTools(freshAPI, true);
// A human review session gets a requested 30-minute expiry; Go's clock remains real.
const expiresAt = new Date(Date.now() + 30 * 60_000).toISOString();
const proposal = await session.submit(candidate
  ? { jobId: job.id, fromRobotId: source.id, toRobotId: recipient.id, expiresAt }
  : { robotId: expected.proposedAction.robotId, stationId: expected.proposedAction.stationId, expiresAt });
assert.equal(proposal.status, "pending_approval");
assert.deepEqual(await api.readFleet(), state);
await fs.writeFile(process.env.FLEETOPS_P405_REASONING_FILE, JSON.stringify({
  modelMode: "deterministic local adapter; NOT a live LLM", localCalls, paidRequests: 0,
  observedFacts: observedFacts(state), reasoning: grounded, proposal,
}, null, 2) + "\n");
console.log(JSON.stringify({ proposalId: proposal.proposalId, localCalls, paidRequests: 0 }));
